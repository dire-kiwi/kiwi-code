#!/usr/bin/env node
import { print, request, run, usage } from "./common.mjs";

run(async () => {
  const args = process.argv.slice(2);
  const id = args.shift();
  const noEnterIndex = args.indexOf("--no-enter");
  const enter = noEnterIndex === -1;
  if (noEnterIndex !== -1) args.splice(noEnterIndex, 1);
  const data = args.join(" ");
  if (!id || !data) {
    usage("Usage: send-input.mjs <process-id> <text> [--no-enter]");
    return;
  }

  print(await request(`/${encodeURIComponent(id)}/input`, {
    method: "POST",
    body: JSON.stringify({ data, enter }),
  }));
});
