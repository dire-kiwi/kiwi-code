import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { KiwiSandbox, configPaths, type EditBlock } from "../../core/src/index.ts";
import { canonicalProjectRoot, isSandboxEnabled, sandboxStatePath } from "./state.ts";

const projectRoot = canonicalProjectRoot(argument("--cwd") || process.env.CLAUDE_PROJECT_DIR || process.cwd());
const sandboxSourceRoot = fileURLToPath(new URL("../../..", import.meta.url));
const sandbox = new KiwiSandbox({
  projectRoot,
  configPaths: configPaths(projectRoot),
  protectedWritePaths: [sandboxSourceRoot, dirname(sandboxStatePath(projectRoot))],
});
await sandbox.validateConfig();

let buffer = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", (chunk) => {
  buffer += chunk;
  while (true) {
    const newline = buffer.indexOf("\n");
    if (newline < 0) break;
    const line = buffer.slice(0, newline).replace(/\r$/, "");
    buffer = buffer.slice(newline + 1);
    if (!line) continue;
    try {
      void handle(JSON.parse(line) as Request);
    } catch (error) {
      fail(undefined, -32700, `Parse error: ${error instanceof Error ? error.message : String(error)}`);
    }
  }
});

type Request = { jsonrpc: "2.0"; id?: string | number; method: string; params?: any };

async function handle(request: Request): Promise<void> {
  try {
    switch (request.method) {
      case "initialize":
        respond(request.id, {
          protocolVersion: negotiateVersion(request.params?.protocolVersion),
          capabilities: { tools: { listChanged: false } },
          serverInfo: { name: "kiwi-sandbox", version: "0.2.0" },
        });
        break;
      case "notifications/initialized":
      case "notifications/cancelled":
        break;
      case "ping":
        respond(request.id, {});
        break;
      case "tools/list":
        respond(request.id, { tools: toolDefinitions() });
        break;
      case "tools/call":
        respond(request.id, await callTool(request.params?.name, request.params?.arguments ?? {}));
        break;
      default:
        fail(request.id, -32601, `Method not found: ${request.method}`);
    }
  } catch (error) {
    fail(request.id, -32000, error instanceof Error ? error.message : String(error));
  }
}

async function callTool(name: string, args: any): Promise<Record<string, unknown>> {
  try {
    sandbox.setEnabled(isSandboxEnabled(projectRoot));
    switch (name) {
      case "sandbox_exec": {
        const chunks: Buffer[] = [];
        let outputBytes = 0;
        const outputLimit = 1024 * 1024;
        const result = await sandbox.runCommand(requiredString(args.command, "command"), {
          cwd: optionalPath(args.cwd),
          timeoutMs: typeof args.timeout_seconds === "number" ? args.timeout_seconds * 1000 : undefined,
          env: stringRecord(args.env),
          onOutput: (_stream, data) => {
            if (outputBytes >= outputLimit) return;
            const remaining = outputLimit - outputBytes;
            const selected = data.subarray(0, remaining);
            if (selected.length > 0) {
              chunks.push(selected);
              outputBytes += selected.length;
            }
          },
        });
        const output = Buffer.concat(chunks, outputBytes).toString("utf8") || `Command exited with status ${result.exitCode}`;
        return toolResult(output, result.exitCode !== 0 || result.timedOut || result.cancelled, {
          exitCode: result.exitCode, timedOut: result.timedOut, cancelled: result.cancelled, policy: result.policy,
        });
      }
      case "sandbox_read": {
        const path = resolveToolPath(requiredString(args.path, "path"));
        const raw = (await sandbox.readFile(path)).toString("utf8");
        const lines = raw.replaceAll("\r\n", "\n").split("\n");
        const start = typeof args.offset === "number" ? Math.max(0, args.offset - 1) : 0;
        const selected = lines.slice(start, typeof args.limit === "number" ? start + args.limit : undefined).join("\n");
        return toolResult(truncate(selected));
      }
      case "sandbox_write": {
        const path = resolveToolPath(requiredString(args.path, "path"));
        const content = requiredString(args.content, "content");
        await sandbox.writeFileWithParents(path, content);
        return toolResult(`Wrote ${Buffer.byteLength(content)} bytes to ${args.path}`);
      }
      case "sandbox_edit": {
        const path = resolveToolPath(requiredString(args.path, "path"));
        if (!Array.isArray(args.edits) || args.edits.length === 0) throw new Error("edits must be a non-empty array");
        const edits: EditBlock[] = args.edits.map((edit: any, index: number) => ({
          oldText: requiredString(edit?.oldText, `edits[${index}].oldText`),
          newText: requiredString(edit?.newText, `edits[${index}].newText`),
        }));
        const result = await sandbox.editFile(path, edits);
        return toolResult(`Applied ${result.replacements} edit(s) to ${args.path}`, false, result);
      }
      default:
        throw new Error(`Unknown tool: ${name}`);
    }
  } catch (error) {
    return toolResult(error instanceof Error ? error.message : String(error), true);
  }
}

function toolDefinitions(): Array<Record<string, unknown>> {
  return [
    {
      name: "sandbox_exec",
      description: "Run a shell command under macOS Seatbelt using command-specific filesystem policy.",
      inputSchema: objectSchema({
        command: { type: "string" },
        cwd: { type: "string", description: "Project-relative or absolute working directory within the project root" },
        timeout_seconds: { type: "integer", minimum: 1, maximum: 3600 },
        env: { type: "object", additionalProperties: { type: "string" } },
      }, ["command"]),
    },
    {
      name: "sandbox_read",
      description: "Read a file through a Seatbelt-isolated worker.",
      inputSchema: objectSchema({
        path: { type: "string" }, offset: { type: "integer", minimum: 1 }, limit: { type: "integer", minimum: 1 },
      }, ["path"]),
    },
    {
      name: "sandbox_write",
      description: "Write a file through a Seatbelt-isolated worker, creating parent directories.",
      inputSchema: objectSchema({ path: { type: "string" }, content: { type: "string" } }, ["path", "content"]),
    },
    {
      name: "sandbox_edit",
      description: "Apply unique exact-text replacements through Seatbelt-isolated read and write workers.",
      inputSchema: objectSchema({
        path: { type: "string" },
        edits: {
          type: "array", minItems: 1,
          items: objectSchema({ oldText: { type: "string" }, newText: { type: "string" } }, ["oldText", "newText"]),
        },
      }, ["path", "edits"]),
    },
  ];
}

function objectSchema(properties: Record<string, unknown>, required: string[]) {
  return { type: "object", properties, required, additionalProperties: false };
}

function toolResult(text: string, isError = false, structuredContent?: unknown) {
  return { content: [{ type: "text", text }], isError, ...(structuredContent === undefined ? {} : { structuredContent }) };
}

function respond(id: Request["id"], result: unknown): void {
  if (id === undefined) return;
  process.stdout.write(`${JSON.stringify({ jsonrpc: "2.0", id, result })}\n`);
}

function fail(id: Request["id"], code: number, message: string): void {
  process.stdout.write(`${JSON.stringify({ jsonrpc: "2.0", id: id ?? null, error: { code, message } })}\n`);
}

function argument(name: string): string | undefined {
  const index = process.argv.indexOf(name);
  return index >= 0 ? process.argv[index + 1] : undefined;
}

function resolveToolPath(path: string): string {
  return resolve(projectRoot, path);
}

function optionalPath(value: unknown): string | undefined {
  return typeof value === "string" ? resolveToolPath(value) : undefined;
}

function requiredString(value: unknown, name: string): string {
  if (typeof value !== "string") throw new Error(`${name} must be a string`);
  return value;
}

function stringRecord(value: unknown): Record<string, string> | undefined {
  if (value === undefined) return undefined;
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("env must be an object of strings");
  const entries = Object.entries(value);
  if (!entries.every((entry): entry is [string, string] => typeof entry[1] === "string")) {
    throw new Error("env must be an object of strings");
  }
  return Object.fromEntries(entries);
}

function truncate(value: string): string {
  const lines = value.split("\n");
  let selected = lines.slice(0, 2000).join("\n");
  let truncated = lines.length > 2000;
  if (Buffer.byteLength(selected) > 50 * 1024) {
    selected = Buffer.from(selected).subarray(0, 50 * 1024).toString("utf8");
    truncated = true;
  }
  return truncated ? `${selected}\n\n[Output truncated at 2000 lines or 50 KiB]` : selected;
}

function negotiateVersion(requested: string | undefined): string {
  return ["2025-06-18", "2025-03-26", "2024-11-05"].includes(requested ?? "") ? requested! : "2025-06-18";
}
