#!/usr/bin/env node
import { request, run, usage } from "./common.mjs";

run(async () => {
  const [id] = process.argv.slice(2);
  if (!id) {
    usage("Usage: stop-process.mjs <process-id>");
    return;
  }

  await request(`/${encodeURIComponent(id)}`, { method: "DELETE" });
  console.log(JSON.stringify({ stopped: id }, null, 2));
});
