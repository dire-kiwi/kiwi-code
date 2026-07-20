#!/usr/bin/env node
import { print, request, run, usage } from "./common.mjs";

run(async () => {
  const [id, ...values] = process.argv.slice(2);
  if (!id || values.length === 0) {
    usage("Usage: update-process.mjs <process-id> <url> [url ...]\n       update-process.mjs <process-id> --clear");
    return;
  }
  const webServers = values.length === 1 && values[0] === "--clear" ? [] : values;
  if (webServers.some((value) => value === "--clear")) {
    usage("--clear cannot be combined with web server URLs.");
    return;
  }

  print(await request(`/${encodeURIComponent(id)}`, {
    method: "PATCH",
    body: JSON.stringify({ webServers }),
  }));
});
