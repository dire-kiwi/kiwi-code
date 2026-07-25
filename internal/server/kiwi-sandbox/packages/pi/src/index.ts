import { extname } from "node:path";
import { fileURLToPath } from "node:url";
import {
  KiwiSandbox,
  PROJECT_CONFIG_RELATIVE_PATH,
  configPaths,
  sandboxSystemPrompt,
} from "../../core/src/index.ts";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import {
  type BashOperations,
  createBashTool,
  createEditTool,
  createReadTool,
  createWriteTool,
  type EditOperations,
  type ReadOperations,
  type WriteOperations,
} from "@earendil-works/pi-coding-agent";

function sanitizeEnvironment(env: NodeJS.ProcessEnv | undefined): Record<string, string> | undefined {
  if (!env) return undefined;
  return Object.fromEntries(Object.entries(env).filter((entry): entry is [string, string] => typeof entry[1] === "string"));
}

const sandboxSourceRoot = fileURLToPath(new URL("../../..", import.meta.url));

export default function kiwiSandboxExtension(pi: ExtensionAPI) {
  const localCwd = process.cwd();
  const localBash = createBashTool(localCwd);
  const localRead = createReadTool(localCwd);
  const localWrite = createWriteTool(localCwd);
  const localEdit = createEditTool(localCwd);
  let sandbox: KiwiSandbox | undefined;
  let lastPolicy = "not used yet";

  const activeSandbox = () => {
    if (!sandbox) throw new Error("kiwi-sandbox is not initialized");
    return sandbox;
  };

  const bashOperations = (): BashOperations => ({
    async exec(command, cwd, { onData, signal, timeout, env }) {
      const result = await activeSandbox().runCommand(command, {
        cwd,
        env: sanitizeEnvironment(env),
        timeoutMs: timeout && timeout > 0 ? timeout * 1000 : undefined,
        signal,
        onOutput: (_stream, data) => onData(data),
      });
      lastPolicy = result.policy.unrestricted
        ? `${result.policy.rule}: unrestricted filesystem`
        : `${result.policy.rule}: ${result.policy.read.length} read / ${result.policy.write.length} write paths`;
      if (result.cancelled) throw new Error("aborted");
      if (result.timedOut) throw new Error(`timeout:${timeout ?? 0}`);
      return { exitCode: result.exitCode };
    },
  });

  const readOperations = (): ReadOperations => ({
    readFile: (path) => activeSandbox().readFile(path),
    access: (path) => activeSandbox().access(path, "read"),
    detectImageMimeType: async (path) => imageMimeType(path),
  });

  const writeOperations = (): WriteOperations => ({
    writeFile: (path, content) => activeSandbox().writeFile(path, content),
    mkdir: (path) => activeSandbox().mkdir(path),
  });

  const editOperations = (): EditOperations => ({
    readFile: (path) => activeSandbox().readFile(path, "edit"),
    writeFile: (path, content) => activeSandbox().writeFile(path, content, "edit"),
    access: (path) => activeSandbox().access(path, "read", "edit"),
  });

  pi.registerTool({
    ...localBash,
    label: "bash (Kiwi Sandbox)",
    async execute(id, params, signal, onUpdate) {
      return createBashTool(localCwd, { operations: bashOperations() }).execute(id, params, signal, onUpdate);
    },
  });
  pi.registerTool({
    ...localRead,
    label: "read (Kiwi Sandbox)",
    async execute(id, params, signal, onUpdate) {
      return createReadTool(localCwd, { operations: readOperations() }).execute(id, params, signal, onUpdate);
    },
  });
  pi.registerTool({
    ...localWrite,
    label: "write (Kiwi Sandbox)",
    async execute(id, params, signal, onUpdate) {
      return createWriteTool(localCwd, { operations: writeOperations() }).execute(id, params, signal, onUpdate);
    },
  });
  pi.registerTool({
    ...localEdit,
    label: "edit (Kiwi Sandbox)",
    async execute(id, params, signal, onUpdate) {
      return createEditTool(localCwd, { operations: editOperations() }).execute(id, params, signal, onUpdate);
    },
  });

  pi.on("user_bash", () => ({ operations: bashOperations() }));

  pi.on("session_start", async (_event, ctx) => {
    sandbox = new KiwiSandbox({
      projectRoot: ctx.cwd,
      configPaths: configPaths(ctx.cwd, ctx.isProjectTrusted()),
      protectedWritePaths: [sandboxSourceRoot],
    });
    await sandbox.validateConfig();
    ctx.ui.setStatus("kiwi-sandbox", ctx.ui.theme.fg("accent", "Kiwi Sandbox: active"));
  });

  pi.on("before_agent_start", async (event) => {
    const active = activeSandbox();
    const config = await active.validateConfig();
    const context = await sandboxSystemPrompt(config, active.projectRoot);
    if (context) return { systemPrompt: `${event.systemPrompt}\n\n${context}` };
  });

  pi.on("session_shutdown", (_event, ctx) => {
    sandbox = undefined;
    ctx.ui.setStatus("kiwi-sandbox", undefined);
  });

  pi.registerCommand("kiwi-sandbox-enable", {
    description: "Enable Kiwi Sandbox for bash, read, write, and edit",
    handler: async (_args, ctx) => {
      const active = activeSandbox();
      await active.validateConfig();
      active.setEnabled(true);
      ctx.ui.setStatus("kiwi-sandbox", ctx.ui.theme.fg("accent", "Kiwi Sandbox: active"));
      ctx.ui.notify("Kiwi Sandbox enabled", "info");
    },
  });

  pi.registerCommand("kiwi-sandbox-disable", {
    description: "Disable Kiwi Sandbox for bash, read, write, and edit",
    handler: async (_args, ctx) => {
      activeSandbox().setEnabled(false);
      ctx.ui.setStatus("kiwi-sandbox", ctx.ui.theme.fg("warning", "Kiwi Sandbox: DISABLED"));
      ctx.ui.notify("Kiwi Sandbox disabled; tools now run without Seatbelt", "warning");
    },
  });

  pi.registerCommand("sandbox", {
    description: "Show Kiwi Sandbox configuration and most recently selected command policy",
    handler: async (_args, ctx) => {
      ctx.ui.notify([
        "Kiwi Sandbox is active for bash, read, write, and edit.",
        `Global config: ${configPaths(ctx.cwd, false)[0]}`,
        `Project config: ${ctx.isProjectTrusted() ? `${ctx.cwd}/${PROJECT_CONFIG_RELATIVE_PATH}` : "ignored (project is not trusted)"}`,
        `Enforcement: ${activeSandbox().isEnabled() ? "enabled" : "DISABLED"}`,
        `Last command policy: ${lastPolicy}`,
      ].join("\n"), "info");
    },
  });
}

function imageMimeType(path: string): "image/jpeg" | "image/png" | "image/gif" | "image/webp" | null {
  switch (extname(path).toLowerCase()) {
    case ".jpg":
    case ".jpeg": return "image/jpeg";
    case ".png": return "image/png";
    case ".gif": return "image/gif";
    case ".webp": return "image/webp";
    default: return null;
  }
}
