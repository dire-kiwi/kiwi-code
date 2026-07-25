import { configPaths, loadConfig, sandboxSystemPrompt } from "../../core/src/index.ts";
import { canonicalProjectRoot, isSandboxEnabled } from "./state.ts";

const failClosedTimer = setTimeout(() => {
  process.stderr.write("Kiwi Sandbox hook timed out\n");
  process.exit(2);
}, 8_000);

try {
  const chunks: Buffer[] = [];
  for await (const chunk of process.stdin) chunks.push(Buffer.from(chunk));
  const event = JSON.parse(Buffer.concat(chunks).toString("utf8")) as { cwd?: string; hook_event_name?: string };
  const projectRoot = canonicalProjectRoot(process.env.CLAUDE_PROJECT_DIR || event.cwd || process.cwd());
  if (event.hook_event_name === "SessionStart") {
    const context = await sandboxSystemPrompt(await loadConfig(configPaths(projectRoot)), projectRoot);
    if (context) {
      process.stdout.write(JSON.stringify({
        hookSpecificOutput: {
          hookEventName: "SessionStart",
          additionalContext: context,
        },
      }));
    }
  } else if (isSandboxEnabled(projectRoot)) {
    process.stdout.write(JSON.stringify({
      hookSpecificOutput: {
        hookEventName: "PreToolUse",
        permissionDecision: "deny",
        permissionDecisionReason: "Built-in filesystem and network tools are disabled by Kiwi Sandbox. Use sandbox_exec, sandbox_read, sandbox_write, or sandbox_edit from the kiwi-sandbox MCP server.",
      },
    }));
  }
} finally {
  clearTimeout(failClosedTimer);
}
