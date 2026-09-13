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
  settle-thread.mjs <thread-id> [--project <project-id>]
  settle-thread.mjs <thread-id> --unsettle [--project <project-id>]

Settle a Kiwi Code thread, or unsettle it with --unsettle. Settling closes its
sessions and retains its saved conversations and workspace.`;

run(async () => {
  const args = process.argv.slice(2);
  const longHelp = readFlag(args, "--help");
  const shortHelp = readFlag(args, "-h");
  if (longHelp || shortHelp) {
    console.log(help);
    return;
  }

  const explicitProject = readOption(args, "--project") || "";
  const unsettle = readFlag(args, "--unsettle");
  rejectUnknownOptions(args);
  const [threadId] = args;
  if (!threadId || args.length !== 1) {
    usage(help);
    return;
  }

  const projectId = currentProjectId(explicitProject);
  print(await request(threadPath(projectId, threadId), {
    method: "PATCH",
    body: JSON.stringify({ settled: !unsettle }),
  }));
});
