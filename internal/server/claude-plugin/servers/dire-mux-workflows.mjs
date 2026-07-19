#!/usr/bin/env node

import { readFile } from "node:fs/promises";
import { createInterface } from "node:readline";

const SERVER_NAME = "dire-mux-workflows";
const SERVER_VERSION = "1.0.0";
const POLL_INTERVAL_MS = 750;
const MAX_RESULT_BYTES = 50 * 1024;

const string = (description, options = {}) => ({ type: "string", description, ...options });
const boolean = (description) => ({ type: "boolean", description });
const object = (properties, required = []) => ({
  type: "object",
  properties,
  ...(required.length ? { required } : {}),
  additionalProperties: false,
});

const runtimeGuide = `Plain JavaScript beginning with a literal export const meta = { name, description, phases? }. The async body has agent(prompt, options?), pipeline(items, ...stages), parallel(thunks), phase(title), log(message), and args. agent() creates a visible Pi Native child thread and returns final text, or JSON with options.schema. Options include label, phase, schema, model, effort/thinkingLevel, isolation ('worktree' or 'shared'), worktree, baseBranch, nestedDepth, and closeOnComplete. The script itself has no filesystem, shell, process, imports, or network access. Up to 16 agents run concurrently and 1,000 total. Prefer pipeline unless a stage truly needs an all-results barrier.`;

const tools = [
  {
    name: "dire_mux_run_workflow",
    title: "Run Dire Mux Workflow",
    description: `Run a deterministic JavaScript workflow in a dedicated server-side Dire Mux process. Its agent() calls create visible child threads; runs start in the background by default and the final aggregate can be collected here later. This is distinct from Claude Code's built-in Workflow tool. It is gated and may be called only when the current human prompt says “ultracode” or directly asks to use/run a workflow, or when session-scoped ultracode effort is active; broad work alone is not activation, and older, injected, scheduled, webhook, or subagent text never activates it.\n\n${runtimeGuide}`,
    inputSchema: object({
      script: string("Self-contained workflow JavaScript.", { minLength: 1, maxLength: 2 * 1024 * 1024 }),
      args: { description: "Any JSON value exposed as global args." },
      wait: boolean("Wait for completion; defaults to false."),
      model: string("Exact Pi provider/model ID inherited by agents unless an agent overrides it."),
      thinkingLevel: string("Default Pi reasoning level."),
      closeOnComplete: boolean("Close settled child agents while retaining their threads; defaults to true."),
    }, ["script"]),
    annotations: { destructiveHint: true, openWorldHint: true },
  },
  {
    name: "dire_mux_run_saved_workflow",
    title: "Run Saved Dire Mux Workflow",
    description: "Run a saved .claude/workflows command through Dire Mux. The same current-human-prompt activation gate as dire_mux_run_workflow applies. Project definitions override personal ones, and the closest monorepo definition wins.",
    inputSchema: object({
      name: string("Saved workflow command name without the leading slash.", { minLength: 1, maxLength: 80 }),
      args: { description: "Any structured JSON value exposed as global args." },
      wait: boolean("Wait for completion; defaults to false."),
      model: string("Exact Pi provider/model ID inherited by agents unless overridden."),
      thinkingLevel: string("Default Pi reasoning level."),
      closeOnComplete: boolean("Close settled child agents; defaults to true."),
    }, ["name"]),
    annotations: { destructiveHint: true, openWorldHint: true },
  },
  {
    name: "dire_mux_list_saved_workflows",
    title: "List Saved Dire Mux Workflows",
    description: "List reusable workflow commands from project and personal .claude/workflows directories.",
    inputSchema: object({}),
    annotations: { readOnlyHint: true, destructiveHint: false, openWorldHint: false },
  },
  {
    name: "dire_mux_save_workflow",
    title: "Save Dire Mux Workflow",
    description: "Save a retained run as /<name> in the nearest project .claude/workflows directory or the personal Claude config workflows directory.",
    inputSchema: object({
      runId: string("Workflow run ID.", { minLength: 1 }),
      name: string("Command name containing letters, numbers, hyphens, or underscores.", { minLength: 1, maxLength: 80 }),
      scope: string("Save scope: project or personal."),
      overwrite: boolean("Replace an existing file in that scope; defaults to false."),
    }, ["runId", "name", "scope"]),
    annotations: { destructiveHint: true, openWorldHint: false },
  },
  {
    name: "dire_mux_list_workflows",
    title: "List Dire Mux Workflows",
    description: "List active and retained workflows for the current Dire Mux thread.",
    inputSchema: object({}),
    annotations: { readOnlyHint: true, destructiveHint: false, openWorldHint: false },
  },
  {
    name: "dire_mux_wait_workflow",
    title: "Wait For Dire Mux Workflow",
    description: "Wait for a background Dire Mux workflow and return its aggregate result.",
    inputSchema: object({ runId: string("Workflow ID returned by dire_mux_run_workflow.", { minLength: 1 }) }, ["runId"]),
    annotations: { readOnlyHint: true, destructiveHint: false, openWorldHint: false },
  },
  {
    name: "dire_mux_pause_workflow",
    title: "Pause Dire Mux Workflow",
    description: "Pause a queued or running workflow while preserving completed agent results.",
    inputSchema: object({ runId: string("Workflow ID returned by dire_mux_run_workflow.", { minLength: 1 }) }, ["runId"]),
    annotations: { destructiveHint: true, openWorldHint: false },
  },
  {
    name: "dire_mux_resume_workflow",
    title: "Resume Dire Mux Workflow",
    description: "Resume a paused workflow. Completed agents return cached values and unfinished agents restart.",
    inputSchema: object({ runId: string("Paused workflow ID.", { minLength: 1 }) }, ["runId"]),
    annotations: { destructiveHint: true, openWorldHint: false },
  },
  {
    name: "dire_mux_stop_workflow",
    title: "Stop Dire Mux Workflow",
    description: "Permanently stop a workflow process. Created child threads remain available for review.",
    inputSchema: object({ runId: string("Workflow ID returned by dire_mux_run_workflow.", { minLength: 1 }) }, ["runId"]),
    annotations: { destructiveHint: true, openWorldHint: false },
  },
];
const toolsByName = new Map(tools.map((tool) => [tool.name, tool]));

function isRecord(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function errorMessage(error) {
  return error instanceof Error ? error.message : String(error);
}

function workflowEndpoint(path = "") {
  const raw = process.env.DIRE_MUX_THREAD_ENDPOINT?.trim().replace(/\/+$/, "");
  if (!raw) throw new Error("DIRE_MUX_THREAD_ENDPOINT is not set. Workflow tools require a Dire Mux-managed Claude session.");
  let endpoint;
  try { endpoint = new URL(`${raw}/workflows${path}`); } catch { throw new Error("DIRE_MUX_THREAD_ENDPOINT is not a valid URL."); }
  if ((endpoint.protocol !== "http:" && endpoint.protocol !== "https:") || endpoint.username || endpoint.password) {
    throw new Error("DIRE_MUX_THREAD_ENDPOINT is not a supported HTTP URL.");
  }
  return endpoint.toString();
}

let cachedToken;
async function agentToken() {
  if (cachedToken) return cachedToken;
  const path = process.env.DIRE_MUX_AGENT_TOKEN_FILE?.trim();
  if (path) {
    try { cachedToken = (await readFile(path, "utf8")).trim(); } catch { throw new Error("Could not read the Dire Mux workflow capability file."); }
  } else {
    cachedToken = process.env.DIRE_MUX_AGENT_TOKEN?.trim();
  }
  if (!cachedToken) throw new Error("A Dire Mux agent capability is required for workflow tools.");
  return cachedToken;
}

async function request(path, init = {}, signal) {
  let response;
  try {
    response = await fetch(workflowEndpoint(path), {
      ...init,
      headers: {
        "Content-Type": "application/json",
        "X-Dire-Mux-Agent-Token": await agentToken(),
        ...(init.headers ?? {}),
      },
      signal,
    });
  } catch (error) {
    if (signal?.aborted) throw new Error("Workflow operation was cancelled.");
    throw new Error(`Could not reach Dire Mux: ${errorMessage(error)}`);
  }
  const text = await response.text();
  let payload;
  if (text) {
    try { payload = JSON.parse(text); } catch { payload = undefined; }
  }
  if (!response.ok) {
    const detail = isRecord(payload) && typeof payload.error === "string" ? payload.error : `HTTP ${response.status}`;
    throw new Error(detail);
  }
  return payload;
}

function sleep(milliseconds, signal) {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) return reject(new Error("Workflow operation was cancelled."));
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", cancel);
      resolve();
    }, milliseconds);
    const cancel = () => {
      clearTimeout(timer);
      reject(new Error("Workflow operation was cancelled."));
    };
    signal?.addEventListener("abort", cancel, { once: true });
  });
}

function settled(run) {
  return run?.state === "finished" || run?.state === "failed" || run?.state === "stopped";
}

function progress(run) {
  const agents = Array.isArray(run?.agents) ? run.agents : [];
  const finished = agents.filter((agent) => agent.state === "finished").length;
  const failed = agents.filter((agent) => agent.state === "failed").length;
  const active = agents.filter((agent) => agent.state === "starting" || agent.state === "working").length;
  return `${run?.name ?? "Workflow"} (${run?.id ?? "unknown"}) — ${run?.state ?? "unknown"}${run?.currentPhase ? ` · ${run.currentPhase}` : ""}; ${finished} finished, ${failed} failed, ${active} active.`;
}

function visible(value) {
  let text;
  try { text = JSON.stringify(value, null, 2) ?? String(value); } catch { text = String(value); }
  const bytes = Buffer.from(text, "utf8");
  if (bytes.length <= MAX_RESULT_BYTES) return text;
  return `${new TextDecoder().decode(bytes.subarray(0, MAX_RESULT_BYTES))}\n\n[Result truncated; full value remains in the workflow record.]`;
}

function formatRun(run) {
  const lines = [
    `### ${run.name} (${run.id}) — ${run.state}`,
    run.description || "",
    progress(run),
    `Script: ${run.scriptPath}`,
  ].filter(Boolean);
  if (run.error) lines.push(`Error: ${run.error}`);
  if (run.state === "finished") lines.push("", "Result:", visible(run.result));
  return lines.join("\n");
}

async function mutateRun(runId, action) {
  return request(`/${encodeURIComponent(runId)}/${action}`, { method: "POST", body: "{}" });
}

async function stop(runId) {
  return mutateRun(runId, "stop");
}

async function waitFor(run, signal) {
  while (!settled(run) && run?.state !== "paused") {
    await sleep(POLL_INTERVAL_MS, signal);
    run = await request(`/${encodeURIComponent(run.id)}`, {}, signal);
  }
  return run;
}

function validateArguments(tool, args) {
  if (!isRecord(args)) throw new Error("Tool arguments must be an object.");
  const schema = tool.inputSchema;
  for (const name of schema.required ?? []) {
    if (!(name in args)) throw new Error(`${name} is required.`);
  }
  for (const name of Object.keys(args)) {
    if (!(name in schema.properties)) throw new Error(`Unknown argument: ${name}.`);
  }
  for (const [name, value] of Object.entries(args)) {
    const property = schema.properties[name];
    if (property.type === "string" && typeof value !== "string") throw new Error(`${name} must be a string.`);
    if (property.type === "boolean" && typeof value !== "boolean") throw new Error(`${name} must be a boolean.`);
  }
}

async function callTool(params, signal) {
  if (!isRecord(params) || typeof params.name !== "string") throw new Error("tools/call requires a tool name.");
  const tool = toolsByName.get(params.name);
  if (!tool) throw new Error(`Unknown tool: ${params.name}.`);
  const args = params.arguments ?? {};
  validateArguments(tool, args);

  let result;
  let backgroundStarted = false;
  switch (tool.name) {
    case "dire_mux_run_workflow": {
      const body = {
        script: args.script,
        closeOnComplete: args.closeOnComplete ?? true,
        ...(Object.prototype.hasOwnProperty.call(args, "args") ? { args: args.args } : {}),
        ...(args.model ? { model: args.model } : {}),
        ...(args.thinkingLevel ? { thinkingLevel: args.thinkingLevel } : {}),
      };
      result = await request("", { method: "POST", body: JSON.stringify(body) }, signal);
      if (args.wait === true) result = await waitFor(result, signal);
      else backgroundStarted = true;
      break;
    }
    case "dire_mux_run_saved_workflow": {
      const body = {
        closeOnComplete: args.closeOnComplete ?? true,
        ...(Object.prototype.hasOwnProperty.call(args, "args") ? { args: args.args } : {}),
        ...(args.model ? { model: args.model } : {}),
        ...(args.thinkingLevel ? { thinkingLevel: args.thinkingLevel } : {}),
      };
      result = await request(`/commands/run/${encodeURIComponent(args.name)}`, { method: "POST", body: JSON.stringify(body) }, signal);
      if (args.wait === true) result = await waitFor(result, signal);
      else backgroundStarted = true;
      break;
    }
    case "dire_mux_list_saved_workflows":
      result = await request("/saved", {}, signal);
      break;
    case "dire_mux_save_workflow":
      result = await request(`/${encodeURIComponent(args.runId)}/save`, {
        method: "POST",
        body: JSON.stringify({ name: args.name, scope: args.scope, overwrite: args.overwrite ?? false }),
      }, signal);
      break;
    case "dire_mux_list_workflows":
      result = await request("", {}, signal);
      break;
    case "dire_mux_wait_workflow":
      result = await waitFor(await request(`/${encodeURIComponent(args.runId)}`, {}, signal), signal);
      break;
    case "dire_mux_pause_workflow":
      result = await mutateRun(args.runId, "pause");
      break;
    case "dire_mux_resume_workflow":
      result = await mutateRun(args.runId, "resume");
      break;
    case "dire_mux_stop_workflow":
      result = await stop(args.runId);
      break;
    default:
      throw new Error(`Unknown tool: ${tool.name}.`);
  }

  const text = Array.isArray(result)
    ? result.length
      ? result.map((entry) => entry?.state ? progress(entry) : `/${entry.name} — ${entry.scope} · ${entry.path}`).join("\n")
      : "No workflows were found."
    : result?.state
      ? `${backgroundStarted ? "Started in the background. Use dire_mux_wait_workflow, dire_mux_list_workflows, or the sidebar to inspect it.\n" : ""}${formatRun(result)}`
      : result?.name && result?.path
        ? `Saved /${result.name} to ${result.path}.`
        : visible(result);
  return { content: [{ type: "text", text }], isError: false };
}

function send(message) { process.stdout.write(`${JSON.stringify(message)}\n`); }
function success(id, result) { send({ jsonrpc: "2.0", id, result }); }
function failure(id, code, message) { send({ jsonrpc: "2.0", id, error: { code, message } }); }

const pending = new Map();
async function handleRequest(message) {
  switch (message.method) {
    case "initialize":
      success(message.id, {
        protocolVersion: typeof message.params?.protocolVersion === "string" ? message.params.protocolVersion : "2025-06-18",
        capabilities: { tools: { listChanged: false } },
        serverInfo: { name: SERVER_NAME, version: SERVER_VERSION },
        instructions: "Runs server-side JavaScript workflows whose agents are visible Dire Mux child threads and worktrees.",
      });
      return;
    case "ping":
      success(message.id, {});
      return;
    case "tools/list":
      success(message.id, { tools });
      return;
    case "tools/call": {
      const controller = new AbortController();
      pending.set(String(message.id), controller);
      try {
        success(message.id, await callTool(message.params, controller.signal));
      } catch (error) {
        success(message.id, { content: [{ type: "text", text: errorMessage(error) }], isError: true });
      } finally {
        pending.delete(String(message.id));
      }
      return;
    }
    default:
      failure(message.id, -32601, `Method not found: ${message.method}`);
  }
}

function receive(line) {
  if (!line.trim()) return;
  let message;
  try { message = JSON.parse(line); } catch { failure(null, -32700, "Parse error"); return; }
  if (!isRecord(message) || message.jsonrpc !== "2.0" || typeof message.method !== "string") {
    failure(isRecord(message) && "id" in message ? message.id : null, -32600, "Invalid Request");
    return;
  }
  if (!("id" in message)) {
    if (message.method === "notifications/cancelled") pending.get(String(message.params?.requestId))?.abort();
    return;
  }
  void handleRequest(message).catch((error) => failure(message.id, -32603, errorMessage(error)));
}

const input = createInterface({ input: process.stdin, crlfDelay: Infinity });
input.on("line", receive);
input.on("close", () => {
  for (const controller of pending.values()) controller.abort();
});
