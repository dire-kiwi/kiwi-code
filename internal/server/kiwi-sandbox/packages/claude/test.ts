import assert from "node:assert/strict";
import { execFile, spawn } from "node:child_process";
import { promisify } from "node:util";
import { chmod, mkdir, mkdtemp, readFile, realpath, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { GIT_WORKTREES_PROMPT } from "../core/src/index.ts";

const execFileAsync = promisify(execFile);
const project = await mkdtemp(join(tmpdir(), "kiwi-sandbox-claude-"));
const fake = join(project, "sandbox-exec");
await writeFile(fake, '#!/bin/sh\nshift 2\nexec "$@"\n');
await chmod(fake, 0o700);
await Promise.all([mkdir(join(project, ".config")), mkdir(join(project, ".git"))]);
await writeFile(join(project, ".git", "HEAD"), "ref: refs/heads/main\n");
await writeFile(join(project, ".config", "kiwi-sandbox.json"), JSON.stringify({
  defaults: { read: ["$CWD"], write: ["$CWD"] }, commands: [], network: false,
  relatedProjects: ["../related-project"],
}));

const child = spawn("node", ["--experimental-strip-types", "./src/mcp.ts", "--cwd", project], {
  cwd: new URL(".", import.meta.url),
  env: { ...process.env, HOME: project, KIWI_SANDBOX_EXECUTABLE: fake },
  stdio: ["pipe", "pipe", "pipe"],
});
let stderr = "";
child.stderr.setEncoding("utf8");
child.stderr.on("data", (chunk) => { stderr += chunk; });
const next = jsonLines(child.stdout);
let id = 0;

try {
  const initialized = await request("initialize", { protocolVersion: "2025-03-26", capabilities: {}, clientInfo: { name: "test", version: "1" } });
  assert.equal(initialized.serverInfo.name, "kiwi-sandbox");
  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" })}\n`);
  child.stdin.write("{malformed json}\n");
  const parseError = await next();
  assert.equal(parseError.error.code, -32700);
  assert.deepEqual(await request("ping", {}), {});
  const tools = await request("tools/list", {});
  assert.deepEqual(tools.tools.map((tool: { name: string }) => tool.name), [
    "sandbox_exec", "sandbox_read", "sandbox_write", "sandbox_edit",
  ]);
  const initialExec = await call("sandbox_exec", { command: "printf claude-ok" });
  assert.equal(initialExec.content[0].text, "claude-ok");
  assert.equal(initialExec.structuredContent.policy.rule, "defaults");
  assert.equal((await call("sandbox_write", { path: "sample.txt", content: "hello world" })).isError, false);
  assert.equal((await call("sandbox_read", { path: "sample.txt" })).content[0].text, "hello world");
  assert.equal((await call("sandbox_edit", {
    path: "sample.txt", edits: [{ oldText: "world", newText: "sandbox" }],
  })).isError, false);
  assert.equal((await call("sandbox_read", { path: "sample.txt" })).content[0].text, "hello sandbox");

  const processEnv = { ...process.env, HOME: project, CLAUDE_PROJECT_DIR: project, KIWI_SANDBOX_EXECUTABLE: fake };
  const sessionContext = JSON.parse(await runHook(processEnv, {
    cwd: project, hook_event_name: "SessionStart",
  }));
  assert.equal(
    sessionContext.hookSpecificOutput.additionalContext,
    `Related Directories: ${join(await realpath(join(project, "..")), "related-project")}\n${GIT_WORKTREES_PROMPT}`,
  );
  const hookConfig = JSON.parse(await readFile(new URL("./hooks/hooks.json", import.meta.url), "utf8"));
  const preToolHook = hookConfig.hooks.PreToolUse[0];
  for (const tool of ["Bash", "Read", "Write", "Edit", "Glob", "Grep", "LS", "NotebookEdit", "MultiEdit", "WebFetch", "WebSearch"]) {
    assert.match(preToolHook.matcher, new RegExp(`(^|\\|)${tool}(\\||$)`));
  }
  assert.match(preToolHook.hooks[0].command, /--experimental-strip-types/);
  assert.match(preToolHook.hooks[0].command, /\|\| exit 2/);
  await execFileAsync("node", ["--experimental-strip-types", "./src/control.ts", "disable", project], { cwd: new URL(".", import.meta.url), env: processEnv });
  const disabledExec = await call("sandbox_exec", { command: "printf disabled-ok" });
  assert.equal(disabledExec.structuredContent.policy.rule, "disabled");
  const disabledHook = await runHook(processEnv);
  assert.equal(disabledHook, "");

  await execFileAsync("node", ["--experimental-strip-types", "./src/control.ts", "enable", project], { cwd: new URL(".", import.meta.url), env: processEnv });
  const enabledHook = JSON.parse(await runHook(processEnv));
  assert.equal(enabledHook.hookSpecificOutput.permissionDecision, "deny");
  const configSkill = await readFile(new URL("./skills/config/SKILL.md", import.meta.url), "utf8");
  assert.match(configSkill, /~\/\.config\/kiwi-sandbox\/sandbox\.json/);
  assert.match(configSkill, /<project-root>\/\.config\/kiwi-sandbox\.json/);
  assert.match(configSkill, /string command entry is an unrestricted-filesystem shorthand/);
  assert.match(configSkill, /matching command object with no `files` field grants unrestricted/);
  assert.match(configSkill, /commands\[\]\.network/);
  assert.match(configSkill, /non-empty list of globs/);
  console.log("Claude MCP tools, controls, and config skill: ok");
} catch (error) {
  throw new Error(`${error instanceof Error ? error.message : String(error)}\nMCP stderr:\n${stderr}`);
} finally {
  child.kill("SIGTERM");
  await new Promise((resolveExit) => child.once("exit", resolveExit));
  await rm(project, { recursive: true, force: true });
}

async function runHook(
  env: NodeJS.ProcessEnv,
  input: Record<string, unknown> = { cwd: project, tool_name: "Bash", tool_input: { command: "pwd" } },
): Promise<string> {
  const hook = spawn("node", ["--experimental-strip-types", "./src/hook.ts"], {
    cwd: new URL(".", import.meta.url), env, stdio: ["pipe", "pipe", "pipe"],
  });
  const stdout: Buffer[] = [];
  const stderr: Buffer[] = [];
  hook.stdout.on("data", (data) => stdout.push(data));
  hook.stderr.on("data", (data) => stderr.push(data));
  hook.stdin.end(JSON.stringify(input));
  const exitCode = await new Promise<number>((resolveExit) => hook.once("close", (code) => resolveExit(code ?? -1)));
  if (exitCode !== 0) throw new Error(Buffer.concat(stderr).toString("utf8"));
  return Buffer.concat(stdout).toString("utf8");
}

async function request(method: string, params: unknown): Promise<any> {
  const requestID = ++id;
  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: requestID, method, params })}\n`);
  while (true) {
    const response = await next();
    if (response.id !== requestID) continue;
    if (response.error) throw new Error(response.error.message);
    return response.result;
  }
}

function call(name: string, args: unknown): Promise<any> {
  return request("tools/call", { name, arguments: args });
}

function jsonLines(stream: NodeJS.ReadableStream): () => Promise<any> {
  let buffer = "";
  const queue: any[] = [];
  const waiters: Array<(value: any) => void> = [];
  stream.setEncoding?.("utf8");
  stream.on("data", (chunk) => {
    buffer += String(chunk);
    while (buffer.includes("\n")) {
      const newline = buffer.indexOf("\n");
      const line = buffer.slice(0, newline).replace(/\r$/, "");
      buffer = buffer.slice(newline + 1);
      if (!line) continue;
      const value = JSON.parse(line);
      const waiter = waiters.shift();
      if (waiter) waiter(value); else queue.push(value);
    }
  });
  return () => queue.length > 0 ? Promise.resolve(queue.shift()) : new Promise((resolveValue) => waiters.push(resolveValue));
}
