#!/usr/bin/env node
import {
  currentProjectId,
  currentThreadId,
  print,
  readFlag,
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
  const allowCurrent = readFlag(args, "--allow-current");
  rejectUnknownOptions(args);
  const [threadId] = args;
  if (!threadId || args.length !== 1) {
    usage("Usage: close-thread.mjs <thread-id> [--project <project-id>] [--allow-current]");
    return;
  }

  let activeThreadId = "";
  try {
    activeThreadId = currentThreadId();
  } catch {
    // A cross-project caller may not have a current thread context.
  }
  if (!allowCurrent && projectId === currentProjectId() && threadId === activeThreadId) {
    throw new Error("Refusing to close the current thread. Pass --allow-current only when the user explicitly requested it; this agent session will be terminated.");
  }

  await request(threadPath(projectId, threadId), { method: "DELETE" });
  print({ closed: threadId, projectId });
});
