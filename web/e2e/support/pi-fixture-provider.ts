import { appendFileSync, mkdirSync, readFileSync } from "node:fs";
import { isAbsolute, join } from "node:path";

import {
	fauxAssistantMessage,
	fauxProvider,
	fauxText,
	fauxThinking,
	fauxToolCall,
} from "@earendil-works/pi-ai";
import type {
	Context,
	FauxContentBlock,
	FauxResponseFactory,
	Model,
	SimpleStreamOptions,
} from "@earendil-works/pi-ai";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const fixtureEnvironmentName = "KIWI_CODE_E2E_PI_FIXTURE";
const reportDirectoryEnvironmentName = "KIWI_CODE_E2E_PI_REPORT_DIR";
const fixtureVersion = 1;
const maximumFixtureBytes = 1 << 20;
const maximumRequestsLimit = 1_000;
const maximumTextLength = 64 << 10;
const reportTextLimit = 500;
const fixtureTokenSize = 8;
const providerID = "kiwi-e2e";
const providerAPI = "kiwi-e2e-fixture";
const titleProviderID = "openai-codex";
const titleModelID = "gpt-5.6-luna";
const titleProviderAPI = "kiwi-e2e-title";

type ThinkingLevel = "off" | "minimal" | "low" | "medium" | "high" | "xhigh" | "max";

type FixtureModel = {
	id: string;
	name: string;
	reasoning: boolean;
	thinkingLevelMap: Partial<Record<ThinkingLevel, string | null>>;
};

type FixtureTextBlock = {
	type: "text";
	text: string;
};

type FixtureThinkingBlock = {
	type: "thinking";
	thinking: string;
};

type FixtureToolCallBlock = {
	type: "toolCall";
	id: string;
	name: string;
	arguments: Record<string, unknown>;
};

type FixtureReplyBlock = FixtureTextBlock | FixtureThinkingBlock | FixtureToolCallBlock;

type FixtureReply = {
	stopReason: "stop" | "toolUse" | "error";
	content: FixtureReplyBlock[];
	errorMessage?: string;
};

type FixtureToolResultExpectation = {
	toolCallId?: string;
	toolName?: string;
	isError?: boolean;
	textIncludes?: string;
};

type FixtureStepExpectation = {
	modelId?: string;
	lastUserText?: string;
	lastUserTextIncludes?: string;
	rolesSuffix?: string[];
	lastToolResult?: FixtureToolResultExpectation;
};

type FixtureStep = {
	id: string;
	when: FixtureStepExpectation;
	reply: FixtureReply;
};

type FixtureTitleExpectation = {
	text?: string;
	textIncludes?: string;
};

type FixtureTitleStep = {
	id: string;
	when: FixtureTitleExpectation;
	text: string;
};

type PiFixture = {
	version: 1;
	tokensPerSecond: number;
	maxRequests: number;
	model: FixtureModel;
	titleSteps: FixtureTitleStep[];
	steps: FixtureStep[];
};

type ContextToolResult = {
	toolCallId: string;
	toolName: string;
	isError: boolean;
	text: string;
};

type ContextView = {
	roles: string[];
	lastUserText: string;
	lastToolResult: ContextToolResult | null;
};

type MatchResult = {
	matches: boolean;
	reasons: string[];
};

type FauxHandle = ReturnType<typeof fauxProvider>;

function fixtureError(path: string, message: string): Error {
	return new Error(`Kiwi E2E Pi fixture ${path}: ${message}`);
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function requireRecord(value: unknown, path: string): Record<string, unknown> {
	if (!isRecord(value)) throw fixtureError(path, "expected an object");
	return value;
}

function requireExactKeys(
	value: Record<string, unknown>,
	path: string,
	allowed: readonly string[],
	required: readonly string[],
): void {
	const allowedKeys = new Set(allowed);
	for (const key of Object.keys(value)) {
		if (!allowedKeys.has(key)) throw fixtureError(`${path}.${key}`, "unknown field");
	}
	for (const key of required) {
		if (!Object.hasOwn(value, key)) throw fixtureError(`${path}.${key}`, "field is required");
	}
}

function requireString(
	value: unknown,
	path: string,
	options: { allowEmpty?: boolean; maximumLength?: number } = {},
): string {
	if (typeof value !== "string") throw fixtureError(path, "expected a string");
	if (!options.allowEmpty && value.length === 0) throw fixtureError(path, "must not be empty");
	if (value.includes("\0")) throw fixtureError(path, "must not contain NUL bytes");
	if (value.length > (options.maximumLength ?? maximumTextLength)) {
		throw fixtureError(path, `must be at most ${options.maximumLength ?? maximumTextLength} characters`);
	}
	return value;
}

function requireBoolean(value: unknown, path: string): boolean {
	if (typeof value !== "boolean") throw fixtureError(path, "expected a boolean");
	return value;
}

function requireInteger(value: unknown, path: string, minimum: number, maximum: number): number {
	if (!Number.isInteger(value) || typeof value !== "number" || value < minimum || value > maximum) {
		throw fixtureError(path, `expected an integer from ${minimum} through ${maximum}`);
	}
	return value;
}

function requireFiniteNumber(value: unknown, path: string, minimum: number, maximum: number): number {
	if (typeof value !== "number" || !Number.isFinite(value) || value < minimum || value > maximum) {
		throw fixtureError(path, `expected a finite number from ${minimum} through ${maximum}`);
	}
	return value;
}

function requireArray(value: unknown, path: string): unknown[] {
	if (!Array.isArray(value)) throw fixtureError(path, "expected an array");
	return value;
}

function optionalString(value: unknown, path: string): string | undefined {
	return value === undefined ? undefined : requireString(value, path);
}

function parseThinkingLevelMap(value: unknown, path: string): FixtureModel["thinkingLevelMap"] {
	const record = requireRecord(value, path);
	const levels: ThinkingLevel[] = ["off", "minimal", "low", "medium", "high", "xhigh", "max"];
	requireExactKeys(record, path, levels, []);
	const parsed: FixtureModel["thinkingLevelMap"] = {};
	for (const level of levels) {
		const mapped = record[level];
		if (mapped === undefined) continue;
		if (mapped !== null && typeof mapped !== "string") {
			throw fixtureError(`${path}.${level}`, "expected a string or null");
		}
		if (typeof mapped === "string" && mapped.length === 0) {
			throw fixtureError(`${path}.${level}`, "must not be empty");
		}
		parsed[level] = mapped;
	}
	return parsed;
}

function parseModel(value: unknown, path: string): FixtureModel {
	const record = requireRecord(value, path);
	requireExactKeys(
		record,
		path,
		["id", "name", "reasoning", "thinkingLevelMap"],
		["id", "name", "reasoning", "thinkingLevelMap"],
	);
	const id = requireString(record.id, `${path}.id`, { maximumLength: 128 });
	if (id.includes("/") || /\s/.test(id) || id.startsWith("-")) {
		throw fixtureError(`${path}.id`, "must be a model ID without slashes, whitespace, or a leading hyphen");
	}
	const reasoning = requireBoolean(record.reasoning, `${path}.reasoning`);
	const thinkingLevelMap = parseThinkingLevelMap(record.thinkingLevelMap, `${path}.thinkingLevelMap`);
	if (!reasoning && Object.values(thinkingLevelMap).some((mapped) => mapped !== null)) {
		throw fixtureError(`${path}.thinkingLevelMap`, "must not expose thinking levels for a non-reasoning model");
	}
	return {
		id,
		name: requireString(record.name, `${path}.name`, { maximumLength: 256 }),
		reasoning,
		thinkingLevelMap,
	};
}

function parseToolResultExpectation(value: unknown, path: string): FixtureToolResultExpectation {
	const record = requireRecord(value, path);
	const keys = ["toolCallId", "toolName", "isError", "textIncludes"] as const;
	requireExactKeys(record, path, keys, []);
	if (Object.keys(record).length === 0) {
		throw fixtureError(path, "must contain at least one tool-result predicate");
	}
	const parsed: FixtureToolResultExpectation = {};
	if (record.toolCallId !== undefined) {
		parsed.toolCallId = requireString(record.toolCallId, `${path}.toolCallId`, { maximumLength: 256 });
	}
	if (record.toolName !== undefined) {
		parsed.toolName = requireString(record.toolName, `${path}.toolName`, { maximumLength: 256 });
	}
	if (record.isError !== undefined) parsed.isError = requireBoolean(record.isError, `${path}.isError`);
	if (record.textIncludes !== undefined) {
		parsed.textIncludes = requireString(record.textIncludes, `${path}.textIncludes`);
	}
	return parsed;
}

function parseStepExpectation(value: unknown, path: string): FixtureStepExpectation {
	const record = requireRecord(value, path);
	const keys = [
		"modelId",
		"lastUserText",
		"lastUserTextIncludes",
		"rolesSuffix",
		"lastToolResult",
	] as const;
	requireExactKeys(record, path, keys, []);
	if (Object.keys(record).length === 0) throw fixtureError(path, "must contain at least one predicate");
	const parsed: FixtureStepExpectation = {};
	if (record.modelId !== undefined) {
		parsed.modelId = requireString(record.modelId, `${path}.modelId`, { maximumLength: 128 });
	}
	if (record.lastUserText !== undefined) {
		parsed.lastUserText = requireString(record.lastUserText, `${path}.lastUserText`);
	}
	if (record.lastUserTextIncludes !== undefined) {
		parsed.lastUserTextIncludes = requireString(record.lastUserTextIncludes, `${path}.lastUserTextIncludes`);
	}
	if (record.rolesSuffix !== undefined) {
		const roles = requireArray(record.rolesSuffix, `${path}.rolesSuffix`);
		if (roles.length === 0) throw fixtureError(`${path}.rolesSuffix`, "must not be empty");
		parsed.rolesSuffix = roles.map((role, index) =>
			requireString(role, `${path}.rolesSuffix[${index}]`, { maximumLength: 64 })
		);
	}
	if (record.lastToolResult !== undefined) {
		parsed.lastToolResult = parseToolResultExpectation(record.lastToolResult, `${path}.lastToolResult`);
	}
	return parsed;
}

function parseTitleExpectation(value: unknown, path: string): FixtureTitleExpectation {
	const record = requireRecord(value, path);
	requireExactKeys(record, path, ["text", "textIncludes"], []);
	if (Object.keys(record).length === 0) throw fixtureError(path, "must contain at least one predicate");
	return {
		text: optionalString(record.text, `${path}.text`),
		textIncludes: optionalString(record.textIncludes, `${path}.textIncludes`),
	};
}

function parseReplyBlock(
	value: unknown,
	path: string,
	toolCallIDs: Set<string>,
): FixtureReplyBlock {
	const record = requireRecord(value, path);
	const type = requireString(record.type, `${path}.type`, { maximumLength: 32 });
	switch (type) {
		case "text":
			requireExactKeys(record, path, ["type", "text"], ["type", "text"]);
			return { type, text: requireString(record.text, `${path}.text`, { allowEmpty: true }) };
		case "thinking":
			requireExactKeys(record, path, ["type", "thinking"], ["type", "thinking"]);
			return { type, thinking: requireString(record.thinking, `${path}.thinking`, { allowEmpty: true }) };
		case "toolCall": {
			requireExactKeys(record, path, ["type", "id", "name", "arguments"], ["type", "id", "name", "arguments"]);
			const id = requireString(record.id, `${path}.id`, { maximumLength: 256 });
			if (toolCallIDs.has(id)) throw fixtureError(`${path}.id`, `duplicate deterministic tool-call ID ${JSON.stringify(id)}`);
			toolCallIDs.add(id);
			return {
				type,
				id,
				name: requireString(record.name, `${path}.name`, { maximumLength: 256 }),
				arguments: requireRecord(record.arguments, `${path}.arguments`),
			};
		}
		default:
			throw fixtureError(`${path}.type`, `expected "text", "thinking", or "toolCall"; received ${JSON.stringify(type)}`);
	}
}

function parseReply(
	value: unknown,
	path: string,
	toolCallIDs: Set<string>,
	model: FixtureModel,
): FixtureReply {
	const record = requireRecord(value, path);
	requireExactKeys(record, path, ["stopReason", "content", "errorMessage"], ["stopReason", "content"]);
	const stopReason = requireString(record.stopReason, `${path}.stopReason`, { maximumLength: 32 });
	if (stopReason !== "stop" && stopReason !== "toolUse" && stopReason !== "error") {
		throw fixtureError(`${path}.stopReason`, 'expected "stop", "toolUse", or "error"');
	}
	const blocks = requireArray(record.content, `${path}.content`).map((block, index) =>
		parseReplyBlock(block, `${path}.content[${index}]`, toolCallIDs)
	);
	const toolCalls = blocks.filter((block) => block.type === "toolCall");
	const thinkingBlocks = blocks.filter((block) => block.type === "thinking");
	if (stopReason === "toolUse" && toolCalls.length === 0) {
		throw fixtureError(path, 'a "toolUse" reply must contain a toolCall block');
	}
	if (stopReason !== "toolUse" && toolCalls.length > 0) {
		throw fixtureError(path, "a reply containing toolCall blocks must use stopReason \"toolUse\"");
	}
	if (!model.reasoning && thinkingBlocks.length > 0) {
		throw fixtureError(path, "a non-reasoning fixture model cannot return thinking blocks");
	}
	const errorMessage = optionalString(record.errorMessage, `${path}.errorMessage`);
	if (stopReason === "error" && !errorMessage) {
		throw fixtureError(`${path}.errorMessage`, 'is required when stopReason is "error"');
	}
	if (stopReason !== "error" && errorMessage !== undefined) {
		throw fixtureError(`${path}.errorMessage`, 'is only valid when stopReason is "error"');
	}
	return { stopReason, content: blocks, errorMessage };
}

function parseFixture(value: unknown): PiFixture {
	const root = requireRecord(value, "$");
	requireExactKeys(
		root,
		"$",
		["version", "tokensPerSecond", "maxRequests", "model", "titleSteps", "steps"],
		["version", "tokensPerSecond", "maxRequests", "model", "titleSteps", "steps"],
	);
	if (root.version !== fixtureVersion) {
		throw fixtureError("$.version", `expected ${fixtureVersion}; received ${JSON.stringify(root.version)}`);
	}
	const model = parseModel(root.model, "$.model");
	const titleIDs = new Set<string>();
	const titleSteps = requireArray(root.titleSteps, "$.titleSteps").map((value, index): FixtureTitleStep => {
		const path = `$.titleSteps[${index}]`;
		const record = requireRecord(value, path);
		requireExactKeys(record, path, ["id", "when", "text"], ["id", "when", "text"]);
		const id = requireString(record.id, `${path}.id`, { maximumLength: 128 });
		if (titleIDs.has(id)) throw fixtureError(`${path}.id`, `duplicate title-step ID ${JSON.stringify(id)}`);
		titleIDs.add(id);
		return {
			id,
			when: parseTitleExpectation(record.when, `${path}.when`),
			text: requireString(record.text, `${path}.text`, { maximumLength: 256 }),
		};
	});
	if (titleSteps.length === 0) throw fixtureError("$.titleSteps", "must contain at least one title response");

	const stepIDs = new Set<string>();
	const toolCallIDs = new Set<string>();
	const steps = requireArray(root.steps, "$.steps").map((value, index): FixtureStep => {
		const path = `$.steps[${index}]`;
		const record = requireRecord(value, path);
		requireExactKeys(record, path, ["id", "when", "reply"], ["id", "when", "reply"]);
		const id = requireString(record.id, `${path}.id`, { maximumLength: 128 });
		if (stepIDs.has(id)) throw fixtureError(`${path}.id`, `duplicate step ID ${JSON.stringify(id)}`);
		stepIDs.add(id);
		return {
			id,
			when: parseStepExpectation(record.when, `${path}.when`),
			reply: parseReply(record.reply, `${path}.reply`, toolCallIDs, model),
		};
	});
	if (steps.length === 0) throw fixtureError("$.steps", "must contain at least one chat response");

	return {
		version: fixtureVersion,
		tokensPerSecond: requireFiniteNumber(root.tokensPerSecond, "$.tokensPerSecond", 0, 100_000),
		maxRequests: requireInteger(root.maxRequests, "$.maxRequests", 1, maximumRequestsLimit),
		model,
		titleSteps,
		steps,
	};
}

function loadFixture(): { fixture: PiFixture; fixturePath: string; reportDirectory: string } {
	const fixturePath = process.env[fixtureEnvironmentName]?.trim() ?? "";
	if (!fixturePath) throw new Error(`${fixtureEnvironmentName} must name an absolute version-1 fixture JSON file`);
	if (!isAbsolute(fixturePath)) throw new Error(`${fixtureEnvironmentName} must be absolute; received ${JSON.stringify(fixturePath)}`);
	const reportDirectory = process.env[reportDirectoryEnvironmentName]?.trim() ?? "";
	if (!reportDirectory) throw new Error(`${reportDirectoryEnvironmentName} must name an absolute report directory`);
	if (!isAbsolute(reportDirectory)) {
		throw new Error(`${reportDirectoryEnvironmentName} must be absolute; received ${JSON.stringify(reportDirectory)}`);
	}

	let contents: string;
	try {
		contents = readFileSync(fixturePath, "utf8");
	} catch (error) {
		throw new Error(`could not read Pi fixture ${fixturePath}: ${error instanceof Error ? error.message : String(error)}`);
	}
	if (Buffer.byteLength(contents) > maximumFixtureBytes) {
		throw new Error(`Pi fixture ${fixturePath} exceeds the ${maximumFixtureBytes}-byte limit`);
	}
	let parsed: unknown;
	try {
		parsed = JSON.parse(contents);
	} catch (error) {
		throw new Error(`could not parse Pi fixture ${fixturePath}: ${error instanceof Error ? error.message : String(error)}`);
	}
	const fixture = parseFixture(parsed);
	try {
		mkdirSync(reportDirectory, { recursive: true, mode: 0o700 });
	} catch (error) {
		throw new Error(`could not create Pi fixture report directory ${reportDirectory}: ${error instanceof Error ? error.message : String(error)}`);
	}
	return { fixture, fixturePath, reportDirectory };
}

function contentText(content: unknown): string {
	if (typeof content === "string") return content;
	if (!Array.isArray(content)) return "";
	const parts: string[] = [];
	for (const value of content) {
		if (typeof value === "string") {
			parts.push(value);
			continue;
		}
		if (!isRecord(value)) continue;
		if (typeof value.text === "string") parts.push(value.text);
		else if (typeof value.thinking === "string") parts.push(value.thinking);
	}
	return parts.join("\n");
}

function contextView(context: Context): ContextView {
	const roles: string[] = [];
	let lastUserText = "";
	let lastToolResult: ContextToolResult | null = null;
	for (const rawMessage of context.messages) {
		const message = rawMessage as unknown;
		if (!isRecord(message)) {
			roles.push("<invalid>");
			continue;
		}
		const role = typeof message.role === "string" ? message.role : "<invalid>";
		roles.push(role);
		if (role === "user") lastUserText = contentText(message.content);
		if (role === "toolResult") {
			lastToolResult = {
				toolCallId: typeof message.toolCallId === "string" ? message.toolCallId : "",
				toolName: typeof message.toolName === "string" ? message.toolName : "",
				isError: message.isError === true,
				text: contentText(message.content),
			};
		}
	}
	return { roles, lastUserText, lastToolResult };
}

function matchStep(expectation: FixtureStepExpectation, view: ContextView, modelID: string): MatchResult {
	const reasons: string[] = [];
	if (expectation.modelId !== undefined && expectation.modelId !== modelID) {
		reasons.push(`modelId expected ${JSON.stringify(expectation.modelId)}, received ${JSON.stringify(modelID)}`);
	}
	if (expectation.lastUserText !== undefined && expectation.lastUserText !== view.lastUserText) {
		reasons.push(
			`lastUserText expected ${JSON.stringify(expectation.lastUserText)}, received ${JSON.stringify(clip(view.lastUserText))}`,
		);
	}
	if (
		expectation.lastUserTextIncludes !== undefined
		&& !view.lastUserText.includes(expectation.lastUserTextIncludes)
	) {
		reasons.push(
			`lastUserText does not include ${JSON.stringify(expectation.lastUserTextIncludes)}; received ${JSON.stringify(clip(view.lastUserText))}`,
		);
	}
	if (expectation.rolesSuffix !== undefined) {
		const suffix = view.roles.slice(-expectation.rolesSuffix.length);
		if (
			suffix.length !== expectation.rolesSuffix.length
			|| suffix.some((role, index) => role !== expectation.rolesSuffix?.[index])
		) {
			reasons.push(
				`roles suffix expected ${JSON.stringify(expectation.rolesSuffix)}, received ${JSON.stringify(suffix)}`,
			);
		}
	}
	if (expectation.lastToolResult !== undefined) {
		const actual = view.lastToolResult;
		if (!actual) {
			reasons.push("expected a tool result, but the context contains none");
		} else {
			const expected = expectation.lastToolResult;
			if (expected.toolCallId !== undefined && expected.toolCallId !== actual.toolCallId) {
				reasons.push(
					`toolCallId expected ${JSON.stringify(expected.toolCallId)}, received ${JSON.stringify(actual.toolCallId)}`,
				);
			}
			if (expected.toolName !== undefined && expected.toolName !== actual.toolName) {
				reasons.push(
					`toolName expected ${JSON.stringify(expected.toolName)}, received ${JSON.stringify(actual.toolName)}`,
				);
			}
			if (expected.isError !== undefined && expected.isError !== actual.isError) {
				reasons.push(`tool isError expected ${expected.isError}, received ${actual.isError}`);
			}
			if (expected.textIncludes !== undefined && !actual.text.includes(expected.textIncludes)) {
				reasons.push(
					`tool text does not include ${JSON.stringify(expected.textIncludes)}; received ${JSON.stringify(clip(actual.text))}`,
				);
			}
		}
	}
	return { matches: reasons.length === 0, reasons };
}

function matchTitleStep(expectation: FixtureTitleExpectation, prompt: string): MatchResult {
	const reasons: string[] = [];
	if (expectation.text !== undefined && expectation.text !== prompt) {
		reasons.push(`text expected ${JSON.stringify(expectation.text)}, received ${JSON.stringify(clip(prompt))}`);
	}
	if (expectation.textIncludes !== undefined && !prompt.includes(expectation.textIncludes)) {
		reasons.push(
			`text does not include ${JSON.stringify(expectation.textIncludes)}; received ${JSON.stringify(clip(prompt))}`,
		);
	}
	return { matches: reasons.length === 0, reasons };
}

function clip(value: string): string {
	if (value.length <= reportTextLimit) return value;
	return `${value.slice(0, reportTextLimit)}…`;
}

function safeReportSegment(value: string, fallback: string): string {
	const safe = value.replace(/[^A-Za-z0-9._-]/g, "_").slice(0, 128);
	return safe && safe !== "." && safe !== ".." ? safe : fallback;
}

function reportPath(reportDirectory: string): string {
	const projectID = safeReportSegment(process.env.KIWI_CODE_PROJECT_ID ?? "", "unknown-project");
	const threadID = safeReportSegment(process.env.KIWI_CODE_THREAD_ID ?? "", "unknown-thread");
	return join(reportDirectory, `${projectID}-${threadID}.jsonl`);
}

function appendReport(
	reportDirectory: string,
	entry: Record<string, unknown>,
): void {
	const record = {
		at: new Date().toISOString(),
		pid: process.pid,
		projectId: process.env.KIWI_CODE_PROJECT_ID ?? "",
		threadId: process.env.KIWI_CODE_THREAD_ID ?? "",
		...entry,
	};
	try {
		appendFileSync(reportPath(reportDirectory), `${JSON.stringify(record)}\n`, {
			encoding: "utf8",
			flag: "a",
			mode: 0o600,
		});
	} catch (error) {
		throw new Error(
			`could not append Pi fixture request report: ${error instanceof Error ? error.message : String(error)}`,
		);
	}
}

function reportContext(view: ContextView): Record<string, unknown> {
	return {
		roles: view.roles,
		lastUserText: clip(view.lastUserText),
		lastToolResult: view.lastToolResult
			? {
				toolCallId: view.lastToolResult.toolCallId,
				toolName: view.lastToolResult.toolName,
				isError: view.lastToolResult.isError,
				text: clip(view.lastToolResult.text),
			}
			: null,
	};
}

function explainNoChatMatch(
	fixturePath: string,
	results: Array<{ step: FixtureStep; result: MatchResult }>,
	view: ContextView,
	modelID: string,
): string {
	const details = results
		.map(({ step, result }) => `${step.id}: ${result.reasons.join("; ") || "matched"}`)
		.join(" | ");
	return `no chat step matched model ${JSON.stringify(modelID)} and context ${JSON.stringify(reportContext(view))} in ${fixturePath}. Predicates: ${details}`;
}

function explainNoTitleMatch(
	fixturePath: string,
	results: Array<{ step: FixtureTitleStep; result: MatchResult }>,
	prompt: string,
): string {
	const details = results
		.map(({ step, result }) => `${step.id}: ${result.reasons.join("; ") || "matched"}`)
		.join(" | ");
	return `no title step matched prompt ${JSON.stringify(clip(prompt))} in ${fixturePath}. Predicates: ${details}`;
}

function replyContent(reply: FixtureReply): FauxContentBlock[] {
	return reply.content.map((block) => {
		switch (block.type) {
			case "text":
				return fauxText(block.text);
			case "thinking":
				return fauxThinking(block.thinking);
			case "toolCall":
				return fauxToolCall(block.name, block.arguments, { id: block.id });
		}
	});
}

function chatResponseFactory(
	fixture: PiFixture,
	fixturePath: string,
	reportDirectory: string,
	requestIndex: number,
): FauxResponseFactory {
	return (context, _options, _state, model) => {
		const view = contextView(context);
		const results = fixture.steps.map((step) => ({
			step,
			result: matchStep(step.when, view, model.id),
		}));
		const matches = results.filter(({ result }) => result.matches);
		if (matches.length !== 1) {
			const message = matches.length === 0
				? explainNoChatMatch(fixturePath, results, view, model.id)
				: `ambiguous chat fixture in ${fixturePath}: steps ${matches.map(({ step }) => step.id).join(", ")} all matched context ${JSON.stringify(reportContext(view))}`;
			appendReport(reportDirectory, {
				channel: "chat",
				requestIndex,
				modelId: model.id,
				matched: false,
				error: message,
				context: reportContext(view),
			});
			throw new Error(message);
		}
		const step = matches[0].step;
		appendReport(reportDirectory, {
			channel: "chat",
			requestIndex,
			modelId: model.id,
			matched: true,
			stepId: step.id,
			context: reportContext(view),
		});
		return fauxAssistantMessage(replyContent(step.reply), {
			stopReason: step.reply.stopReason,
			errorMessage: step.reply.errorMessage,
		});
	};
}

function titleResponseFactory(
	fixture: PiFixture,
	fixturePath: string,
	reportDirectory: string,
	requestIndex: number,
): FauxResponseFactory {
	return (context, _options, _state, model) => {
		const prompt = contextView(context).lastUserText;
		const results = fixture.titleSteps.map((step) => ({
			step,
			result: matchTitleStep(step.when, prompt),
		}));
		const matches = results.filter(({ result }) => result.matches);
		if (matches.length !== 1) {
			const message = matches.length === 0
				? explainNoTitleMatch(fixturePath, results, prompt)
				: `ambiguous title fixture in ${fixturePath}: steps ${matches.map(({ step }) => step.id).join(", ")} all matched prompt ${JSON.stringify(clip(prompt))}`;
			appendReport(reportDirectory, {
				channel: "title",
				requestIndex,
				modelId: model.id,
				matched: false,
				error: message,
				prompt: clip(prompt),
			});
			throw new Error(message);
		}
		const step = matches[0].step;
		appendReport(reportDirectory, {
			channel: "title",
			requestIndex,
			modelId: model.id,
			matched: true,
			stepId: step.id,
			prompt: clip(prompt),
		});
		return fauxAssistantMessage(fauxText(step.text));
	};
}

function exceededRequestFactory(
	fixture: PiFixture,
	fixturePath: string,
	reportDirectory: string,
	channel: "chat" | "title",
	requestIndex: number,
): FauxResponseFactory {
	return (context, _options, _state, model) => {
		const view = contextView(context);
		const message = `Pi fixture ${fixturePath} exceeded maxRequests=${fixture.maxRequests}; request ${requestIndex} was for ${channel} model ${model.id}. Add an expected step only if the extra request is intentional.`;
		appendReport(reportDirectory, {
			channel,
			requestIndex,
			modelId: model.id,
			matched: false,
			error: message,
			context: reportContext(view),
		});
		throw new Error(message);
	};
}

function configureStream(
	handle: FauxHandle,
	factory: FauxResponseFactory,
	model: Model<string>,
	context: Context,
	options?: SimpleStreamOptions,
) {
	handle.setResponses([factory]);
	return handle.provider.streamSimple(model, context, options);
}

export default function registerPiFixtureProvider(pi: ExtensionAPI): void {
	const { fixture, fixturePath, reportDirectory } = loadFixture();
	const chat = fauxProvider({
		api: providerAPI,
		provider: providerID,
		models: [{
			id: fixture.model.id,
			name: fixture.model.name,
			reasoning: fixture.model.reasoning,
			input: ["text", "image"],
			contextWindow: 128_000,
			maxTokens: 16_384,
			cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
		}],
		tokensPerSecond: fixture.tokensPerSecond,
		tokenSize: { min: fixtureTokenSize, max: fixtureTokenSize },
	});
	const title = fauxProvider({
		api: titleProviderAPI,
		provider: titleProviderID,
		models: [{
			id: titleModelID,
			name: "Kiwi E2E Fixture Title",
			reasoning: true,
			input: ["text"],
			contextWindow: 128_000,
			maxTokens: 256,
			cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
		}],
		tokensPerSecond: fixture.tokensPerSecond,
		tokenSize: { min: fixtureTokenSize, max: fixtureTokenSize },
	});
	let requestCount = 0;
	const nextFactory = (
		channel: "chat" | "title",
	): FauxResponseFactory => {
		requestCount += 1;
		if (requestCount > fixture.maxRequests) {
			return exceededRequestFactory(
				fixture,
				fixturePath,
				reportDirectory,
				channel,
				requestCount,
			);
		}
		return channel === "chat"
			? chatResponseFactory(fixture, fixturePath, reportDirectory, requestCount)
			: titleResponseFactory(fixture, fixturePath, reportDirectory, requestCount);
	};

	pi.registerProvider(providerID, {
		name: "Kiwi E2E Fixture",
		baseUrl: "http://127.0.0.1:0",
		apiKey: "fixture",
		api: providerAPI,
		streamSimple: (model, context, options) =>
			configureStream(chat, nextFactory("chat"), model, context, options),
		models: [{
			id: fixture.model.id,
			name: fixture.model.name,
			reasoning: fixture.model.reasoning,
			thinkingLevelMap: fixture.model.thinkingLevelMap,
			input: ["text", "image"],
			cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
			contextWindow: 128_000,
			maxTokens: 16_384,
		}],
	});

	// The bundled thread-title extension otherwise makes an independent request
	// to openai-codex/gpt-5.6-luna on the first user turn. Replacing that exact
	// provider/model keeps real-Pi E2E sessions fully offline.
	pi.registerProvider(titleProviderID, {
		name: "Kiwi E2E Fixture Title",
		baseUrl: "http://127.0.0.1:0",
		apiKey: "fixture",
		api: titleProviderAPI,
		streamSimple: (model, context, options) =>
			configureStream(title, nextFactory("title"), model, context, options),
		models: [{
			id: titleModelID,
			name: "Kiwi E2E Fixture Title",
			reasoning: true,
			thinkingLevelMap: { low: "low" },
			input: ["text"],
			cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
			contextWindow: 128_000,
			maxTokens: 256,
		}],
	});
}
