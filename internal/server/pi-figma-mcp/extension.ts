// Bridges the Figma MCP server into Pi, which has no built-in MCP support.
// Kiwi Code loads this extension only for projects with Figma MCP enabled and
// passes the endpoint through KIWI_CODE_FIGMA_MCP_URL.
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import type { TSchema } from "typebox";

const PROTOCOL_VERSION = "2025-06-18";
const CONNECT_TIMEOUT_MS = 10_000;
const CALL_TIMEOUT_MS = 120_000;
const TOOL_PREFIX = "figma_";

interface MCPToolDescriptor {
  name: string;
  title?: string;
  description?: string;
  inputSchema?: Record<string, unknown>;
}

interface MCPContentItem {
  type: string;
  text?: string;
  data?: string;
  mimeType?: string;
  uri?: string;
  resource?: { text?: string; uri?: string; mimeType?: string };
}

interface MCPToolResult {
  content?: MCPContentItem[];
  structuredContent?: unknown;
  isError?: boolean;
}

function combineSignals(signal: AbortSignal | undefined, timeoutMs: number): AbortSignal {
  const timeout = AbortSignal.timeout(timeoutMs);
  return signal ? AbortSignal.any([signal, timeout]) : timeout;
}

/**
 * Parse a streamable-HTTP MCP response body. The transport may answer a POST
 * with either a single JSON object or an SSE stream whose data frames carry the
 * JSON-RPC messages, so handle both and pick the frame matching the request id.
 */
function parseResponseBody(contentType: string, body: string, id: number): any {
  if (contentType.includes("text/event-stream")) {
    let last: any;
    for (const line of body.split(/\r?\n/)) {
      if (!line.startsWith("data:")) continue;
      const payload = line.slice(5).trim();
      if (!payload) continue;
      let message: any;
      try {
        message = JSON.parse(payload);
      } catch {
        continue;
      }
      if (message?.id === id) return message;
      last = message;
    }
    if (last) return last;
    throw new Error("the Figma MCP server returned no JSON-RPC message");
  }
  return JSON.parse(body);
}

class FigmaMCPClient {
  private sessionId: string | undefined;
  private initialized = false;
  private nextId = 1;
  private readonly url: string;

  constructor(url: string) {
    this.url = url;
  }

  private async send(
    method: string,
    params: unknown,
    signal: AbortSignal | undefined,
    timeoutMs: number,
    notification = false,
  ): Promise<any> {
    const id = this.nextId++;
    const headers: Record<string, string> = {
      "content-type": "application/json",
      accept: "application/json, text/event-stream",
    };
    if (this.sessionId) headers["mcp-session-id"] = this.sessionId;
    if (this.initialized) headers["mcp-protocol-version"] = PROTOCOL_VERSION;

    const body = notification
      ? { jsonrpc: "2.0", method, params }
      : { jsonrpc: "2.0", id, method, params };
    const response = await fetch(this.url, {
      method: "POST",
      headers,
      body: JSON.stringify(body),
      signal: combineSignals(signal, timeoutMs),
    });
    const session = response.headers.get("mcp-session-id");
    if (session) this.sessionId = session;
    if (!response.ok) {
      const detail = (await response.text().catch(() => "")).slice(0, 500);
      throw new Error(`Figma MCP ${method} failed: HTTP ${response.status} ${detail}`.trim());
    }
    if (notification || response.status === 202) return undefined;

    const text = await response.text();
    if (!text.trim()) return undefined;
    const message = parseResponseBody(response.headers.get("content-type") ?? "", text, id);
    if (message?.error) {
      throw new Error(`Figma MCP ${method} failed: ${message.error.message ?? "unknown error"}`);
    }
    return message?.result;
  }

  async connect(signal?: AbortSignal): Promise<void> {
    if (this.initialized) return;
    await this.send(
      "initialize",
      {
        protocolVersion: PROTOCOL_VERSION,
        capabilities: {},
        clientInfo: { name: "kiwi-code-pi", version: "1.0.0" },
      },
      signal,
      CONNECT_TIMEOUT_MS,
    );
    this.initialized = true;
    await this.send("notifications/initialized", {}, signal, CONNECT_TIMEOUT_MS, true);
  }

  async listTools(signal?: AbortSignal): Promise<MCPToolDescriptor[]> {
    const tools: MCPToolDescriptor[] = [];
    let cursor: string | undefined;
    do {
      const result = await this.send(
        "tools/list",
        cursor ? { cursor } : {},
        signal,
        CONNECT_TIMEOUT_MS,
      );
      for (const tool of result?.tools ?? []) {
        if (tool && typeof tool.name === "string") tools.push(tool);
      }
      cursor = typeof result?.nextCursor === "string" ? result.nextCursor : undefined;
    } while (cursor);
    return tools;
  }

  async callTool(
    name: string,
    args: unknown,
    signal: AbortSignal | undefined,
  ): Promise<MCPToolResult> {
    return (await this.send(
      "tools/call",
      { name, arguments: args ?? {} },
      signal,
      CALL_TIMEOUT_MS,
    )) as MCPToolResult;
  }
}

function toolParameters(tool: MCPToolDescriptor): TSchema {
  const schema = { ...(tool.inputSchema ?? {}) } as Record<string, unknown>;
  delete schema.$schema;
  if (schema.type !== "object") {
    return { type: "object", properties: {} } as unknown as TSchema;
  }
  if (!schema.properties) schema.properties = {};
  return schema as unknown as TSchema;
}

function piToolName(name: string): string {
  const normalized = name.replace(/[^a-zA-Z0-9_]/g, "_");
  return normalized.startsWith(TOOL_PREFIX) ? normalized : TOOL_PREFIX + normalized;
}

function toolLabel(tool: MCPToolDescriptor): string {
  if (tool.title?.trim()) return `Figma: ${tool.title.trim()}`;
  return `Figma: ${tool.name}`;
}

function convertContent(result: MCPToolResult): Array<
  { type: "text"; text: string } | { type: "image"; data: string; mimeType: string }
> {
  const content: Array<
    { type: "text"; text: string } | { type: "image"; data: string; mimeType: string }
  > = [];
  for (const item of result.content ?? []) {
    if (item.type === "text" && typeof item.text === "string") {
      content.push({ type: "text", text: item.text });
      continue;
    }
    if (item.type === "image" && typeof item.data === "string") {
      content.push({ type: "image", data: item.data, mimeType: item.mimeType ?? "image/png" });
      continue;
    }
    if (item.type === "resource") {
      const resource = item.resource ?? {};
      content.push({
        type: "text",
        text: resource.text ?? `[resource ${resource.uri ?? item.uri ?? "unknown"}]`,
      });
      continue;
    }
    content.push({ type: "text", text: `[unsupported ${item.type} content]` });
  }
  if (content.length === 0 && result.structuredContent !== undefined) {
    content.push({ type: "text", text: JSON.stringify(result.structuredContent, null, 2) });
  }
  if (content.length === 0) content.push({ type: "text", text: "(no content)" });
  return content;
}

export default function figmaMCPExtension(pi: ExtensionAPI): void {
  const url = process.env.KIWI_CODE_FIGMA_MCP_URL?.trim();
  if (!url) return;

  const client = new FigmaMCPClient(url);
  const registered = new Set<string>();
  let connecting: Promise<number> | undefined;

  async function discoverAndRegister(): Promise<number> {
    await client.connect();
    const tools = await client.listTools();
    for (const tool of tools) {
      const name = piToolName(tool.name);
      if (registered.has(name)) continue;
      registered.add(name);
      pi.registerTool({
        name,
        label: toolLabel(tool),
        description: tool.description?.trim()
          || `Figma MCP tool ${tool.name}. Requires an open Figma file with a selection.`,
        parameters: toolParameters(tool),
        async execute(_toolCallId, params, signal) {
          const result = await client.callTool(tool.name, params, signal);
          if (result?.isError) {
            const message = (result.content ?? [])
              .filter((item) => item.type === "text" && item.text)
              .map((item) => item.text)
              .join("\n");
            throw new Error(message || `Figma MCP tool ${tool.name} failed`);
          }
          return { content: convertContent(result ?? {}), details: { tool: tool.name } };
        },
      });
    }
    return registered.size;
  }

  function connect(): Promise<number> {
    if (!connecting) {
      connecting = discoverAndRegister().catch((reason) => {
        // Allow a later retry: a closed Figma desktop app is the common failure.
        connecting = undefined;
        throw reason;
      });
    }
    return connecting;
  }

  pi.on("session_start", async (_event, ctx: ExtensionContext) => {
    try {
      const count = await connect();
      if (count === 0) ctx.ui?.notify?.("Figma MCP exposed no tools.", "warning");
    } catch (reason) {
      const message = reason instanceof Error ? reason.message : String(reason);
      ctx.ui?.notify?.(
        `Figma MCP unavailable (${url}): ${message}. Open the Figma desktop app and run /figma-mcp to retry.`,
        "warning",
      );
    }
  });

  pi.registerCommand("figma-mcp", {
    description: "Reconnect to the Figma MCP server and load its tools",
    handler: async (_args, ctx) => {
      try {
        const count = await connect();
        ctx.ui.notify(`Figma MCP connected: ${count} tool(s) available.`, "info");
      } catch (reason) {
        const message = reason instanceof Error ? reason.message : String(reason);
        ctx.ui.notify(`Figma MCP connection failed: ${message}`, "error");
      }
    },
  });
}
