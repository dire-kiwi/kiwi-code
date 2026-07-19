#!/usr/bin/env node
import { print, request, run, usage } from "./common.mjs";

run(async () => {
  const [id, rawLines = "200"] = process.argv.slice(2);
  const lines = Number(rawLines);
  if (!id || !Number.isInteger(lines) || lines < 1 || lines > 5000) {
    usage("Usage: read-logs.mjs <process-id> [lines: 1-5000]");
    return;
  }

  print(await request(`/${encodeURIComponent(id)}/logs?lines=${lines}`));
});
