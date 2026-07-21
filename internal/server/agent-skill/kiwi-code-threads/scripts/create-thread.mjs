#!/usr/bin/env node
import {
  currentProjectId,
  print,
  readFlag,
  readOption,
  rejectUnknownOptions,
  request,
  run,
  usage,
} from "./common.mjs";

run(async () => {
  const args = process.argv.slice(2);
  const projectId = currentProjectId(readOption(args, "--project") || "");
  const worktree = readFlag(args, "--worktree");
  const baseBranch = readOption(args, "--base-branch") || "";
  const agent = readOption(args, "--agent") || "";
  const model = readOption(args, "--model") || "";
  const thinkingLevel = readOption(args, "--thinking") || "";
  const prompt = readOption(args, "--prompt") || "";
  rejectUnknownOptions(args);
  const title = args.join(" ").trim();
  if (baseBranch && !worktree) {
    usage("--base-branch requires --worktree.");
    return;
  }
  if (!agent && (model || thinkingLevel || prompt)) {
    usage("--model, --thinking, and --prompt require --agent.");
    return;
  }
  if (agent && !new Set(["pi-native", "pi", "claude-native", "claude", "claude-gpt"]).has(agent)) {
    usage("--agent must be pi-native, pi, claude-native, claude, or claude-gpt.");
    return;
  }

  const thread = await request(`/api/projects/${encodeURIComponent(projectId)}/threads`, {
    method: "POST",
    body: JSON.stringify({ title, worktree, baseBranch }),
  });
  if (agent) {
    try {
      await request(`/api/projects/${encodeURIComponent(projectId)}/threads/${encodeURIComponent(thread.id)}/coding-agent`, {
        method: "POST",
        body: JSON.stringify({ agent, model, thinkingLevel, prompt }),
      });
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      throw new Error(`Thread ${thread.id} was created, but its coding agent did not start: ${message}`);
    }
  }
  print(thread);
});
