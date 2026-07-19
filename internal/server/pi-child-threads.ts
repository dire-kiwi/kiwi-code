import { getSupportedThinkingLevels, StringEnum } from "@earendil-works/pi-ai";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";

const maxChildren = 8;
// Keep inbox delivery compatible with retained child conversations, but do not
// expose direct child tools. Server-side Dire Mux workflows own orchestration.
const childThreadToolsEnabled = false;
const pollIntervalMs = 750;
const inboxIntervalMs = 1_000;
const perChildOutputBytes = 50 * 1024;

const threadEndpoint = process.env.DIRE_MUX_THREAD_ENDPOINT?.replace(/\/+$/, "") ?? "";
const agentToken = process.env.DIRE_MUX_AGENT_TOKEN ?? "";
const currentProjectID = process.env.DIRE_MUX_PROJECT_ID ?? "";
const currentThreadID = process.env.DIRE_MUX_THREAD_ID ?? "";
const parentThreadID = process.env.DIRE_MUX_PARENT_THREAD_ID ?? "";

type Thread = {
	id: string;
	title: string;
	cwd: string;
	createdAt: string;
	parentThreadId?: string;
	worktree?: boolean;
	branch?: string;
	worktreePath?: string;
	closedAt?: string;
	archivedAt?: string;
};

type ChildRunState = "starting" | "working" | "finished" | "failed";

type ChildRun = {
	id: number;
	state: ChildRunState;
	output?: string;
	error?: string;
	startedAt: string;
	finishedAt?: string;
};

type CreatedChild = {
	thread: Thread;
	run: ChildRun;
	agent: "pi";
};

type ListedChild = {
	thread: Thread;
	run?: ChildRun;
	agent: "pi";
};

type ThreadMessage = {
	id: number;
	fromThreadId: string;
	fromThreadTitle: string;
	message: string;
	createdAt: string;
};

type ChildTask = {
	title: string;
	prompt: string;
	agent?: "pi";
	model?: string;
	thinkingLevel?: "off" | "minimal" | "low" | "medium" | "high" | "xhigh" | "max";
	worktree?: boolean;
	baseBranch?: string;
};

type ChildResult = {
	thread?: Thread;
	run?: ChildRun;
	agent: "pi";
	closed: boolean;
	error?: string;
};

type PiModelOption = {
	id: string;
	label: string;
	reasoningLevels: string[];
};

type ChildTaskValidation = {
	tasks: ChildTask[];
	errors: string[];
	availableModels: PiModelOption[];
};

function ensureAvailable(): void {
	if (!threadEndpoint || !agentToken || !currentProjectID || !currentThreadID) {
		throw new Error("Child threads are only available inside a Dire Mux-managed Pi session.");
	}
}

function closeChildThreadURL(threadID: string): string {
	return `${threadEndpoint}/children/${encodeURIComponent(threadID)}/close`;
}

function formatAvailableModels(models: PiModelOption[]): string {
	if (models.length === 0) return "Available Pi models: none. Configure authentication in Pi and retry.";
	const lines = models.map((model) =>
		`- ${model.id} (reasoning: ${model.reasoningLevels.join(", ") || "default only"})`,
	);
	return `Available Pi models (use an exact provider/model ID):\n${lines.join("\n")}`;
}

function validateChildTasks(tasks: ChildTask[], ctx: ExtensionContext): ChildTaskValidation {
	const byID = new Map<string, PiModelOption>();
	for (const model of ctx.modelRegistry.getAvailable()) {
		const id = `${model.provider}/${model.id}`;
		if (byID.has(id)) continue;
		byID.set(id, {
			id,
			label: `${model.name} · ${model.provider}`,
			reasoningLevels: [...getSupportedThinkingLevels(model)],
		});
	}
	const availableModels = [...byID.values()].sort((left, right) => left.id.localeCompare(right.id));
	const inheritedModel = ctx.model ? `${ctx.model.provider}/${ctx.model.id}` : "";
	const errors: string[] = [];
	const resolvedTasks = tasks.map((task) => {
		const requestedModel = task.model?.trim() || inheritedModel;
		const model = byID.get(requestedModel);
		if (!requestedModel) {
			errors.push(`${task.title}: no model was requested and the parent Pi session has no active model.`);
			return task;
		}
		if (!model) {
			errors.push(`${task.title}: Pi model "${requestedModel}" is not available.`);
			return { ...task, model: requestedModel };
		}
		if (task.thinkingLevel && !model.reasoningLevels.includes(task.thinkingLevel)) {
			errors.push(
				`${task.title}: reasoning level "${task.thinkingLevel}" is unavailable for ${requestedModel}; choose ${model.reasoningLevels.join(", ") || "the model default"}.`,
			);
		}
		return { ...task, model: requestedModel };
	});
	return { tasks: resolvedTasks, errors, availableModels };
}

async function request<T>(url: string, init: RequestInit = {}, signal?: AbortSignal): Promise<T> {
	ensureAvailable();
	const response = await fetch(url, {
		...init,
		headers: {
			"Content-Type": "application/json",
			"X-Dire-Mux-Agent-Token": agentToken,
			...init.headers,
		},
		signal,
	});
	if (!response.ok) {
		let message = `Dire Mux returned ${response.status}`;
		try {
			const body = await response.json() as {
				error?: unknown;
				availableModels?: PiModelOption[];
			};
			if (typeof body.error === "string" && body.error) message = body.error;
			if (Array.isArray(body.availableModels)) {
				message += `\n\n${formatAvailableModels(body.availableModels)}`;
			}
		} catch {
			// Keep the HTTP status fallback.
		}
		throw new Error(message);
	}
	if (response.status === 204) return undefined as T;
	return response.json() as Promise<T>;
}

function sleep(milliseconds: number, signal?: AbortSignal): Promise<void> {
	return new Promise((resolve, reject) => {
		if (signal?.aborted) {
			reject(new Error("Child-thread operation was aborted."));
			return;
		}
		const timeout = setTimeout(() => {
			signal?.removeEventListener("abort", aborted);
			resolve();
		}, milliseconds);
		const aborted = () => {
			clearTimeout(timeout);
			reject(new Error("Child-thread operation was aborted."));
		};
		signal?.addEventListener("abort", aborted, { once: true });
	});
}

function truncateOutput(value: string): string {
	const bytes = Buffer.byteLength(value, "utf8");
	if (bytes <= perChildOutputBytes) return value;
	let output = value.slice(0, perChildOutputBytes);
	while (Buffer.byteLength(output, "utf8") > perChildOutputBytes) output = output.slice(0, -1);
	return `${output}\n\n[Output truncated by ${bytes - Buffer.byteLength(output, "utf8")} bytes.]`;
}

async function createChild(task: ChildTask, signal?: AbortSignal): Promise<CreatedChild> {
	return request<CreatedChild>(`${threadEndpoint}/children`, {
		method: "POST",
		body: JSON.stringify(task),
	}, signal);
}

async function readRun(threadID: string, runID: number, signal?: AbortSignal): Promise<ChildRun> {
	return request<ChildRun>(
		`${threadEndpoint}/children/${encodeURIComponent(threadID)}/runs/${encodeURIComponent(String(runID))}`,
		{},
		signal,
	);
}

async function waitForRun(
	created: CreatedChild,
	signal: AbortSignal | undefined,
	onState?: (run: ChildRun) => void,
): Promise<ChildRun> {
	let run = created.run;
	while (run.state === "starting" || run.state === "working") {
		onState?.(run);
		await sleep(pollIntervalMs, signal);
		run = await readRun(created.thread.id, run.id, signal);
	}
	onState?.(run);
	return run;
}

async function closeChild(thread: Thread): Promise<string | undefined> {
	try {
		await request<Thread>(closeChildThreadURL(thread.id), { method: "POST", body: "{}" });
		return undefined;
	} catch (reason) {
		return reason instanceof Error ? reason.message : String(reason);
	}
}

function formatResults(results: ChildResult[], heading: string): string {
	const sections = results.map((result) => {
		if (!result.thread) return `### Child creation failed\n\n${result.error ?? "Unknown error"}`;
		const run = result.run;
		const state = run?.state ?? "failed";
		const location = result.thread.worktree
			? `${result.thread.cwd}${result.thread.branch ? `\nBranch: ${result.thread.branch}` : ""}`
			: result.thread.cwd;
		const lifecycle = result.closed
			? "Closed and retained in Dire Mux for review."
			: "Thread remains open.";
		const output = run?.output
			? truncateOutput(run.output)
			: run?.error ?? result.error ?? "(no output)";
		const warning = result.error ?? (run?.output ? run.error : undefined);
		return [
			`### ${result.thread.title} (${result.thread.id}) — ${state}`,
			`Location: ${location}`,
			lifecycle,
			...(warning ? [`Warning: ${warning}`] : []),
			"",
			output,
		].join("\n");
	});
	return `${heading}\n\n${sections.join("\n\n---\n\n")}`;
}

const ThinkingLevel = StringEnum(
	["off", "minimal", "low", "medium", "high", "xhigh", "max"] as const,
	{ description: "Reasoning level supported by the selected model" },
);
const ChildTaskSchema = Type.Object({
	title: Type.String({ description: "Short descriptive title for the child thread" }),
	prompt: Type.String({ description: "Complete task prompt for the child agent" }),
	agent: Type.Optional(StringEnum(["pi"] as const, { description: "Agent harness. Only Pi is supported for now." })),
	model: Type.Optional(Type.String({ description: "Exact authenticated Pi provider/model identifier. Defaults to the parent Pi model." })),
	thinkingLevel: Type.Optional(ThinkingLevel),
	worktree: Type.Optional(Type.Boolean({ description: "Use an isolated Git worktree. Defaults to true for Git projects." })),
	baseBranch: Type.Optional(Type.String({ description: "Optional local branch used as the worktree base" })),
});

function registerChildCreationTool(pi: ExtensionAPI): void {
	pi.registerTool({
		name: "create_child_threads",
		label: "Create Child Threads",
		description: "Create one or more Dire Mux child threads and start Pi agents concurrently. The complete batch is validated against Pi's authenticated models and model-specific reasoning levels before any child starts. Wait for all results by default, or run them in the background. Child prompts define the work; this tool does not assign fixed roles.",
		promptSnippet: "Create isolated child threads for parallel, background, or chained delegated work",
		promptGuidelines: [
			"Use create_child_threads when delegated work needs isolated context or parallel implementations; put independent tasks in one call so they start concurrently.",
			"Use isolated worktrees for child threads that may edit files concurrently, include all task-specific expectations in each child prompt, and commit any baseline changes those worktrees must inherit before spawning them.",
			"Use exact provider/model IDs. If a model or reasoning level is unavailable, no child starts and the tool returns the authenticated Pi models with their supported reasoning levels.",
		],
		parameters: Type.Object({
			tasks: Type.Array(ChildTaskSchema, { minItems: 1, maxItems: maxChildren }),
			wait: Type.Optional(Type.Boolean({ description: "Wait for every child to settle. Defaults to true." })),
			closeOnComplete: Type.Optional(Type.Boolean({ description: "Close settled child agents while retaining their conversations and worktrees for review. Defaults to true when waiting." })),
		}),
		async execute(_toolCallID, params, signal, onUpdate, ctx) {
			ensureAvailable();
			const validation = validateChildTasks(params.tasks, ctx);
			if (validation.errors.length > 0) {
				return {
					content: [{
						type: "text",
						text: `No child threads were started because model validation failed:\n- ${validation.errors.join("\n- ")}\n\n${formatAvailableModels(validation.availableModels)}`,
					}],
					details: {
						mode: "validation-error",
						errors: validation.errors,
						availableModels: validation.availableModels,
					},
				};
			}
			const tasks = validation.tasks;
			const wait = params.wait ?? true;
			const closeOnComplete = wait && (params.closeOnComplete ?? true);
			let createdCount = 0;
			const creations = await Promise.all(tasks.map(async (task): Promise<ChildResult> => {
				try {
					const created = await createChild(task, signal);
					createdCount += 1;
					onUpdate?.({
						content: [{ type: "text", text: `Started ${createdCount}/${tasks.length} child threads.` }],
						details: { created: createdCount, total: tasks.length },
					});
					return { thread: created.thread, run: created.run, agent: created.agent, closed: false };
				} catch (reason) {
					return {
						agent: "pi",
						closed: false,
						error: reason instanceof Error ? reason.message : String(reason),
					};
				}
			}));

			if (!wait) {
				return {
					content: [{ type: "text", text: formatResults(creations, `Started ${createdCount}/${tasks.length} child threads in the background.`) }],
					details: { mode: "background", results: creations },
				};
			}

			let settledCount = creations.filter((result) => !result.thread || !result.run).length;
			const results = await Promise.all(creations.map(async (result): Promise<ChildResult> => {
				if (!result.thread || !result.run) return result;
				const created: CreatedChild = { thread: result.thread, run: result.run, agent: result.agent };
				try {
					const run = await waitForRun(created, signal, (current) => {
						if (current.state !== "starting" && current.state !== "working") settledCount += 1;
						onUpdate?.({
							content: [{ type: "text", text: `Child threads: ${Math.min(settledCount, creations.length)}/${creations.length} settled.` }],
							details: { settled: Math.min(settledCount, creations.length), total: creations.length },
						});
					});
					let closeError: string | undefined;
					if (closeOnComplete) closeError = await closeChild(result.thread);
					return {
						...result,
						run,
						closed: closeOnComplete && !closeError,
						error: closeError ? `Could not close child: ${closeError}` : undefined,
					};
				} catch (reason) {
					return { ...result, error: reason instanceof Error ? reason.message : String(reason) };
				}
			}));
			return {
				content: [{ type: "text", text: formatResults(results, `Completed ${results.filter((result) => result.run?.state === "finished").length}/${results.length} child runs.`) }],
				details: { mode: "blocking", results },
			};
		},
	});
}

function registerChildOrchestrationTools(pi: ExtensionAPI): void {
	if (!childThreadToolsEnabled) return;
	registerChildCreationTool(pi);

	pi.registerTool({
		name: "list_child_threads",
		label: "List Child Threads",
		description: "List direct child threads of the current Dire Mux thread and their latest Pi run state.",
		parameters: Type.Object({}),
		async execute(_toolCallID, _params, signal) {
			const children = await request<ListedChild[]>(`${threadEndpoint}/children`, {}, signal);
			const text = children.length === 0
				? "This thread has no open child threads."
				: children.map(({ thread, run }) =>
					`${thread.id}\t${run?.state ?? "idle"}\t${thread.title}\t${thread.cwd}`,
				).join("\n");
			return { content: [{ type: "text", text }], details: { children } };
		},
	});

	pi.registerTool({
		name: "wait_for_child_threads",
		label: "Wait For Child Threads",
		description: "Wait for existing background child threads to settle and optionally close their agents while retaining their conversations and worktrees for review.",
		parameters: Type.Object({
			threadIds: Type.Array(Type.String(), { minItems: 1, maxItems: maxChildren }),
			closeOnComplete: Type.Optional(Type.Boolean({ description: "Defaults to true." })),
		}),
		async execute(_toolCallID, params, signal, onUpdate) {
			const listed = await request<ListedChild[]>(`${threadEndpoint}/children`, {}, signal);
			const byID = new Map(listed.map((child) => [child.thread.id, child]));
			let settled = 0;
			const results = await Promise.all(params.threadIds.map(async (threadID): Promise<ChildResult> => {
				const child = byID.get(threadID);
				if (!child) return { agent: "pi", closed: false, error: `Unknown direct child thread: ${threadID}` };
				if (!child.run) return { thread: child.thread, agent: child.agent, closed: false, error: "The child has no Pi run to wait for." };
				const run = await waitForRun({ thread: child.thread, run: child.run, agent: child.agent }, signal);
				settled += 1;
				onUpdate?.({
					content: [{ type: "text", text: `Child threads: ${settled}/${params.threadIds.length} settled.` }],
					details: { settled, total: params.threadIds.length },
				});
				const shouldClose = params.closeOnComplete ?? true;
				const closeError = shouldClose ? await closeChild(child.thread) : undefined;
				return {
					thread: child.thread,
					run,
					agent: child.agent,
					closed: shouldClose && !closeError,
					error: closeError,
				};
			}));
			return {
				content: [{ type: "text", text: formatResults(results, `Settled ${settled}/${params.threadIds.length} requested child threads.`) }],
				details: { mode: "wait", results },
			};
		},
	});
}

export default function (pi: ExtensionAPI) {
	registerChildOrchestrationTools(pi);

	if (childThreadToolsEnabled) pi.registerTool({
		name: "send_thread_message",
		label: "Send Thread Message",
		description: parentThreadID
			? "Send a message to this thread's direct parent by omitting threadId, or to one of its direct child threads by ID."
			: "Send a message to one of this thread's direct child threads by thread ID.",
		promptSnippet: "Exchange messages between a parent thread and its direct children",
		promptGuidelines: [
			"Use send_thread_message for explicit parent-child progress, clarification, or handoff messages; a child may omit threadId to address its parent.",
		],
		parameters: Type.Object({
			threadId: Type.Optional(Type.String({ description: "Direct child target, or omit in a child thread to target its parent" })),
			message: Type.String({ description: "Message delivered into the related thread's Pi context" }),
		}),
		async execute(_toolCallID, params, signal) {
			const delivered = await request<ThreadMessage>(`${threadEndpoint}/messages`, {
				method: "POST",
				body: JSON.stringify({ threadId: params.threadId ?? "", message: params.message }),
			}, signal);
			return {
				content: [{ type: "text", text: `Delivered message ${delivered.id} to the related thread.` }],
				details: { message: delivered },
			};
		},
	});

	let inboxTimer: ReturnType<typeof setTimeout> | undefined;
	let inboxController: AbortController | undefined;
	let stopped = true;

	const scheduleInbox = () => {
		if (stopped) return;
		inboxTimer = setTimeout(() => void pollInbox(), inboxIntervalMs);
	};
	const pollInbox = async () => {
		if (stopped || !threadEndpoint || !agentToken) return;
		const controller = new AbortController();
		inboxController = controller;
		try {
			const messages = await request<ThreadMessage[]>(`${threadEndpoint}/messages/receive`, {
				method: "POST",
				body: "{}",
			}, controller.signal);
			if (!Array.isArray(messages)) return;
			for (const message of messages) {
				pi.sendMessage({
					customType: "dire-mux-thread-message",
					content: `[Dire Mux message from thread ${message.fromThreadTitle} (${message.fromThreadId})]\n${message.message}`,
					display: true,
					details: message,
				}, { deliverAs: "steer", triggerTurn: true });
			}
		} catch {
			// A backend restart or transient network failure must not stop future polls.
		} finally {
			if (inboxController === controller) inboxController = undefined;
			scheduleInbox();
		}
	};

	pi.on("session_start", (_event, ctx) => {
		// Print/JSON invocations must be able to exit after their one-shot work;
		// a recurring inbox timer would keep the Node process alive indefinitely.
		if (ctx.mode !== "tui" && ctx.mode !== "rpc") return;
		stopped = false;
		if (threadEndpoint && agentToken) void pollInbox();
	});
	pi.on("session_shutdown", () => {
		stopped = true;
		if (inboxTimer) clearTimeout(inboxTimer);
		inboxTimer = undefined;
		inboxController?.abort();
		inboxController = undefined;
	});
}
