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
  rejectUnknownOptions(args);
  const title = args.join(" ").trim();
  if (baseBranch && !worktree) {
    usage("--base-branch requires --worktree.");
    return;
  }

  print(await request(`/api/projects/${encodeURIComponent(projectId)}/threads`, {
    method: "POST",
    body: JSON.stringify({ title, worktree, baseBranch }),
  }));
});
