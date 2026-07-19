#!/usr/bin/env node
import { print, request, run, usage } from "./common.mjs";

run(async () => {
  const [id] = process.argv.slice(2);
  if (!id) {
    usage("Usage: interrupt-process.mjs <process-id>");
    return;
  }

  print(await request(`/${encodeURIComponent(id)}/interrupt`, { method: "POST" }));
});
