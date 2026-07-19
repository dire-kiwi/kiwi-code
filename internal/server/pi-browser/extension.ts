// Adapted from @dire-pi/chrome-devtools under the bundled MIT license.
import { mkdtemp, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import { StringEnum } from "@earendil-works/pi-ai";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import {
  DEFAULT_MAX_BYTES,
  DEFAULT_MAX_LINES,
  formatDimensionNote,
  formatSize,
  resizeImage,
  truncateHead,
  withFileMutationQueue,
} from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";

const SessionAction = StringEnum(
  ["status", "start", "disconnect", "stop"] as const,
);
const TabAction = StringEnum(["list", "new", "select", "close"] as const);
const NavigationAction = StringEnum(
  ["goto", "back", "forward", "reload"] as const,
);
const MouseButton = StringEnum(["left", "middle", "right"] as const);
const ElementState = StringEnum(["visible", "hidden"] as const);
const ScreenshotFormat = StringEnum(["png", "jpeg"] as const);
const CdpTarget = StringEnum(["page"] as const);
const BrowserBackend = StringEnum(["in-app"] as const);

const BROWSER_BACKEND = "in-app" as const;
const BROWSER_SKILL_PATH = fileURLToPath(
  new URL("../skills/dire-mux-in-app-browser", import.meta.url),
);
const BROWSER_TOOL_LOADER = "browser_tool_search";
const BROWSER_TOOL_NAMES = [
  "browser_session",
  "browser_tabs",
  "browser_navigate",
  "browser_snapshot",
  "browser_click",
  "browser_fill",
  "browser_key",
  "browser_wait",
  "browser_evaluate",
  "browser_screenshot",
  "browser_cdp",
] as const;
type BrowserToolName = (typeof BROWSER_TOOL_NAMES)[number];

const BROWSER_TOOL_NAME_SET = new Set<string>(BROWSER_TOOL_NAMES);
const BROWSER_TOOL_SEARCH_TEXT: Record<BrowserToolName, string> = {
  browser_session:
    "connect launch start status lifecycle disconnect stop chrome chromium endpoint",
  browser_tabs: "tab tabs window windows target list open create select switch close",
  browser_navigate:
    "browse visit open url website navigate navigation goto reload refresh back forward history",
  browser_snapshot:
    "inspect read understand page content text accessibility tree elements controls refs dom",
  browser_click: "click press activate button link mouse double right submit",
  browser_fill: "fill type enter input form field textarea contenteditable search login",
  browser_key: "keyboard key chord shortcut enter tab escape arrow control meta focus",
  browser_wait: "wait delay asynchronous selector visible hidden text url loading condition",
  browser_evaluate:
    "javascript evaluate runtime script execute read mutate document storage page",
  browser_screenshot:
    "screenshot image visual capture viewport full page png jpeg layout canvas",
  browser_cdp:
    "raw cdp chrome devtools protocol network emulation browser domain command advanced",
};
const TOOL_SEARCH_STOP_WORDS = new Set([
  "a",
  "an",
  "and",
  "browser",
  "for",
  "in",
  "of",
  "on",
  "page",
  "the",
  "to",
  "tool",
  "tools",
  "use",
  "web",
  "with",
]);

interface BrowserToolMatch {
  name: BrowserToolName;
  score: number;
}

interface RawCdpResult {
  method: string;
  result: unknown;
  target: "page";
  targetId?: string;
}

function isBrowserToolName(name: string): name is BrowserToolName {
  return BROWSER_TOOL_NAME_SET.has(name);
}

function searchBrowserTools(
  query: string,
  descriptions: ReadonlyMap<string, string>,
  limit: number,
): BrowserToolName[] {
  const normalized = query.toLowerCase();
  if (/\b(all|everything|every capability|full catalog)\b/.test(normalized)) {
    return BROWSER_TOOL_NAMES.filter((name) => descriptions.has(name));
  }
  const terms = normalized
    .split(/[^a-z0-9_]+/)
    .filter((term) => term.length > 1 && !TOOL_SEARCH_STOP_WORDS.has(term));
  const matches: BrowserToolMatch[] = BROWSER_TOOL_NAMES.filter((name) =>
    descriptions.has(name),
  ).map((name) => {
    const description = descriptions.get(name) ?? "";
    const haystack = `${name} ${name.replaceAll("_", " ")} ${description} ${BROWSER_TOOL_SEARCH_TEXT[name]}`.toLowerCase();
    const score = terms.reduce((total, term) => {
      const nameScore = name.includes(term) ? 4 : 0;
      return total + nameScore + (haystack.includes(term) ? 1 : 0);
    }, 0);
    return { name, score };
  })
    .filter((match) => match.score > 0)
    .sort(
      (left, right) =>
        right.score - left.score ||
        BROWSER_TOOL_NAMES.indexOf(left.name) -
          BROWSER_TOOL_NAMES.indexOf(right.name),
    );
  return matches.slice(0, limit).map((match) => match.name);
}

function includeBrowserToolDependencies(
  names: readonly BrowserToolName[],
): BrowserToolName[] {
  const expanded = new Set<BrowserToolName>(names);
  if (expanded.has("browser_click") || expanded.has("browser_fill")) {
    expanded.add("browser_snapshot");
  }
  return BROWSER_TOOL_NAMES.filter((name) => expanded.has(name));
}

interface OutputMetadata {
  fullOutputPath?: string;
  truncated?: boolean;
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function browserActionsEndpoint(): string {
  const rawEndpoint = process.env.DIRE_MUX_THREAD_ENDPOINT?.trim().replace(/\/+$/, "");
  if (!rawEndpoint) {
    throw new Error(
      "DIRE_MUX_THREAD_ENDPOINT is not set. Browser tools must run inside a Dire Mux agent session.",
    );
  }
  try {
    const endpoint = new URL(`${rawEndpoint}/browser/actions`);
    if (endpoint.protocol !== "http:" && endpoint.protocol !== "https:") {
      throw new Error("unsupported protocol");
    }
    return endpoint.toString();
  } catch {
    throw new Error("DIRE_MUX_THREAD_ENDPOINT is not a valid HTTP URL.");
  }
}

function responseErrorMessage(payload: unknown): string | undefined {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return undefined;
  const candidate = payload as { error?: unknown; message?: unknown };
  if (typeof candidate.error === "string" && candidate.error.trim()) {
    return candidate.error.trim();
  }
  if (typeof candidate.message === "string" && candidate.message.trim()) {
    return candidate.message.trim();
  }
  return undefined;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === "object" && !Array.isArray(value);
}

function hasFields(
  value: Record<string, unknown>,
  fields: ReadonlyArray<readonly [string, "array" | "boolean" | "number" | "object" | "string"]>,
): boolean {
  return fields.every(([name, type]) =>
    type === "array"
      ? Array.isArray(value[name])
      : type === "object"
        ? isRecord(value[name])
        : typeof value[name] === type,
  );
}

function hasPageFields(value: unknown): boolean {
  return (
    isRecord(value) &&
    hasFields(value, [
      ["id", "string"],
      ["title", "string"],
      ["url", "string"],
    ])
  );
}

function validBrowserActionResult(operation: string, result: Record<string, unknown>): boolean {
  if (operation.startsWith("session.")) {
    if (!hasFields(result, [["message", "string"], ["status", "object"]])) return false;
    const status = result.status as Record<string, unknown>;
    return hasFields(status, [
      ["endpoint", "string"],
      ["owned", "boolean"],
      ["pages", "number"],
      ["reachable", "boolean"],
    ]);
  }
  if (operation.startsWith("tabs.")) {
    return (
      hasFields(result, [["message", "string"], ["pages", "array"]]) &&
      (result.pages as unknown[]).every(hasPageFields)
    );
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
    return (
      hasFields(result, [
        ["clicked", "string"],
        ["newTabs", "array"],
        ["targetId", "string"],
        ["title", "string"],
        ["url", "string"],
      ]) && (result.newTabs as unknown[]).every(hasPageFields)
    );
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
    return (
      Object.hasOwn(result, "result") &&
      hasFields(result, [
        ["targetId", "string"],
        ["title", "string"],
        ["url", "string"],
      ])
    );
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
    return (
      Object.hasOwn(result, "result") &&
      hasFields(result, [["method", "string"], ["target", "string"]]) &&
      result.target === "page"
    );
  }
  return false;
}

async function browserAction<T>(
  operation: string,
  params: Record<string, unknown>,
  signal?: AbortSignal,
): Promise<T> {
  const token = process.env.DIRE_MUX_AGENT_TOKEN?.trim();
  if (!token) {
    throw new Error(
      "DIRE_MUX_AGENT_TOKEN is not set. Browser tools require a Dire Mux agent capability.",
    );
  }

  let response: Response;
  try {
    response = await fetch(browserActionsEndpoint(), {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Dire-Mux-Agent-Token": token,
      },
      body: JSON.stringify({ operation, params }),
      signal,
    });
  } catch (error) {
    if (signal?.aborted) throw error;
    throw new Error(`Could not reach the Dire Mux browser service: ${errorMessage(error)}`);
  }

  let payload: unknown;
  try {
    payload = await response.json();
  } catch {
    payload = undefined;
  }

  const detail = responseErrorMessage(payload);
  if (response.status === 404 && !detail) {
    throw new Error(
      "Dire Mux's browser actions endpoint is unavailable (HTTP 404). Update or restart Dire Mux.",
    );
  }
  if (response.status === 503) {
    throw new Error(
      "Dire Mux's in-app browser provider is unavailable (HTTP 503). Start or reconnect the Dire Mux desktop app.",
    );
  }
  if (!response.ok) {
    throw new Error(
      `Dire Mux browser action ${operation} failed (HTTP ${response.status})${detail ? `: ${detail}` : "."}`,
    );
  }
  const result = (payload as { result?: unknown } | undefined)?.result;
  if (
    !payload ||
    typeof payload !== "object" ||
    Array.isArray(payload) ||
    !("result" in payload) ||
    !isRecord(result) ||
    !validBrowserActionResult(operation, result)
  ) {
    throw new Error(
      `Dire Mux browser action ${operation} returned an invalid response; expected {result: ...}.`,
    );
  }
  return result as T;
}

function validateOperationTimeout(value: unknown, name = "timeoutMs"): void {
  if (value === undefined) return;
  if (!Number.isInteger(value) || (value as number) < 100 || (value as number) > 60_000) {
    throw new Error(`${name} must be an integer from 100 to 60000 milliseconds.`);
  }
}

function validateElementTarget(params: { ref?: string; selector?: string }): void {
  if ((typeof params.ref === "string") === (typeof params.selector === "string")) {
    throw new Error("Provide exactly one of ref or selector.");
  }
}

async function prepareScreenshot(data: unknown, mimeType: unknown) {
  if (typeof data !== "string" || (mimeType !== "image/png" && mimeType !== "image/jpeg")) {
    throw new Error("Dire Mux browser action screenshot returned an invalid image response.");
  }
  if (data.length === 0 || data.length % 4 !== 0 || !/^[A-Za-z0-9+/]*={0,2}$/.test(data)) {
    throw new Error("Dire Mux browser action screenshot returned invalid base64 data.");
  }
  const bytes = Buffer.from(data, "base64");
  const png = bytes.length >= 8 && bytes.subarray(0, 8).equals(
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
  );
  const jpeg = bytes.length >= 3 && bytes[0] === 0xff && bytes[1] === 0xd8 && bytes[2] === 0xff;
  if ((mimeType === "image/png" && !png) || (mimeType === "image/jpeg" && !jpeg)) {
    throw new Error("Dire Mux browser action screenshot bytes do not match its MIME type.");
  }
  const resized = await resizeImage(bytes, mimeType, {
    maxBytes: 4_500_000,
    maxHeight: 2_000,
    maxWidth: 2_000,
  });
  if (!resized) {
    throw new Error("The browser screenshot could not be prepared within Pi's image limits.");
  }
  return resized;
}

function safeStringify(value: unknown): string {
  if (value === undefined) return "undefined";
  try {
    const serialized = JSON.stringify(value, null, 2);
    return serialized ?? String(value);
  } catch {
    return String(value);
  }
}

async function limitOutput(text: string): Promise<{
  metadata: OutputMetadata;
  text: string;
}> {
  const truncation = truncateHead(text, {
    maxBytes: DEFAULT_MAX_BYTES,
    maxLines: DEFAULT_MAX_LINES,
  });
  if (!truncation.truncated) return { metadata: {}, text };

  const directory = await mkdtemp(join(tmpdir(), "pi-browser-output-"));
  const path = join(directory, "output.txt");
  await withFileMutationQueue(path, () => writeFile(path, text, "utf8"));
  return {
    metadata: { fullOutputPath: path, truncated: true },
    text: `${truncation.content}\n\n[Output truncated: showing ${truncation.outputLines} of ${truncation.totalLines} lines (${formatSize(truncation.outputBytes)} of ${formatSize(truncation.totalBytes)}). Full output saved to: ${path}]`,
  };
}

function formatStatus(status: {
  currentTargetId?: string;
  endpoint: string;
  error?: string;
  owned: boolean;
  pages: number;
  product?: string;
  protocolVersion?: string;
  reachable: boolean;
}): string {
  return [
    `Endpoint: ${status.endpoint}`,
    `Reachable: ${status.reachable ? "yes" : "no"}`,
    `Browser: ${status.product ?? "unknown"}`,
    `Protocol: ${status.protocolVersion ?? "unknown"}`,
    `Tabs: ${status.pages}`,
    `Current target: ${status.currentTargetId ?? "none"}`,
    `Provider managed: ${status.owned ? "yes" : "no"}`,
    ...(status.error ? [`Error: ${status.error}`] : []),
  ].join("\n");
}

function formatTabs(result: {
  currentTargetId?: string;
  message: string;
  pages: Array<{ id: string; title: string; url: string }>;
}): string {
  const lines = [result.message];
  if (result.pages.length === 0) lines.push("No page tabs are open.");
  for (const page of result.pages) {
    const current = page.id === result.currentTargetId ? "*" : " ";
    lines.push(
      `${current} ${page.id} | ${page.title || "(untitled)"} | ${page.url || "about:blank"}`,
    );
  }
  return lines.join("\n");
}

function formatRawResult(result: RawCdpResult): string {
  return [
    `CDP ${result.method} (${result.target}${result.targetId ? ` ${result.targetId}` : ""})`,
    safeStringify(result.result),
  ].join("\n\n");
}

export default function chromeDevtoolsExtension(pi: ExtensionAPI): void {
  let firstPartyRegistered = false;
  const registerFirstPartyBrowserTools = () => {
    if (firstPartyRegistered) return;
    firstPartyRegistered = true;

  pi.registerTool({
    name: "browser_session",
    label: "Browser Session",
    description:
      "Inspect or control Dire Mux's in-app browser. The only supported backend is in-app; existing-profile and external providers are unavailable. disconnect releases control while leaving the thread session running; stop destroys the session.",
    parameters: Type.Object(
      {
        action: Type.Unsafe<"status" | "start" | "disconnect" | "stop">({
          ...SessionAction,
          description: "Session operation.",
        }),
        backend: Type.Optional(
          Type.Unsafe<"in-app">({
            ...BrowserBackend,
            description: "Select Dire Mux's in-app browser backend.",
          }),
        ),
      },
      { additionalProperties: false },
    ),
    executionMode: "sequential",
    async execute(_id, params, signal) {
      const result = await browserAction<any>(
        `session.${params.action}`,
        params.backend ? { backend: params.backend } : {},
        signal,
      );
      return {
        content: [
          {
            type: "text",
            text: `${result.message}\nBackend: ${BROWSER_BACKEND}\n\n${formatStatus(result.status)}`,
          },
        ],
        details: {
          ...result.status,
          backend: BROWSER_BACKEND,
        },
      };
    },
  });

  pi.registerTool({
    name: "browser_tabs",
    label: "Browser Tabs",
    description:
      "List, open, select, or close in-app browser page targets. Target IDs may be unique prefixes. The thread browser is connected or started lazily. Use list after links that may open a new tab.",
    parameters: Type.Object(
      {
        action: Type.Unsafe<"list" | "new" | "select" | "close">({
          ...TabAction,
          description: "Tab operation.",
        }),
        targetId: Type.Optional(
          Type.String({
            description:
              "Target ID (required for select; optional for close, which defaults to current).",
            minLength: 1,
          }),
        ),
        url: Type.Optional(
          Type.String({
            description: "Initial URL for a new tab (defaults to about:blank).",
            minLength: 1,
          }),
        ),
      },
      { additionalProperties: false },
    ),
    executionMode: "sequential",
    async execute(_id, params, signal) {
      if (params.action === "select" && !params.targetId) {
        throw new Error("targetId is required when selecting a browser tab.");
      }
      const { action, ...operationParams } = params;
      const result = await browserAction<any>(`tabs.${action}`, operationParams, signal);
      return {
        content: [{ type: "text", text: formatTabs(result) }],
        details: {
          currentTargetId: result.currentTargetId,
          pages: result.pages.map(
            ({ id, title, url }: { id: string; title: string; url: string }) => ({
              id,
              title,
              url,
            }),
          ),
        },
      };
    },
  });

  pi.registerTool({
    name: "browser_navigate",
    label: "Browser Navigate",
    description:
      "Navigate the current in-app browser tab to a URL, move through history, or reload. Waits for document.readyState=complete. Bare hostnames use HTTPS; localhost uses HTTP.",
    parameters: Type.Object(
      {
        action: Type.Unsafe<"goto" | "back" | "forward" | "reload">({
          ...NavigationAction,
          description: "Navigation operation (defaults conceptually to goto).",
        }),
        url: Type.Optional(
          Type.String({
            description: "Destination URL; required for goto.",
            minLength: 1,
          }),
        ),
        timeoutMs: Type.Optional(
          Type.Integer({
            description: "Navigation timeout in milliseconds (default 30000).",
            maximum: 60_000,
            minimum: 100,
          }),
        ),
      },
      { additionalProperties: false },
    ),
    executionMode: "sequential",
    async execute(_id, params, signal) {
      if (params.action === "goto" && !params.url) {
        throw new Error("url is required for navigate.goto.");
      }
      validateOperationTimeout(params.timeoutMs);
      const { action, ...operationParams } = params;
      const result = await browserAction<any>(`navigate.${action}`, operationParams, signal);
      return {
        content: [
          {
            type: "text",
            text: `${result.action} complete.\nPage: ${result.title || "(untitled)"}\nURL: ${result.url}\nTarget: ${result.targetId}`,
          },
        ],
        details: result,
      };
    },
  });

  pi.registerTool({
    name: "browser_snapshot",
    label: "Browser Snapshot",
    description: `Return a compact accessibility-tree snapshot of the current tab with refs such as e1 for interactive elements. Text output is limited to ${DEFAULT_MAX_LINES} lines or ${formatSize(DEFAULT_MAX_BYTES)}; truncated output is saved to a temporary file. Refs become stale after navigation or DOM replacement.`,
    parameters: Type.Object(
      {
        interactiveOnly: Type.Optional(
          Type.Boolean({
            description: "Show only focusable/actionable nodes (default false).",
          }),
        ),
        maxDepth: Type.Optional(
          Type.Integer({
            description: "Maximum accessibility-tree depth (1-50, default 30).",
            maximum: 50,
            minimum: 1,
          }),
        ),
        maxNodes: Type.Optional(
          Type.Integer({
            description: "Maximum displayed nodes (1-1000, default 300).",
            maximum: 1_000,
            minimum: 1,
          }),
        ),
      },
      { additionalProperties: false },
    ),
    executionMode: "sequential",
    async execute(_id, params, signal) {
      const result = await browserAction<any>("snapshot", params, signal);
      const output = await limitOutput(result.text);
      return {
        content: [{ type: "text", text: output.text }],
        details: {
          ...output.metadata,
          includedNodes: result.includedNodes,
          omittedNodes: result.omittedNodes,
          refs: result.refs,
          targetId: result.targetId,
          title: result.title,
          url: result.url,
        },
      };
    },
  });

  pi.registerTool({
    name: "browser_click",
    label: "Browser Click",
    description:
      "Click an element in the current tab using a ref from the latest browser_snapshot or a CSS selector. Uses CDP Input mouse events and reports tabs opened by the click.",
    parameters: Type.Object(
      {
        ref: Type.Optional(
          Type.String({
            description: "Element ref from browser_snapshot.",
            minLength: 1,
          }),
        ),
        selector: Type.Optional(
          Type.String({ description: "CSS selector instead of ref.", minLength: 1 }),
        ),
        button: Type.Optional(
          Type.Unsafe<"left" | "middle" | "right">({
            ...MouseButton,
            description: "Mouse button (default left).",
          }),
        ),
        clickCount: Type.Optional(
          Type.Integer({
            description: "Click count, e.g. 2 for double-click (default 1).",
            maximum: 3,
            minimum: 1,
          }),
        ),
        waitMs: Type.Optional(
          Type.Integer({
            description: "Delay after clicking before reporting page state (default 500).",
            maximum: 5_000,
            minimum: 0,
          }),
        ),
      },
      { additionalProperties: false },
    ),
    executionMode: "sequential",
    async execute(_id, params, signal) {
      validateElementTarget(params);
      const result = await browserAction<any>("click", params, signal);
      const newTabs =
        result.newTabs.length === 0
          ? ""
          : `\nNew tabs: ${result.newTabs
              .map((tab: { id: string; url: string }) => `${tab.id} (${tab.url})`)
              .join(", ")}`;
      return {
        content: [
          {
            type: "text",
            text: `Clicked ${result.clicked}.\nPage: ${result.title || "(untitled)"}\nURL: ${result.url}\nTarget: ${result.targetId}${newTabs}`,
          },
        ],
        details: {
          ...result,
          newTabs: result.newTabs.map(
            ({ id, title, url }: { id: string; title: string; url: string }) => ({
              id,
              title,
              url,
            }),
          ),
        },
      };
    },
  });

  pi.registerTool({
    name: "browser_fill",
    label: "Browser Fill",
    description:
      "Focus and fill a text input, textarea, or contenteditable element using a browser_snapshot ref or CSS selector. Clears existing content by default and can press Enter afterward. The result does not echo the entered text.",
    parameters: Type.Object(
      {
        ref: Type.Optional(
          Type.String({
            description: "Element ref from browser_snapshot.",
            minLength: 1,
          }),
        ),
        selector: Type.Optional(
          Type.String({ description: "CSS selector instead of ref.", minLength: 1 }),
        ),
        text: Type.String({
          description: "Text to enter.",
          maxLength: 100_000,
        }),
        clear: Type.Optional(
          Type.Boolean({ description: "Replace existing content (default true)." }),
        ),
        submit: Type.Optional(
          Type.Boolean({ description: "Press Enter after filling (default false)." }),
        ),
      },
      { additionalProperties: false },
    ),
    executionMode: "sequential",
    async execute(_id, params, signal) {
      validateElementTarget(params);
      const result = await browserAction<any>("fill", params, signal);
      return {
        content: [
          {
            type: "text",
            text: `Filled ${result.filled} with ${result.textLength} characters${result.submitted ? " and pressed Enter" : ""}.\nPage: ${result.title || "(untitled)"}\nURL: ${result.url}\nTarget: ${result.targetId}`,
          },
        ],
        details: result,
      };
    },
  });

  pi.registerTool({
    name: "browser_key",
    label: "Browser Key",
    description:
      "Send a key or chord to the focused element in the current tab through CDP Input. Examples: Enter, Tab, Escape, ArrowDown, CTRL+A, META+L, F5, or a single character.",
    parameters: Type.Object(
      {
        key: Type.String({
          description: "Key name, character, or modifier chord such as CTRL+A.",
          minLength: 1,
        }),
      },
      { additionalProperties: false },
    ),
    executionMode: "sequential",
    async execute(_id, params, signal) {
      const result = await browserAction<any>("key", params, signal);
      return {
        content: [
          {
            type: "text",
            text: `Sent ${result.chord}.\nPage: ${result.title || "(untitled)"}\nURL: ${result.url}\nTarget: ${result.targetId}`,
          },
        ],
        details: result,
      };
    },
  });

  pi.registerTool({
    name: "browser_wait",
    label: "Browser Wait",
    description:
      "Wait for a delay and/or until all supplied page conditions match: CSS selector visible/hidden, visible page text, or URL substring. Conditions are polled through CDP Runtime.",
    parameters: Type.Object(
      {
        timeMs: Type.Optional(
          Type.Integer({
            description: "Initial fixed delay in milliseconds.",
            maximum: 60_000,
            minimum: 0,
          }),
        ),
        selector: Type.Optional(
          Type.String({ description: "CSS selector to wait for.", minLength: 1 }),
        ),
        state: Type.Optional(
          Type.Unsafe<"visible" | "hidden">({
            ...ElementState,
            description: "Required selector state (default visible).",
          }),
        ),
        text: Type.Optional(
          Type.String({ description: "Visible body text substring to wait for." }),
        ),
        urlContains: Type.Optional(
          Type.String({ description: "URL substring to wait for." }),
        ),
        timeoutMs: Type.Optional(
          Type.Integer({
            description: "Condition timeout in milliseconds (default 10000; timeMs plus timeoutMs may total at most 60000).",
            maximum: 60_000,
            minimum: 100,
          }),
        ),
      },
      { additionalProperties: false },
    ),
    executionMode: "sequential",
    async execute(_id, params, signal) {
      const hasCondition = params.selector !== undefined || params.text !== undefined || params.urlContains !== undefined;
      if (!hasCondition && params.timeMs === undefined) {
        throw new Error("Provide timeMs or at least one browser wait condition.");
      }
      if (params.state !== undefined && params.selector === undefined) {
        throw new Error("state requires selector.");
      }
      validateOperationTimeout(params.timeoutMs);
      if ((params.timeMs ?? 0) + (hasCondition ? (params.timeoutMs ?? 10_000) : 0) > 60_000) {
        throw new Error("timeMs and timeoutMs may total at most 60000 milliseconds.");
      }
      const result = await browserAction<any>("wait", params, signal);
      return {
        content: [
          {
            type: "text",
            text: `Wait conditions met after ${result.elapsedMs}ms.\nPage: ${result.title || "(untitled)"}\nURL: ${result.url}\nTarget: ${result.targetId}`,
          },
        ],
        details: result,
      };
    },
  });

  pi.registerTool({
    name: "browser_evaluate",
    label: "Browser Evaluate",
    description: `Evaluate JavaScript in the current page's main frame with awaitPromise and return-by-value enabled. This can read or mutate the page. Output is limited to ${DEFAULT_MAX_LINES} lines or ${formatSize(DEFAULT_MAX_BYTES)}; truncated output is saved to a temporary file.`,
    parameters: Type.Object(
      {
        expression: Type.String({
          description: "JavaScript expression or program to evaluate.",
          maxLength: 100_000,
          minLength: 1,
        }),
      },
      { additionalProperties: false },
    ),
    executionMode: "sequential",
    async execute(_id, params, signal) {
      const result = await browserAction<any>("evaluate", params, signal);
      const output = await limitOutput(safeStringify(result.result));
      return {
        content: [
          {
            type: "text",
            text: `Page: ${result.title || "(untitled)"}\nURL: ${result.url}\nTarget: ${result.targetId}\n\nResult:\n${output.text}`,
          },
        ],
        details: {
          ...output.metadata,
          targetId: result.targetId,
          title: result.title,
          url: result.url,
        },
      };
    },
  });

  pi.registerTool({
    name: "browser_screenshot",
    label: "Browser Screenshot",
    description:
      "Capture the current in-app browser tab as a PNG or JPEG and return it as image content. Defaults to the viewport and PNG; full-page captures are limited to 15MB.",
    parameters: Type.Object(
      {
        format: Type.Optional(
          Type.Unsafe<"png" | "jpeg">({
            ...ScreenshotFormat,
            description: "Image format (default png).",
          }),
        ),
        fullPage: Type.Optional(
          Type.Boolean({ description: "Capture beyond the viewport (default false)." }),
        ),
        quality: Type.Optional(
          Type.Integer({
            description: "JPEG quality from 0 to 100 (default 80; ignored for PNG).",
            maximum: 100,
            minimum: 0,
          }),
        ),
      },
      { additionalProperties: false },
    ),
    executionMode: "sequential",
    async execute(_id, params, signal) {
      const result = await browserAction<any>("screenshot", params, signal);
      const image = await prepareScreenshot(result.data, result.mimeType);
      const bytes = Buffer.from(image.data, "base64").byteLength;
      const dimensionNote = formatDimensionNote(image);
      return {
        content: [
          {
            type: "text",
            text: `Screenshot of ${result.title || "(untitled)"} (${result.url}), ${formatSize(bytes)}.${dimensionNote ? ` ${dimensionNote}` : ""}`,
          },
          { type: "image", data: image.data, mimeType: image.mimeType },
        ],
        details: {
          bytes,
          height: image.height,
          mimeType: image.mimeType,
          originalHeight: image.originalHeight,
          originalWidth: image.originalWidth,
          resized: image.wasResized,
          targetId: result.targetId,
          title: result.title,
          url: result.url,
          width: image.width,
        },
      };
    },
  });

  pi.registerTool({
    name: "browser_cdp",
    label: "Browser CDP",
    description: `Send an allowlisted Chrome DevTools Protocol command to the current page target. Browser-wide, filesystem, cookie-export, download, crash, and target-management commands are blocked. Use official Domain.method names and protocol-shaped params. Output is limited to ${DEFAULT_MAX_LINES} lines or ${formatSize(DEFAULT_MAX_BYTES)}; truncated output is saved to a temporary file.`,
    parameters: Type.Object(
      {
        method: Type.String({
          description: "CDP method such as Network.enable or Emulation.setDeviceMetricsOverride.",
          minLength: 3,
        }),
        params: Type.Optional(
          Type.Record(Type.String(), Type.Unknown(), {
            description: "Protocol parameters object (default {}).",
          }),
        ),
        target: Type.Optional(
          Type.Unsafe<"page">({
            ...CdpTarget,
            description: "Command target; only the selected page is available (default page).",
          }),
        ),
        timeoutMs: Type.Optional(
          Type.Integer({
            description: "Command timeout in milliseconds (default 30000).",
            maximum: 60_000,
            minimum: 100,
          }),
        ),
      },
      { additionalProperties: false },
    ),
    executionMode: "sequential",
    async execute(_id, params, signal) {
      if (!/^[A-Za-z][A-Za-z\d]*\.[A-Za-z][A-Za-z\d]*$/.test(params.method)) {
        throw new Error("CDP method must look like Domain.method.");
      }
      validateOperationTimeout(params.timeoutMs);
      const result = await browserAction<RawCdpResult>(
        "cdp",
        {
          method: params.method,
          params: params.params ?? {},
          target: params.target ?? "page",
          ...(params.timeoutMs !== undefined ? { timeoutMs: params.timeoutMs } : {}),
        },
        signal,
      );
      const output = await limitOutput(formatRawResult(result));
      return {
        content: [{ type: "text", text: output.text }],
        details: {
          ...output.metadata,
          method: result.method,
          target: result.target,
          targetId: result.targetId,
        },
      };
    },
  });

  pi.registerTool({
    name: BROWSER_TOOL_LOADER,
    label: "Find Browser Tools",
    description:
      "Search the registered Chrome DevTools capability catalog and dynamically activate browser_* tools relevant to a task. Provide a task query and optionally exact toolNames from the chrome-devtools-browser skill. Newly activated tools become callable on the following model turn.",
    parameters: Type.Object(
      {
        query: Type.String({
          description:
            "Browser task or capability to search for, such as 'navigate and inspect a website'.",
          minLength: 1,
        }),
        toolNames: Type.Optional(
          Type.Array(Type.String({ minLength: 1 }), {
            description:
              "Exact browser_* tool names to activate when already known from the skill. Dependencies are included automatically.",
            maxItems: BROWSER_TOOL_NAMES.length,
            minItems: 1,
          }),
        ),
        limit: Type.Optional(
          Type.Integer({
            description: "Maximum search matches when toolNames is omitted (default 4).",
            maximum: BROWSER_TOOL_NAMES.length,
            minimum: 1,
          }),
        ),
      },
      { additionalProperties: false },
    ),
    executionMode: "sequential",
    async execute(_id, params) {
      const catalog = new Map(
        pi
          .getAllTools()
          .filter((tool) => BROWSER_TOOL_NAME_SET.has(tool.name))
          .map((tool) => [tool.name, tool.description]),
      );
      const availableNames = BROWSER_TOOL_NAMES.filter((name) => catalog.has(name));
      const explicit = [...new Set((params.toolNames ?? []).map((name) => name.trim()))];
      const unknown = explicit.filter(
        (name) => !isBrowserToolName(name) || !catalog.has(name),
      );
      const requested =
        explicit.length > 0
          ? explicit.filter(
              (name): name is BrowserToolName =>
                isBrowserToolName(name) && catalog.has(name),
            )
          : searchBrowserTools(params.query, catalog, params.limit ?? 4);
      const matches = includeBrowserToolDependencies(requested).filter((name) =>
        catalog.has(name),
      );
      const active = pi.getActiveTools();
      const activeSet = new Set(active);
      const added = matches.filter((name) => !activeSet.has(name));
      const alreadyActive = matches.filter((name) => activeSet.has(name));

      if (added.length > 0) {
        pi.setActiveTools([...new Set([...active, ...added])]);
      }

      const lines: string[] = [];
      if (added.length > 0) {
        lines.push(`Loaded browser tools: ${added.join(", ")}.`);
        lines.push("They are available on the next model turn.");
      }
      if (alreadyActive.length > 0) {
        lines.push(`Already active: ${alreadyActive.join(", ")}.`);
      }
      if (unknown.length > 0) {
        lines.push(`Unknown or unavailable browser tool names ignored: ${unknown.join(", ")}.`);
      }
      if (matches.length === 0) {
        lines.push(`No browser tools matched: ${params.query}.`);
        lines.push(`Available names: ${availableNames.join(", ") || "none"}.`);
      }

      return {
        content: [{ type: "text", text: lines.join("\n") }],
        details: {
          added,
          alreadyActive,
          matches,
          query: params.query,
          unknown,
        },
      };
    },
  });
  };

  const restoreActiveBrowserTools = (ctx: any) => {
    const available = new Set(pi.getAllTools().map((tool) => tool.name));
    if (!available.has(BROWSER_TOOL_LOADER)) return;

    const restored = new Set<BrowserToolName>();
    for (const entry of ctx.sessionManager.getBranch()) {
      if (entry.type !== "message" || entry.message.role !== "toolResult") continue;
      if (entry.message.toolName !== BROWSER_TOOL_LOADER) continue;
      for (const name of entry.message.addedToolNames ?? []) {
        if (isBrowserToolName(name)) restored.add(name);
      }
    }

    const initial = pi
      .getActiveTools()
      .filter((name) => !BROWSER_TOOL_NAME_SET.has(name));
    pi.setActiveTools([
      ...new Set([...initial, BROWSER_TOOL_LOADER, ...restored]),
    ]);
  };
  pi.on("session_start", (_event, ctx) => {
    if (firstPartyRegistered) {
      restoreActiveBrowserTools(ctx);
      return;
    }
    const conflicts = pi.getAllTools().filter((tool) =>
      BROWSER_TOOL_NAME_SET.has(tool.name) || tool.name === BROWSER_TOOL_LOADER,
    );
    if (conflicts.length > 0) {
      const sources = [...new Set(conflicts.map((tool) => tool.sourceInfo?.path).filter(Boolean))];
      const message = [
        "Dire Mux's first-party in-app browser tools are disabled because another browser extension is installed.",
        ...(sources.length > 0 ? [`Conflicting extension: ${sources.join(", ")}`] : []),
        "Remove or disable @dire-pi/chrome-devtools (or the conflicting browser_* extension), then run /reload to migrate to the in-app backend.",
      ].join("\n");
      console.error(message);
      ctx.ui.notify(message, "warning");
      return;
    }
    registerFirstPartyBrowserTools();
    registerBrowserCommand();
    restoreActiveBrowserTools(ctx);
  });
  pi.on("session_tree", (_event, ctx) => {
    if (firstPartyRegistered) restoreActiveBrowserTools(ctx);
  });

  pi.on("resources_discover", () => ({
    skillPaths: firstPartyRegistered ? [BROWSER_SKILL_PATH] : [],
  }));

  const registerBrowserCommand = () => pi.registerCommand("browser", {
    description: "In-app browser status, tabs, start, or stop",
    getArgumentCompletions(prefix) {
      const values = ["status", "tabs", "start", "stop"];
      const items = values
        .filter((value) => value.startsWith(prefix.trim()))
        .map((value) => ({ label: value, value }));
      return items.length > 0 ? items : null;
    },
    handler: async (args, ctx) => {
      const action = args.trim().toLowerCase() || "status";
      try {
        if (action === "tabs") {
          const result = await browserAction<any>("tabs.list", {});
          ctx.ui.notify(formatTabs(result), "info");
          return;
        }
        if (!["status", "start", "stop"].includes(action)) {
          ctx.ui.notify(
            "Usage: /browser [status|tabs|start|stop]",
            "warning",
          );
          return;
        }
        const result = await browserAction<any>(`session.${action}`, {});
        ctx.ui.notify(
          `${result.message}\nBackend: ${BROWSER_BACKEND}\n${formatStatus(result.status)}`,
          "info",
        );
      } catch (error) {
        ctx.ui.notify(errorMessage(error), "error");
      }
    },
  });
}
