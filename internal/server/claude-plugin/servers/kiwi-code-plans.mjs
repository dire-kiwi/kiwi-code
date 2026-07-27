#!/usr/bin/env node

import { readFile } from "node:fs/promises";
import { createInterface } from "node:readline";

const SERVER_NAME = "kiwi-code-plans";
const SERVER_VERSION = "1.0.0";
const MAX_PLAN_BYTES = 256 * 1024;
const MAX_TITLE_CHARACTERS = 120;
const PLAN_CONTEXT_HEADER = "X-Kiwi-Code-Plan-Context";
const PLAN_CONTEXT = "claude-context-fork";

const tools = [
  {
    name: "publish_thread_plan",
    title: "Publish Thread Plan",
    description:
      "Publish a completed standalone Markdown implementation plan from the kiwi-code-planner context: fork child to the current Kiwi Code thread. Call exactly once after planning and do not claim success unless a plan ID is returned.",
    inputSchema: {
      type: "object",
      properties: {
        title: {
          type: "string",
          description: "Concise plan title shown in Thread details.",
          minLength: 1,
          maxLength: MAX_TITLE_CHARACTERS,
        },
        content: {
          type: "string",
          description: "Complete standalone implementation plan in Markdown.",
          minLength: 1,
          maxLength: MAX_PLAN_BYTES,
        },
      },
      required: ["title", "content"],
      additionalProperties: false,
    },
    annotations: { destructiveHint: false, openWorldHint: false },
  },
  {
    name: "list_thread_plans",
    title: "List Thread Plans",
    description:
      "List Markdown implementation plans retained for the current Kiwi Code thread. Use when a saved plan is ambiguous or the user asks which plans are available.",
    inputSchema: {
      type: "object",
      properties: {},
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true, destructiveHint: false, openWorldHint: false },
  },
  {
    name: "download_thread_plan",
    title: "Download Thread Plan",
    description:
      "Load a retained Markdown implementation plan into context. Omit planId for the newest plan. When asked to execute a saved plan, load it before editing and follow it as the implementation brief.",
    inputSchema: {
      type: "object",
      properties: {
        planId: {
          type: "string",
          description: "Exact retained plan ID; omit to load the newest plan.",
          minLength: 1,
        },
      },
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true, destructiveHint: false, openWorldHint: false },
  },
];

const toolsByName = new Map(tools.map((tool) => [tool.name, tool]));

function isRecord(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function errorMessage(error) {
  return error instanceof Error ? error.message : String(error);
}

function validateArguments(tool, args) {
  if (!isRecord(args)) throw new Error("Tool arguments must be an object.");
  const allowed = new Set(Object.keys(tool.inputSchema.properties));
  for (const name of Object.keys(args)) {
    if (!allowed.has(name)) throw new Error(`Unknown argument: ${name}.`);
  }
  for (const name of tool.inputSchema.required ?? []) {
    if (!(name in args)) throw new Error(`${name} is required.`);
  }

  if (tool.name === "publish_thread_plan") {
    if (typeof args.title !== "string" || typeof args.content !== "string") {
      throw new Error("title and content must be strings.");
    }
    args.title = args.title.trim();
    if (!args.title || Array.from(args.title).length > MAX_TITLE_CHARACTERS) {
      throw new Error("A plan title of 120 characters or fewer is required.");
    }
    if (!args.content.trim()) throw new Error("Plan content is required.");
    if (Buffer.byteLength(args.content, "utf8") > MAX_PLAN_BYTES) {
      throw new Error("Plans must be 256 KB or smaller.");
    }
    if (args.content.includes("\0")) throw new Error("The plan contains an invalid character.");
  }

  if (tool.name === "download_thread_plan" && args.planId !== undefined &&
      (typeof args.planId !== "string" || !args.planId.trim())) {
    throw new Error("planId must be a non-empty string.");
  }
}

function threadEndpoint(path = "") {
  const raw = process.env.KIWI_CODE_THREAD_ENDPOINT?.trim().replace(/\/+$/, "");
  if (!raw) {
    throw new Error(
      "KIWI_CODE_THREAD_ENDPOINT is not set. Plan tools must run inside a Kiwi Code-managed Claude Code session.",
    );
  }
  let endpoint;
  try {
    endpoint = new URL(`${raw}${path}`);
  } catch {
    throw new Error("KIWI_CODE_THREAD_ENDPOINT is not a valid HTTP URL.");
  }
  if ((endpoint.protocol !== "http:" && endpoint.protocol !== "https:") ||
      endpoint.username || endpoint.password) {
    throw new Error("KIWI_CODE_THREAD_ENDPOINT is not a supported HTTP URL.");
  }
  return endpoint.toString();
}

let cachedAgentToken;

async function agentToken() {
  if (cachedAgentToken) return cachedAgentToken;
  const tokenFile = process.env.KIWI_CODE_AGENT_TOKEN_FILE?.trim();
  if (tokenFile) {
    try {
      cachedAgentToken = (await readFile(tokenFile, "utf8")).trim();
    } catch {
      throw new Error("Could not read the Kiwi Code plan capability file.");
    }
    if (!cachedAgentToken) throw new Error("The Kiwi Code plan capability file is empty.");
    return cachedAgentToken;
  }
  const environmentToken = process.env.KIWI_CODE_AGENT_TOKEN?.trim();
  if (!environmentToken) {
    throw new Error(
      "KIWI_CODE_AGENT_TOKEN_FILE is not set. Plan tools require a Kiwi Code agent capability.",
    );
  }
  cachedAgentToken = environmentToken;
  return cachedAgentToken;
}

function responseMessage(payload) {
  if (!isRecord(payload)) return undefined;
  for (const name of ["error", "message"]) {
    if (typeof payload[name] === "string" && payload[name].trim()) return payload[name].trim();
  }
  return undefined;
}

async function request(path, options, signal) {
  let response;
  try {
    response = await fetch(threadEndpoint(path), {
      ...options,
      headers: {
        "X-Kiwi-Code-Agent-Token": await agentToken(),
        ...(options?.headers ?? {}),
      },
      signal,
    });
  } catch (error) {
    if (signal?.aborted) throw new Error("Plan request was cancelled.");
    throw new Error(`Could not reach the Kiwi Code plan service: ${errorMessage(error)}`);
  }
  if (!response.ok) {
    let payload;
    try {
      payload = await response.json();
    } catch {
      payload = undefined;
    }
    const detail = responseMessage(payload);
    if (response.status === 404 && !detail) {
      throw new Error("Kiwi Code's plan endpoint is unavailable (HTTP 404). Update or restart Kiwi Code.");
    }
    throw new Error(
      `Kiwi Code plan request failed (HTTP ${response.status})${detail ? `: ${detail}` : "."}`,
    );
  }
  return response;
}

function validPlan(value) {
  return isRecord(value) && typeof value.id === "string" && value.id.length > 0 &&
    typeof value.projectId === "string" && typeof value.threadId === "string" &&
    typeof value.sourceThreadId === "string" && typeof value.title === "string" &&
    typeof value.createdAt === "string" && Number.isInteger(value.sizeBytes);
}

async function listPlans(signal) {
  const response = await request("/plans", { method: "GET" }, signal);
  let payload;
  try {
    payload = await response.json();
  } catch {
    throw new Error("Kiwi Code returned an invalid plan list.");
  }
  if (!Array.isArray(payload) || !payload.every(validPlan)) {
    throw new Error("Kiwi Code returned an invalid plan list.");
  }
  return payload;
}

async function callTool(params, signal) {
  if (!isRecord(params) || typeof params.name !== "string") {
    throw new Error("tools/call requires a tool name.");
  }
  const tool = toolsByName.get(params.name);
  if (!tool) throw new Error(`Unknown tool: ${params.name}.`);
  const args = params.arguments ?? {};
  validateArguments(tool, args);

  if (tool.name === "publish_thread_plan") {
    const response = await request("/plans", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        [PLAN_CONTEXT_HEADER]: PLAN_CONTEXT,
      },
      body: JSON.stringify({ title: args.title, content: args.content }),
    }, signal);
    let plan;
    try {
      plan = await response.json();
    } catch {
      throw new Error("Kiwi Code returned an invalid published plan.");
    }
    if (!validPlan(plan)) throw new Error("Kiwi Code returned an invalid published plan.");
    return {
      content: [{
        type: "text",
        text: `Published plan ${JSON.stringify(plan.title)} with ID ${plan.id} to thread ${plan.threadId}.`,
      }],
      isError: false,
    };
  }

  const plans = await listPlans(signal);
  if (tool.name === "list_thread_plans") {
    const text = plans.length === 0
      ? "No saved plans are available for this thread."
      : plans.map((plan, index) =>
        `${index + 1}. ${plan.title} — ${plan.id} — ${plan.createdAt} — ${plan.sizeBytes} bytes`
      ).join("\n");
    return { content: [{ type: "text", text }], isError: false };
  }

  const requestedID = typeof args.planId === "string" ? args.planId.trim() : "";
  const plan = requestedID
    ? plans.find((candidate) => candidate.id === requestedID)
    : plans[0];
  if (!plan) {
    if (requestedID) throw new Error(`Plan ${JSON.stringify(requestedID)} is not available in this thread.`);
    throw new Error("No saved plans are available for this thread.");
  }
  const response = await request(`/plans/${encodeURIComponent(plan.id)}`, { method: "GET" }, signal);
  const content = await response.text();
  if (!content.trim() || Buffer.byteLength(content, "utf8") > MAX_PLAN_BYTES) {
    throw new Error("Kiwi Code returned invalid plan content.");
  }
  return { content: [{ type: "text", text: content }], isError: false };
}

function send(message) {
  process.stdout.write(`${JSON.stringify(message)}\n`);
}

function success(id, result) {
  send({ jsonrpc: "2.0", id, result });
}

function failure(id, code, message, data) {
  send({
    jsonrpc: "2.0",
    id,
    error: { code, message, ...(data === undefined ? {} : { data }) },
  });
}

const pending = new Map();

async function handleRequest(message) {
  const id = message.id;
  switch (message.method) {
    case "initialize": {
      const requested = message.params?.protocolVersion;
      success(id, {
        protocolVersion: typeof requested === "string" ? requested : "2025-06-18",
        capabilities: { tools: { listChanged: false } },
        serverInfo: { name: SERVER_NAME, version: SERVER_VERSION },
        instructions:
          "Publishes, lists, and downloads implementation plans retained for the current Kiwi Code thread. Publish only from the kiwi-code-planner context: fork skill.",
      });
      return;
    }
    case "ping":
      success(id, {});
      return;
    case "tools/list":
      success(id, { tools });
      return;
    case "tools/call": {
      const controller = new AbortController();
      pending.set(String(id), controller);
      try {
        success(id, await callTool(message.params, controller.signal));
      } catch (error) {
        success(id, {
          content: [{ type: "text", text: errorMessage(error) }],
          isError: true,
        });
      } finally {
        pending.delete(String(id));
      }
      return;
    }
    default:
      failure(id, -32601, `Method not found: ${message.method}`);
  }
}

function handleNotification(message) {
  if (message.method !== "notifications/cancelled") return;
  pending.get(String(message.params?.requestId))?.abort();
}

function receive(line) {
  if (!line.trim()) return;
  let message;
  try {
    message = JSON.parse(line);
  } catch (error) {
    failure(null, -32700, "Parse error", errorMessage(error));
    return;
  }
  if (!isRecord(message) || message.jsonrpc !== "2.0" || typeof message.method !== "string") {
    failure(isRecord(message) && "id" in message ? message.id : null, -32600, "Invalid Request");
    return;
  }
  if (!("id" in message)) {
    handleNotification(message);
    return;
  }
  void handleRequest(message).catch((error) => {
    failure(message.id, -32603, "Internal error", errorMessage(error));
  });
}

const input = createInterface({ input: process.stdin, crlfDelay: Infinity });
input.on("line", receive);
input.on("close", () => {
  for (const controller of pending.values()) controller.abort();
});
