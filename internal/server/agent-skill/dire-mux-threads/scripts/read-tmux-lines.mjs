#!/usr/bin/env node
import {
  currentProjectId,
  currentThreadId,
  print,
  readOption,
  rejectUnknownOptions,
  request,
  run,
  threadPath,
  usage,
} from "./common.mjs";

const supportedTargets = new Set(["pi", "claude", "claude-gpt", "terminal", "shell", "nvim", "lazygit"]);

run(async () => {
  const args = process.argv.slice(2);
  const projectId = currentProjectId(readOption(args, "--project") || "");
  const threadId = currentThreadId(readOption(args, "--thread") || "");
  const rawWindow = readOption(args, "--window") || "";
  rejectUnknownOptions(args);

  const [target, rawLines = "200"] = args;
  const lines = Number(rawLines);
  if (!target || args.length > 2 || !Number.isInteger(lines) || lines < 1 || lines > 5000) {
    usage("Usage: read-tmux-lines.mjs <pi|claude|claude-gpt|terminal|nvim|lazygit|process:ID> [lines: 1-5000] [--thread <thread-id>] [--project <project-id>] [--window <shell-index>]");
    return;
  }

  const query = new URLSearchParams({ lines: String(lines) });
  if (target.startsWith("process:")) {
    const processId = target.slice("process:".length).trim();
    if (!processId) {
      usage("A process target must include its ID, for example process:abc123.");
      return;
    }
    query.set("tool", "process");
    query.set("processId", processId);
  } else {
    if (!supportedTargets.has(target)) {
      usage(`Unknown tmux target: ${target}`);
      return;
    }
    if (target === "pi" || target === "claude" || target === "claude-gpt") {
      query.set("tool", "pi");
      query.set("agent", target);
    } else {
      query.set("tool", target === "shell" ? "terminal" : target);
    }
  }

  if (rawWindow) {
    if (query.get("tool") !== "terminal" || !/^\d+$/.test(rawWindow)) {
      usage("--window must be a non-negative shell window index and can only be used with terminal or shell.");
      return;
    }
    query.set("window", rawWindow);
  }

  print(await request(`${threadPath(projectId, threadId, "/terminal/lines")}?${query}`));
});
