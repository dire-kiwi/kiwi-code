import { canonicalProjectRoot, setSandboxEnabled } from "./state.ts";

const action = process.argv[2];
const projectRoot = canonicalProjectRoot(process.argv[3] || process.env.CLAUDE_PROJECT_DIR || process.cwd());
if (action !== "enable" && action !== "disable") {
  throw new Error("Usage: kiwi-sandbox-control <enable|disable> <project-root>");
}
const enabled = action === "enable";
await setSandboxEnabled(projectRoot, enabled);
process.stdout.write(enabled
  ? `Kiwi Sandbox enabled for ${projectRoot}`
  : `Kiwi Sandbox DISABLED for ${projectRoot}; built-in command, filesystem, and network tools may run without Seatbelt`);
