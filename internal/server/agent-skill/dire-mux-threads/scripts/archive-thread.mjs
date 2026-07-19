#!/usr/bin/env node
import {
  currentProjectId,
  print,
  readFlag,
  readOption,
  rejectUnknownOptions,
  request,
  run,
  threadPath,
  usage,
} from "./common.mjs";

const help = `Usage:
  archive-thread.mjs <thread-id> [--project <project-id>]
  archive-thread.mjs <thread-id> --restore [--project <project-id>]

Archive a Dire Mux thread, or restore it with --restore. Archiving keeps the
thread and its tmux sessions but starts its configured archive-retention period.`;

run(async () => {
  const args = process.argv.slice(2);
  const longHelp = readFlag(args, "--help");
  const shortHelp = readFlag(args, "-h");
  if (longHelp || shortHelp) {
    console.log(help);
    return;
  }

  const explicitProject = readOption(args, "--project") || "";
  const restore = readFlag(args, "--restore");
  rejectUnknownOptions(args);
  const [threadId] = args;
  if (!threadId || args.length !== 1) {
    usage(help);
    return;
  }

  const projectId = currentProjectId(explicitProject);
  print(await request(threadPath(projectId, threadId), {
    method: "PATCH",
    body: JSON.stringify({ archived: !restore }),
  }));
});
