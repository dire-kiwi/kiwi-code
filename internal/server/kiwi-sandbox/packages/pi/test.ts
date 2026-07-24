import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { chmod, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const temp = await mkdtemp(join(tmpdir(), "kiwi-sandbox-pi-"));
const fake = join(temp, "sandbox-exec");
await writeFile(fake, '#!/bin/sh\nshift 2\nexec "$@"\n');
await chmod(fake, 0o700);
const child = spawn("pi", ["--mode", "rpc", "--no-session", "--no-extensions", "-e", "."], {
  cwd: new URL(".", import.meta.url),
  env: { ...process.env, HOME: temp, KIWI_SANDBOX_EXECUTABLE: fake },
  stdio: ["pipe", "pipe", "pipe"],
});
let stderr = "";
child.stderr.setEncoding("utf8");
child.stderr.on("data", (chunk) => { stderr += chunk; });
const lines = jsonLines(child.stdout);

try {
  child.stdin.write(`${JSON.stringify({ id: "bash", type: "bash", command: "printf pi-sandbox-ok" })}\n`);
  while (true) {
    const message = await lines();
    if (message.type !== "response" || message.id !== "bash") continue;
    assert.equal(message.success, true, JSON.stringify(message));
    assert.equal(message.data.output, "pi-sandbox-ok");
    assert.equal(message.data.exitCode, 0);
    break;
  }
  child.stdin.write(`${JSON.stringify({ id: "commands", type: "get_commands" })}\n`);
  while (true) {
    const message = await lines();
    if (message.type !== "response" || message.id !== "commands") continue;
    const names = message.data.commands.map((command: { name: string }) => command.name);
    assert.equal(names.includes("sandbox"), true);
    assert.equal(names.includes("kiwi-sandbox-enable"), true);
    assert.equal(names.includes("kiwi-sandbox-disable"), true);
    assert.equal(names.includes("skill:kiwi-sandbox-config"), true);
    break;
  }
  await promptCommand("disable", "/kiwi-sandbox-disable");
  await promptCommand("enable", "/kiwi-sandbox-enable");
  console.log("Pi sandbox tools, config skill, and enable/disable commands: ok");
} catch (error) {
  throw new Error(`${error instanceof Error ? error.message : String(error)}\nPi stderr:\n${stderr}`);
} finally {
  child.kill("SIGTERM");
  await new Promise((resolveExit) => child.once("exit", resolveExit));
  await rm(temp, { recursive: true, force: true });
}

async function promptCommand(id: string, message: string): Promise<void> {
  child.stdin.write(`${JSON.stringify({ id, type: "prompt", message })}\n`);
  while (true) {
    const response = await lines();
    if (response.type === "response" && response.id === id) {
      assert.equal(response.success, true, JSON.stringify(response));
      return;
    }
  }
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
