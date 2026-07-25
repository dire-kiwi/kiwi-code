#!/usr/bin/env node

// Adapted from @dire-pi/chrome-devtools under the bundled MIT license.
import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createInterface } from "node:readline";

const SERVER_NAME = "kiwi-code-browser";
const SERVER_VERSION = "1.1.0";
const MAX_TEXT_BYTES = 50 * 1024;
const MAX_TEXT_LINES = 2_000;
const MAX_IMAGE_BYTES = 5 * 1024 * 1024;

const string = (description, options = {}) => ({
  type: "string",
  description,
  ...options,
});
const integer = (description, minimum, maximum) => ({
  type: "integer",
  description,
  minimum,
  maximum,
});
const boolean = (description) => ({ type: "boolean", description });
const object = (properties, required = []) => ({
  type: "object",
  properties,
  ...(required.length > 0 ? { required } : {}),
  additionalProperties: false,
});

const tools = [
  {
    name: "browser_session",
    title: "Browser Session",
    description:
      "Inspect or control the current Kiwi Code thread's in-app browser session. Most browser tools start or connect lazily; use this for explicit status, start, disconnect, or stop operations.",
    inputSchema: object(
      {
        action: string("Session operation.", {
          enum: ["status", "start", "disconnect", "stop"],
        }),
        backend: string("Browser backend; only in-app is supported.", {
          enum: ["in-app"],
        }),
      },
      ["action"],
    ),
    annotations: { destructiveHint: true, openWorldHint: true },
  },
  {
    name: "browser_recording",
    title: "Browser Recording",
    description:
      "Inspect, start, or stop video recording for the current in-app browser tab. Start requires a concise purpose title, lazily opens a blank tab if needed, and defaults to a 300-second inactivity timeout. Browser operations refresh the timeout; inactivity automatically finalizes the WebM.",
    inputSchema: object(
      {
        action: string("Recording operation.", { enum: ["status", "start", "stop"] }),
        targetId: string("Target tab ID for start; defaults to the selected tab.", {
          minLength: 1,
        }),
        title: string("Required for start: 2–12 words explaining the point of the recording.", {
          minLength: 3,
          maxLength: 80,
        }),
        recordingId: string("Exact recording ID required for stop.", {
          minLength: 1,
        }),
        idleTimeoutSeconds: integer(
          "Auto-stop after this many seconds without browser activity; defaults to 300.",
          30,
          3_600,
        ),
      },
      ["action"],
    ),
    annotations: { destructiveHint: true, openWorldHint: true },
  },
  {
    name: "browser_tabs",
    title: "Browser Tabs",
    description:
      "List, open, select, or close in-app browser page targets. Target IDs may be unique prefixes. Use list after actions that may open a new tab.",
    inputSchema: object(
      {
        action: string("Tab operation.", {
          enum: ["list", "new", "select", "close"],
        }),
        targetId: string(
          "Target ID. Required for select; close defaults to the current target.",
          { minLength: 1 },
        ),
        url: string("Initial URL for a new tab; defaults to about:blank.", {
          minLength: 1,
        }),
      },
      ["action"],
    ),
    annotations: { destructiveHint: true, openWorldHint: true },
  },
  {
    name: "browser_navigate",
    title: "Browser Navigate",
    description:
      "Navigate the current in-app browser tab to a URL, move through history, or reload. Bare hostnames use HTTPS and localhost uses HTTP.",
    inputSchema: object(
      {
        action: string("Navigation operation.", {
          enum: ["goto", "back", "forward", "reload"],
        }),
        url: string("Destination URL; required for goto.", { minLength: 1 }),
        timeoutMs: integer("Navigation timeout in milliseconds.", 100, 60_000),
      },
      ["action"],
    ),
    annotations: { destructiveHint: false, openWorldHint: true },
  },
  {
    name: "browser_snapshot",
    title: "Browser Snapshot",
    description:
      "Return a compact accessibility-tree snapshot of the current tab with refs such as e1 for actionable elements. Refs become stale after navigation or DOM replacement.",
    inputSchema: object({
      interactiveOnly: boolean("Show only focusable or actionable nodes."),
      maxDepth: integer("Maximum accessibility-tree depth.", 1, 50),
      maxNodes: integer("Maximum displayed nodes.", 1, 1_000),
    }),
    annotations: { readOnlyHint: true, destructiveHint: false, openWorldHint: true },
  },
  {
    name: "browser_click",
    title: "Browser Click",
    description:
      "Click an element in the current tab using exactly one ref from the latest browser_snapshot or one CSS selector. Reports tabs opened by the click.",
    inputSchema: object({
      ref: string("Element ref from browser_snapshot.", { minLength: 1 }),
      selector: string("CSS selector instead of a snapshot ref.", { minLength: 1 }),
      button: string("Mouse button.", { enum: ["left", "middle", "right"] }),
      clickCount: integer("Click count, such as 2 for double-click.", 1, 3),
      waitMs: integer("Delay after clicking before reporting page state.", 0, 5_000),
    }),
    annotations: { destructiveHint: true, openWorldHint: true },
  },
  {
    name: "browser_fill",
    title: "Browser Fill",
    description:
      "Focus and fill a text input, textarea, or contenteditable element using exactly one browser_snapshot ref or one CSS selector. Clears existing content by default and can submit with Enter.",
    inputSchema: object(
      {
        ref: string("Element ref from browser_snapshot.", { minLength: 1 }),
        selector: string("CSS selector instead of a snapshot ref.", {
          minLength: 1,
        }),
        text: string("Text to enter.", { maxLength: 100_000 }),
        clear: boolean("Replace existing content; defaults to true."),
        submit: boolean("Press Enter after filling; defaults to false."),
      },
      ["text"],
    ),
    annotations: { destructiveHint: true, openWorldHint: true },
  },
  {
    name: "browser_key",
    title: "Browser Key",
    description:
      "Send a key or modifier chord to the focused element in the current tab, such as Enter, Tab, Escape, ArrowDown, CTRL+A, META+L, F5, or a single character.",
    inputSchema: object(
      { key: string("Key name, character, or modifier chord.", { minLength: 1 }) },
      ["key"],
    ),
    annotations: { destructiveHint: true, openWorldHint: true },
  },
  {
    name: "browser_wait",
    title: "Browser Wait",
    description:
      "Wait for a delay and/or until all supplied page conditions match: CSS selector visibility, visible page text, or a URL substring.",
    inputSchema: object({
      timeMs: integer("Initial fixed delay in milliseconds.", 0, 60_000),
      selector: string("CSS selector to wait for.", { minLength: 1 }),
      state: string("Required selector state; defaults to visible.", {
        enum: ["visible", "hidden"],
      }),
      text: string("Visible body-text substring to wait for."),
      urlContains: string("URL substring to wait for."),
      timeoutMs: integer("Condition timeout in milliseconds.", 100, 60_000),
    }),
    annotations: { readOnlyHint: true, destructiveHint: false, openWorldHint: true },
  },
  {
    name: "browser_evaluate",
    title: "Browser Evaluate",
    description:
      "Evaluate JavaScript in the current page's main frame with promises awaited and return the value. This can read or mutate private page state.",
    inputSchema: object(
      {
        expression: string("JavaScript expression or program to evaluate.", {
          minLength: 1,
          maxLength: 100_000,
        }),
      },
      ["expression"],
    ),
    annotations: { destructiveHint: true, openWorldHint: true },
  },
  {
    name: "browser_screenshot",
    title: "Browser Screenshot",
    description:
      "Capture the current in-app browser tab as a PNG or JPEG image. Defaults to the viewport and PNG; use JPEG with lower quality for large full-page captures.",
    inputSchema: object({
      format: string("Image format; defaults to png.", { enum: ["png", "jpeg"] }),
      fullPage: boolean("Capture beyond the viewport; defaults to false."),
      quality: integer("JPEG quality from 0 to 100; ignored for PNG.", 0, 100),
    }),
    annotations: { readOnlyHint: true, destructiveHint: false, openWorldHint: true },
  },
  {
    name: "browser_cdp",
    title: "Browser CDP",
    description:
      "Send an allowlisted Chrome DevTools Protocol Domain.method command to the selected page target. Browser-wide, filesystem, cookie-export, download, crash, and target-management commands are blocked.",
    inputSchema: object(
      {
        method: string("CDP method such as Network.enable.", { minLength: 3 }),
        params: {
          type: "object",
          description: "Official protocol parameters; defaults to an empty object.",
          additionalProperties: true,
        },
        target: string("Command target; only page is supported.", {
          enum: ["page"],
        }),
        timeoutMs: integer("Command timeout in milliseconds.", 100, 60_000),
      },
      ["method"],
    ),
    annotations: { destructiveHint: true, openWorldHint: true },
  },
];

const toolsByName = new Map(tools.map((tool) => [tool.name, tool]));

function isRecord(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function hasFields(value, fields) {
  return fields.every(([name, type]) =>
    type === "array"
      ? Array.isArray(value[name])
      : type === "object"
        ? isRecord(value[name])
        : typeof value[name] === type,
  );
}

function hasPageFields(value) {
  return isRecord(value) && hasFields(value, [
    ["id", "string"],
    ["title", "string"],
    ["url", "string"],
  ]);
}

function validBrowserRecording(value) {
  return isRecord(value) && hasFields(value, [
    ["id", "string"],
    ["startedAt", "string"],
    ["state", "string"],
    ["targetId", "string"],
    ["title", "string"],
  ]) && ["starting", "recording", "finalizing", "completed"].includes(value.state) &&
    (value.idleTimeoutMs === undefined || typeof value.idleTimeoutMs === "number") &&
    (value.idleDeadlineAt === undefined || typeof value.idleDeadlineAt === "string");
}

function validBrowserActionResult(operation, result) {
  if (operation === "recording.status") {
    return Object.hasOwn(result, "recording") &&
      (result.recording === null || validBrowserRecording(result.recording)) &&
      Array.isArray(result.recordings) && result.recordings.every(validBrowserRecording);
  }
  if (operation === "recording.start" || operation === "recording.stop") {
    return validBrowserRecording(result);
  }
  if (operation.startsWith("session.")) {
    if (!hasFields(result, [["message", "string"], ["status", "object"]])) return false;
    return hasFields(result.status, [
      ["endpoint", "string"],
      ["owned", "boolean"],
      ["pages", "number"],
      ["reachable", "boolean"],
    ]);
  }
  if (operation.startsWith("tabs.")) {
    return hasFields(result, [["message", "string"], ["pages", "array"]]) &&
      result.pages.every(hasPageFields);
  }
  if (operation.startsWith("navigate.")) {
    return hasFields(result, [
      ["action", "string"],
      ["targetId", "string"],
      ["title", "string"],
      ["url", "string"],
    ]);
  }
  if (operation === "snapshot") {
    return hasFields(result, [
      ["includedNodes", "number"],
      ["omittedNodes", "number"],
      ["refs", "number"],
      ["targetId", "string"],
      ["text", "string"],
      ["title", "string"],
      ["url", "string"],
    ]);
  }
  if (operation === "click") {
    return hasFields(result, [
      ["clicked", "string"],
      ["newTabs", "array"],
      ["targetId", "string"],
      ["title", "string"],
      ["url", "string"],
    ]) && result.newTabs.every(hasPageFields);
  }
  if (operation === "fill") {
    return hasFields(result, [
      ["filled", "string"],
      ["submitted", "boolean"],
      ["targetId", "string"],
      ["textLength", "number"],
      ["title", "string"],
      ["url", "string"],
    ]);
  }
  if (operation === "key") {
    return hasFields(result, [
      ["chord", "string"],
      ["targetId", "string"],
      ["title", "string"],
      ["url", "string"],
    ]);
  }
  if (operation === "wait") {
    return hasFields(result, [
      ["elapsedMs", "number"],
      ["targetId", "string"],
      ["title", "string"],
      ["url", "string"],
    ]);
  }
  if (operation === "evaluate") {
    return Object.hasOwn(result, "result") && hasFields(result, [
      ["targetId", "string"],
      ["title", "string"],
      ["url", "string"],
    ]);
  }
  if (operation === "screenshot") {
    return hasFields(result, [
      ["data", "string"],
      ["mimeType", "string"],
      ["targetId", "string"],
      ["title", "string"],
      ["url", "string"],
    ]);
  }
  if (operation === "cdp") {
    return Object.hasOwn(result, "result") &&
      hasFields(result, [["method", "string"], ["target", "string"]]) &&
      result.target === "page";
  }
  return false;
}

function validateArguments(tool, args) {
  if (!isRecord(args)) throw new Error("Tool arguments must be an object.");
  const schema = tool.inputSchema;
  for (const name of schema.required ?? []) {
    if (!(name in args)) throw new Error(`${name} is required.`);
  }
  for (const [name, value] of Object.entries(args)) {
    const property = schema.properties[name];
    if (!property) throw new Error(`Unknown argument: ${name}.`);
    if (property.type === "string") {
      if (typeof value !== "string") throw new Error(`${name} must be a string.`);
      if (property.minLength !== undefined && value.length < property.minLength) {
        throw new Error(`${name} must not be empty.`);
      }
      if (property.maxLength !== undefined && value.length > property.maxLength) {
        throw new Error(`${name} is too long.`);
      }
      if (property.enum && !property.enum.includes(value)) {
        throw new Error(`${name} must be one of: ${property.enum.join(", ")}.`);
      }
    } else if (property.type === "integer") {
      if (!Number.isInteger(value)) throw new Error(`${name} must be an integer.`);
      if (value < property.minimum || value > property.maximum) {
        throw new Error(`${name} must be from ${property.minimum} to ${property.maximum}.`);
      }
    } else if (property.type === "boolean") {
      if (typeof value !== "boolean") throw new Error(`${name} must be a boolean.`);
    } else if (property.type === "object" && !isRecord(value)) {
      throw new Error(`${name} must be an object.`);
    }
  }

  if ((tool.name === "browser_click" || tool.name === "browser_fill") &&
      ((typeof args.ref === "string") === (typeof args.selector === "string"))) {
    throw new Error("Provide exactly one of ref or selector.");
  }
  if (tool.name === "browser_recording") {
    if (args.action !== "stop" && args.recordingId) {
      throw new Error("recordingId is only valid for recording.stop.");
    }
    if (args.action === "stop" && !args.recordingId) {
      throw new Error("recordingId is required for recording.stop.");
    }
    if (args.action === "start") {
      const title = typeof args.title === "string" ? args.title.replace(/\s+/g, " ").trim() : "";
      const words = title.split(" ").filter(Boolean);
      if (title.length < 3 || title.length > 80 || words.length < 2 || words.length > 12) {
        throw new Error("recording.start requires a 2–12 word title explaining the point of the recording.");
      }
      args.title = title;
    } else if (args.targetId || args.title || args.idleTimeoutSeconds !== undefined) {
      throw new Error("targetId, title, and idleTimeoutSeconds are only valid for recording.start.");
    }
  }
  if (tool.name === "browser_tabs" && args.action === "select" && !args.targetId) {
    throw new Error("targetId is required when selecting a browser tab.");
  }
  if (tool.name === "browser_navigate" && args.action === "goto" && !args.url) {
    throw new Error("url is required for navigate.goto.");
  }
  if (tool.name === "browser_wait") {
    const hasCondition = args.selector !== undefined || args.text !== undefined || args.urlContains !== undefined;
    if (!hasCondition && args.timeMs === undefined) {
      throw new Error("Provide timeMs or at least one browser wait condition.");
    }
    if (args.state !== undefined && args.selector === undefined) {
      throw new Error("state requires selector.");
    }
    if ((args.timeMs ?? 0) + (hasCondition ? (args.timeoutMs ?? 10_000) : 0) > 60_000) {
      throw new Error("timeMs and timeoutMs may total at most 60000 milliseconds.");
    }
  }
  if (tool.name === "browser_cdp" &&
      !/^[A-Za-z][A-Za-z\d]*\.[A-Za-z][A-Za-z\d]*$/.test(args.method)) {
    throw new Error("CDP method must look like Domain.method.");
  }
}

function actionForTool(name, args) {
  const params = { ...args };
  switch (name) {
    case "browser_session": {
      delete params.action;
      return { operation: `session.${args.action}`, params };
    }
    case "browser_recording": {
      delete params.action;
      delete params.idleTimeoutSeconds;
      if (args.action === "start") {
        params.title = args.title;
        params.idleTimeoutMs = (args.idleTimeoutSeconds ?? 300) * 1_000;
      }
      return { operation: `recording.${args.action}`, params };
    }
    case "browser_tabs": {
      delete params.action;
      return { operation: `tabs.${args.action}`, params };
    }
    case "browser_navigate": {
      delete params.action;
      return { operation: `navigate.${args.action}`, params };
    }
    case "browser_snapshot":
      return { operation: "snapshot", params };
    case "browser_click":
      return { operation: "click", params };
    case "browser_fill":
      return { operation: "fill", params };
    case "browser_key":
      return { operation: "key", params };
    case "browser_wait":
      return { operation: "wait", params };
    case "browser_evaluate":
      return { operation: "evaluate", params };
    case "browser_screenshot":
      return { operation: "screenshot", params };
    case "browser_cdp":
      return {
        operation: "cdp",
        params: {
          ...params,
          params: args.params ?? {},
          target: args.target ?? "page",
        },
      };
    default:
      throw new Error(`Unknown browser tool: ${name}.`);
  }
}

function browserActionsEndpoint() {
  const raw = process.env.KIWI_CODE_THREAD_ENDPOINT?.trim().replace(/\/+$/, "");
  if (!raw) {
    throw new Error(
      "KIWI_CODE_THREAD_ENDPOINT is not set. Browser tools must run inside a Kiwi Code-managed Claude Code session.",
    );
  }
  let endpoint;
  try {
    endpoint = new URL(`${raw}/browser/actions`);
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
      throw new Error("Could not read the Kiwi Code browser capability file.");
    }
    if (!cachedAgentToken) {
      throw new Error("The Kiwi Code browser capability file is empty.");
    }
    return cachedAgentToken;
  }

  const environmentToken = process.env.KIWI_CODE_AGENT_TOKEN?.trim();
  if (!environmentToken) {
    throw new Error(
      "KIWI_CODE_AGENT_TOKEN_FILE is not set. Browser tools require a Kiwi Code agent capability.",
    );
  }
  cachedAgentToken = environmentToken;
  return cachedAgentToken;
}

function responseMessage(payload) {
  if (!isRecord(payload)) return undefined;
  for (const name of ["error", "message"]) {
    if (typeof payload[name] === "string" && payload[name].trim()) {
      return payload[name].trim();
    }
  }
  return undefined;
}

async function browserAction(operation, params, signal) {
  let response;
  try {
    response = await fetch(browserActionsEndpoint(), {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Kiwi-Code-Agent-Token": await agentToken(),
      },
      body: JSON.stringify({ operation, params }),
      signal,
    });
  } catch (error) {
    if (signal?.aborted) throw new Error("Browser action was cancelled.");
    throw new Error(`Could not reach the Kiwi Code browser service: ${errorMessage(error)}`);
  }

  let payload;
  try {
    payload = await response.json();
  } catch {
    payload = undefined;
  }
  const detail = responseMessage(payload);
  if (response.status === 404 && !detail) {
    throw new Error(
      "Kiwi Code's browser actions endpoint is unavailable (HTTP 404). Update or restart Kiwi Code.",
    );
  }
  if (response.status === 503) {
    throw new Error(
      "Kiwi Code's in-app browser provider is unavailable (HTTP 503). Check the configured browser backend; headless mode requires a supported Chrome installation, while Electron mode requires the desktop app.",
    );
  }
  if (!response.ok) {
    throw new Error(
      `Kiwi Code browser action ${operation} failed (HTTP ${response.status})${detail ? `: ${detail}` : "."}`,
    );
  }
  if (!isRecord(payload) || !isRecord(payload.result) ||
      !validBrowserActionResult(operation, payload.result)) {
    throw new Error(
      `Kiwi Code browser action ${operation} returned an invalid response; expected {result: ...}.`,
    );
  }
  return payload.result;
}

function errorMessage(error) {
  return error instanceof Error ? error.message : String(error);
}

function pretty(value) {
  try {
    return JSON.stringify(value, null, 2) ?? String(value);
  } catch {
    return String(value);
  }
}

function truncateUtf8(text, maximumBytes) {
  const encoded = Buffer.from(text, "utf8");
  if (encoded.length <= maximumBytes) return text;
  return new TextDecoder().decode(encoded.subarray(0, maximumBytes));
}

async function limitText(text) {
  const lines = text.split("\n");
  if (lines.length <= MAX_TEXT_LINES && Buffer.byteLength(text, "utf8") <= MAX_TEXT_BYTES) {
    return text;
  }

  let visible = lines.slice(0, MAX_TEXT_LINES).join("\n");
  visible = truncateUtf8(visible, MAX_TEXT_BYTES);
  let saved = "";
  try {
    const directory = await mkdtemp(join(tmpdir(), "kiwi-code-claude-browser-"));
    const path = join(directory, "output.txt");
    await writeFile(path, text, "utf8");
    saved = ` Full output saved to: ${path}`;
  } catch {
    saved = " Full output could not be saved.";
  }
  return `${visible}\n\n[Output truncated to ${MAX_TEXT_LINES} lines or ${MAX_TEXT_BYTES} bytes.${saved}]`;
}

function pageHeading(result) {
  const details = [];
  if (typeof result.title === "string") details.push(`Page: ${result.title || "(untitled)"}`);
  if (typeof result.url === "string") details.push(`URL: ${result.url}`);
  if (typeof result.targetId === "string") details.push(`Target: ${result.targetId}`);
  return details.join("\n");
}

function validImage(data, mimeType) {
  if (typeof data !== "string" ||
      (mimeType !== "image/png" && mimeType !== "image/jpeg") ||
      data.length === 0 || data.length % 4 !== 0 ||
      !/^[A-Za-z0-9+/]*={0,2}$/.test(data)) {
    return undefined;
  }
  const bytes = Buffer.from(data, "base64");
  const png = bytes.length >= 8 && bytes.subarray(0, 8).equals(
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
  );
  const jpeg = bytes.length >= 3 && bytes[0] === 0xff && bytes[1] === 0xd8 && bytes[2] === 0xff;
  if ((mimeType === "image/png" && !png) || (mimeType === "image/jpeg" && !jpeg)) {
    return undefined;
  }
  return bytes;
}

async function toolContent(name, result) {
  if (name === "browser_snapshot" && typeof result.text === "string") {
    const heading = pageHeading(result);
    return [{
      type: "text",
      text: await limitText(`${heading}${heading ? "\n\n" : ""}${result.text}`),
    }];
  }
  if (name === "browser_screenshot") {
    const bytes = validImage(result.data, result.mimeType);
    if (!bytes) throw new Error("Kiwi Code returned an invalid browser screenshot.");
    if (bytes.length > MAX_IMAGE_BYTES) {
      throw new Error(
        `Browser screenshot is ${bytes.length} bytes; Claude Code accepts at most ${MAX_IMAGE_BYTES}. Retry with JPEG, lower quality, or fullPage false.`,
      );
    }
    const heading = pageHeading(result) || "Browser screenshot";
    return [
      { type: "text", text: `${heading}\nImage: ${result.mimeType}, ${bytes.length} bytes` },
      { type: "image", data: result.data, mimeType: result.mimeType },
    ];
  }
  return [{ type: "text", text: await limitText(pretty(result)) }];
}

async function callTool(params, signal) {
  if (!isRecord(params) || typeof params.name !== "string") {
    throw new Error("tools/call requires a tool name.");
  }
  const tool = toolsByName.get(params.name);
  if (!tool) throw new Error(`Unknown tool: ${params.name}.`);
  const args = params.arguments ?? {};
  validateArguments(tool, args);
  const action = actionForTool(tool.name, args);
  const result = await browserAction(action.operation, action.params, signal);
  return { content: await toolContent(tool.name, result), isError: false };
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
          "Controls the in-app browser owned by the current Kiwi Code thread. Use focused browser_* tools and inspect the page with browser_snapshot before interacting with refs.",
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
  const requestId = message.params?.requestId;
  pending.get(String(requestId))?.abort();
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
