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

run(async () => {
  const args = process.argv.slice(2);
  const projectId = currentProjectId(readOption(args, "--project") || "");
  const threadId = currentThreadId(readOption(args, "--thread") || "");
  rejectUnknownOptions(args);
  if (args.length > 0) {
    usage("Usage: list-processes.mjs [--thread <thread-id>] [--project <project-id>]");
    return;
  }

  print(await request(threadPath(projectId, threadId, "/processes")));
});
