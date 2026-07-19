import { readFileSync } from "node:fs";
import { dirname } from "node:path";
import {
	parseFrontmatter,
	type ExtensionAPI,
	type ExtensionContext,
} from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";

const pollIntervalMs = 750;
const maxVisibleResultBytes = 50 * 1024;
const threadEndpoint = process.env.DIRE_MUX_THREAD_ENDPOINT?.replace(/\/+$/, "") ?? "";
const agentToken = process.env.DIRE_MUX_AGENT_TOKEN ?? "";

type WorkflowState = "queued" | "running" | "finished" | "failed" | "stopped";

type WorkflowAgent = {
	id: string;
	label: string;
	state: "starting" | "working" | "finished" | "failed";
	error?: string;
};

type WorkflowRun = {
	id: string;
	state: WorkflowState;
	name: string;
	currentPhase?: string;
	error?: string;
	result?: unknown;
	agents: WorkflowAgent[];
};

type ForkedSkill = {
	name: string;
	description: string;
	filePath: string;
	baseDir: string;
	body: string;
	agent: string;
	argumentNames: string[];
	disableModelInvocation: boolean;
};

type SkillFrontmatter = {
	[key: string]: unknown;
	name?: unknown;
	description?: unknown;
	context?: unknown;
	agent?: unknown;
	arguments?: unknown;
	"disable-model-invocation"?: unknown;
};

function ensureAvailable(): void {
	if (!threadEndpoint || !agentToken) {
		throw new Error("Forked skills are only available inside a Dire Mux-managed Pi session.");
	}
}

async function request<T>(path: string, init: RequestInit = {}, signal?: AbortSignal): Promise<T> {
	ensureAvailable();
	let response: Response;
	try {
		response = await fetch(`${threadEndpoint}${path}`, {
			...init,
			headers: {
				"Content-Type": "application/json",
				"X-Dire-Mux-Agent-Token": agentToken,
				...init.headers,
			},
			signal,
		});
	} catch (reason) {
		if (signal?.aborted) throw new Error("Forked skill execution was cancelled.");
		throw new Error(`Could not reach Dire Mux: ${reason instanceof Error ? reason.message : String(reason)}`);
	}
	if (!response.ok) {
		let message = `Dire Mux returned HTTP ${response.status}.`;
		try {
			const body = await response.json() as { error?: unknown };
			if (typeof body.error === "string" && body.error.trim()) message = body.error.trim();
		} catch {
			// Keep the HTTP fallback.
		}
		throw new Error(message);
	}
	if (response.status === 204) return undefined as T;
	return response.json() as Promise<T>;
}

function sleep(milliseconds: number, signal?: AbortSignal): Promise<void> {
	return new Promise((resolve, reject) => {
		if (signal?.aborted) {
			reject(new Error("Forked skill execution was cancelled."));
			return;
		}
		const timer = setTimeout(() => {
			signal?.removeEventListener("abort", cancel);
			resolve();
		}, milliseconds);
		const cancel = () => {
			clearTimeout(timer);
			reject(new Error("Forked skill execution was cancelled."));
		};
		signal?.addEventListener("abort", cancel, { once: true });
	});
}

function settled(state: WorkflowState): boolean {
	return state === "finished" || state === "failed" || state === "stopped";
}

function progress(run: WorkflowRun): string {
	const finished = run.agents.filter((agent) => agent.state === "finished").length;
	const failed = run.agents.filter((agent) => agent.state === "failed").length;
	const active = run.agents.filter((agent) => agent.state === "starting" || agent.state === "working").length;
	return `${run.name} (${run.id}) — ${run.state}: ${finished} finished, ${failed} failed, ${active} active.`;
}

async function stopWorkflow(runID: string): Promise<void> {
	await request(`/workflows/${encodeURIComponent(runID)}/stop`, {
		method: "POST",
		body: "{}",
	});
}

async function waitForWorkflow(
	run: WorkflowRun,
	signal: AbortSignal | undefined,
	onUpdate?: (run: WorkflowRun) => void,
): Promise<WorkflowRun> {
	try {
		while (!settled(run.state)) {
			onUpdate?.(run);
			await sleep(pollIntervalMs, signal);
			run = await request<WorkflowRun>(`/workflows/${encodeURIComponent(run.id)}`, {}, signal);
		}
		onUpdate?.(run);
		return run;
	} catch (reason) {
		if (signal?.aborted) {
			try { await stopWorkflow(run.id); } catch { /* The backend may already be stopping it. */ }
		}
		throw reason;
	}
}

function frontmatterArgumentNames(value: unknown): string[] {
	const candidates = Array.isArray(value)
		? value
		: typeof value === "string"
			? value.split(/[\s,]+/)
			: [];
	return candidates
		.filter((candidate): candidate is string => typeof candidate === "string")
		.map((candidate) => candidate.trim())
		.filter((candidate) =>
			candidate !== "ARGUMENTS" && /^[A-Za-z_][A-Za-z0-9_-]*$/.test(candidate),
		);
}

function loadedSkillCommands(pi: ExtensionAPI) {
	return pi.getCommands().filter((command) =>
		command.source === "skill" &&
		command.name.startsWith("skill:") &&
		typeof command.sourceInfo?.path === "string" &&
		command.sourceInfo.path.length > 0,
	);
}

function loadForkedSkills(pi: ExtensionAPI): ForkedSkill[] {
	const skills: ForkedSkill[] = [];
	for (const command of loadedSkillCommands(pi)) {
		try {
			const contents = readFileSync(command.sourceInfo.path, "utf8");
			const parsed = parseFrontmatter<SkillFrontmatter>(contents);
			if (typeof parsed.frontmatter.context !== "string" || parsed.frontmatter.context.trim().toLowerCase() !== "fork") {
				continue;
			}
			const frontmatterName = typeof parsed.frontmatter.name === "string" ? parsed.frontmatter.name.trim() : "";
			const commandName = command.name.slice("skill:".length);
			const name = frontmatterName || commandName;
			if (!name || name !== commandName) continue;
			skills.push({
				name,
				description: typeof parsed.frontmatter.description === "string"
					? parsed.frontmatter.description.trim()
					: command.description?.trim() ?? "",
				filePath: command.sourceInfo.path,
				baseDir: command.sourceInfo.baseDir || dirname(command.sourceInfo.path),
				body: parsed.body.trim(),
				agent: typeof parsed.frontmatter.agent === "string" ? parsed.frontmatter.agent.trim() : "",
				argumentNames: frontmatterArgumentNames(parsed.frontmatter.arguments),
				disableModelInvocation: parsed.frontmatter["disable-model-invocation"] === true,
			});
		} catch {
			// Pi owns skill diagnostics. Ignore an unreadable or malformed skill here
			// so normal skill handling can report it without breaking every prompt.
		}
	}
	return skills;
}

function parseSkillInvocation(text: string): { name: string; arguments: string } | undefined {
	const match = text.match(/^\/skill:([^\s]+)(?:\s+([\s\S]*))?$/);
	if (!match) return undefined;
	return { name: match[1], arguments: (match[2] ?? "").trim() };
}

function splitArguments(value: string): string[] {
	const result: string[] = [];
	let current = "";
	let quote = "";
	let escaped = false;
	let started = false;
	for (const character of value) {
		if (escaped) {
			current += character;
			escaped = false;
			started = true;
			continue;
		}
		if (character === "\\" && quote !== "'") {
			escaped = true;
			started = true;
			continue;
		}
		if (quote) {
			if (character === quote) quote = "";
			else current += character;
			started = true;
			continue;
		}
		if (character === "'" || character === '"') {
			quote = character;
			started = true;
			continue;
		}
		if (/\s/.test(character)) {
			if (started) {
				result.push(current);
				current = "";
				started = false;
			}
			continue;
		}
		current += character;
		started = true;
	}
	if (escaped) current += "\\";
	if (started) result.push(current);
	return result;
}

function escapeRegExp(value: string): string {
	return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function renderSkillBody(skill: ForkedSkill, rawArguments: string): string {
	const positional = splitArguments(rawArguments);
	let body = skill.body;
	let substituted = false;
	body = body.replace(/\$ARGUMENTS\[(\d+)]/g, (_match, rawIndex: string) => {
		substituted = true;
		return positional[Number(rawIndex)] ?? "";
	});
	body = body.replace(/\$(\d+)/g, (_match, rawIndex: string) => {
		substituted = true;
		return positional[Number(rawIndex)] ?? "";
	});
	for (let index = 0; index < skill.argumentNames.length; index += 1) {
		const name = skill.argumentNames[index];
		const expression = new RegExp(`\\$\\{${escapeRegExp(name)}\\}|\\$${escapeRegExp(name)}(?![A-Za-z0-9_-])`, "g");
		body = body.replace(expression, () => {
			substituted = true;
			return positional[index] ?? "";
		});
	}
	body = body.replace(/\$ARGUMENTS(?![\[A-Za-z0-9_-])/g, () => {
		substituted = true;
		return rawArguments;
	});
	if (rawArguments && !substituted) body += `\n\nARGUMENTS: ${rawArguments}`;
	return body.trim();
}

function agentProfileInstruction(agent: string): string {
	switch (agent.toLowerCase()) {
		case "":
		case "general-purpose":
		case "general_purpose":
		case "pi":
			return "";
		case "explore":
			return "The skill selected the Explore agent profile. Inspect and report without modifying files.";
		case "plan":
			return "The skill selected the Plan agent profile. Analyze and produce a plan without modifying files.";
		default:
			return `The skill requested the agent profile ${JSON.stringify(agent)}. Dire Mux runs Pi Native workers, so treat this name as a specialization hint; Claude custom-agent configuration is not loaded.`;
	}
}

function renderSkillPrompt(skill: ForkedSkill, rawArguments: string): string {
	const profile = agentProfileInstruction(skill.agent);
	return [
		`<dire_mux_forked_skill name="${skill.name}">`,
		`You are already the isolated child executing Agent Skill ${JSON.stringify(skill.name)}. Follow its instructions directly and do not invoke run_forked_skill for this same skill again.`,
		profile,
		`Skill file: ${skill.filePath}`,
		`References in the skill are relative to ${skill.baseDir}.`,
		"",
		renderSkillBody(skill, rawArguments),
		"</dire_mux_forked_skill>",
	].filter((line) => line !== "").join("\n");
}

function workflowScript(skill: ForkedSkill): string {
	const metadataName = `skill:${skill.name}`;
	const metadataDescription = skill.description || `Run ${skill.name} in a forked child context`;
	return `export const meta = { name: ${JSON.stringify(metadataName)}, description: ${JSON.stringify(metadataDescription)} }\n` +
		`return await agent(args.prompt, { label: args.label, isolation: 'shared', closeOnComplete: true })\n`;
}

function visibleText(text: string): string {
	const bytes = Buffer.byteLength(text, "utf8");
	if (bytes <= maxVisibleResultBytes) return text;
	let visible = text.slice(0, maxVisibleResultBytes);
	while (Buffer.byteLength(visible, "utf8") > maxVisibleResultBytes) visible = visible.slice(0, -1);
	return `${visible}\n\n[Forked skill result truncated by ${bytes - Buffer.byteLength(visible, "utf8")} bytes.]`;
}

function visibleJSON(value: unknown): string {
	try {
		return visibleText(JSON.stringify(value, null, 2) ?? String(value));
	} catch {
		return visibleText(String(value));
	}
}

function completedOutput(run: WorkflowRun, skill: ForkedSkill): string {
	if (run.state !== "finished") {
		throw new Error(run.error || `Forked skill ${skill.name} ended in state ${run.state}.`);
	}
	const failed = run.agents.find((agent) => agent.state === "failed");
	if (failed) throw new Error(failed.error || `The child running ${skill.name} failed.`);
	if (typeof run.result === "string") return run.result ? visibleText(run.result) : "(no output)";
	if (run.result === undefined || run.result === null) {
		throw new Error(`The child running ${skill.name} did not return a result.`);
	}
	return visibleJSON(run.result);
}

function xml(value: string): string {
	return value
		.replace(/&/g, "&amp;")
		.replace(/</g, "&lt;")
		.replace(/>/g, "&gt;")
		.replace(/"/g, "&quot;")
		.replace(/'/g, "&apos;");
}

function executingForkedSkill(prompt: string): string {
	return prompt.match(/<dire_mux_forked_skill name="([a-z0-9-]+)">/)?.[1] ?? "";
}

function removePiSkillEntries(systemPrompt: string, skills: ForkedSkill[]): string {
	for (const skill of skills) {
		const pattern = new RegExp(
			`\\n  <skill>\\n    <name>${escapeRegExp(xml(skill.name))}<\\/name>[\\s\\S]*?` +
			`\\n    <location>${escapeRegExp(xml(skill.filePath))}<\\/location>\\n  <\\/skill>`,
			"g",
		);
		systemPrompt = systemPrompt.replace(pattern, "");
	}
	return systemPrompt;
}

function forkedSkillGuidance(skills: ForkedSkill[]): string {
	const entries = skills.map((skill) => [
		"  <skill>",
		`    <name>${xml(skill.name)}</name>`,
		`    <description>${xml(skill.description)}</description>`,
		...(skill.agent ? [`    <agent>${xml(skill.agent)}</agent>`] : []),
		"  </skill>",
	].join("\n"));
	return `\n\nThe following skills declare \`context: fork\`. To execute one, do not read its SKILL.md into the parent context. Call \`run_forked_skill\` with its name and the relevant user request as \`arguments\`. The tool renders the skill and runs it in a visible, isolated Pi Native child thread through a Dire Mux workflow.\n\n<forked_skills>\n${entries.join("\n")}\n</forked_skills>`;
}

export default function (pi: ExtensionAPI) {
	pi.registerTool({
		name: "run_forked_skill",
		label: "Run Forked Skill",
		description: "Run a loaded Agent Skill whose SKILL.md frontmatter declares context: fork. The skill body becomes the task for one visible Pi Native child thread in the current workspace. Pass the user's relevant request as arguments; Claude-style $ARGUMENTS, indexed, positional, and named placeholders are rendered before execution. The child inherits the current Pi model and thinking level and is retained in Dire Mux for review.",
		promptSnippet: "Run skills with context: fork in visible Dire Mux child threads",
		promptGuidelines: [
			"Use run_forked_skill instead of read when an available skill is listed under forked_skills or the user explicitly invokes such a skill.",
			"Pass the complete relevant user request to run_forked_skill.arguments; do not execute a context: fork skill in the parent context.",
		],
		parameters: Type.Object({
			skill: Type.String({ description: "Exact loaded skill name without the skill: prefix" }),
			arguments: Type.Optional(Type.String({ description: "User arguments or request supplied to the skill" })),
		}),
		async execute(_toolCallID, params, signal, onUpdate, ctx: ExtensionContext) {
			const skillName = params.skill.trim();
			const skill = loadForkedSkills(pi).find((candidate) => candidate.name === skillName);
			if (!skill) {
				const available = loadForkedSkills(pi).map((candidate) => candidate.name);
				throw new Error(`Unknown context: fork skill ${JSON.stringify(skillName)}. Available forked skills: ${available.join(", ") || "none"}.`);
			}
			const model = ctx.model ? `${ctx.model.provider}/${ctx.model.id}` : "";
			const thinkingLevel = pi.getThinkingLevel();
			const label = skill.agent ? `${skill.name} · ${skill.agent}` : skill.name;
			const body: Record<string, unknown> = {
				script: workflowScript(skill),
				args: {
					prompt: renderSkillPrompt(skill, params.arguments?.trim() ?? ""),
					label,
				},
				closeOnComplete: true,
			};
			if (model) body.model = model;
			if (thinkingLevel) body.thinkingLevel = thinkingLevel;
			let run = await request<WorkflowRun>("/workflows", {
				method: "POST",
				body: JSON.stringify(body),
			}, signal);
			run = await waitForWorkflow(run, signal, (current) => onUpdate?.({
				content: [{ type: "text", text: progress(current) }],
				details: { skill: skill.name, agent: skill.agent || "general-purpose", run: current },
			}));
			const output = completedOutput(run, skill);
			return {
				content: [{ type: "text", text: output }],
				details: {
					skill: skill.name,
					skillPath: skill.filePath,
					agent: skill.agent || "general-purpose",
					run,
				},
			};
		},
	});

	pi.on("input", (event) => {
		const invocation = parseSkillInvocation(event.text);
		if (!invocation) return { action: "continue" as const };
		const skill = loadForkedSkills(pi).find((candidate) => candidate.name === invocation.name);
		if (!skill) return { action: "continue" as const };
		return {
			action: "transform" as const,
			text: [
				`The user explicitly invoked the context: fork skill ${JSON.stringify(skill.name)}.`,
				"Call run_forked_skill now exactly once with this JSON input:",
				JSON.stringify({ skill: skill.name, arguments: invocation.arguments }),
				"Do not read the skill file or carry out its instructions in this parent context. Return the child result to the user.",
			].join("\n"),
		};
	});

	pi.on("before_agent_start", (event) => {
		const executing = executingForkedSkill(event.prompt);
		const modelInvocableSkills = loadForkedSkills(pi).filter((skill) => !skill.disableModelInvocation);
		const skills = modelInvocableSkills.filter((skill) => skill.name !== executing);
		let systemPrompt = removePiSkillEntries(event.systemPrompt, modelInvocableSkills);
		if (skills.length > 0) systemPrompt += forkedSkillGuidance(skills);
		if (executing) {
			systemPrompt += `\n\nYou are already executing the context: fork skill ${JSON.stringify(executing)} in its child thread. Follow the supplied skill body directly and do not invoke run_forked_skill for that same skill.`;
		}
		if (systemPrompt !== event.systemPrompt) return { systemPrompt };
	});
}
