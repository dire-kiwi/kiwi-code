#!/usr/bin/env node
import { print, request, run, usage } from "./common.mjs";

run(async () => {
  const [name, ...rawCommand] = process.argv.slice(2);
  if (!name || rawCommand.length === 0) {
    usage("Usage: start-process.mjs <name> <command>");
    return;
  }
  if (rawCommand[0] === "--") rawCommand.shift();
  const command = rawCommand.join(" ").trim();
  if (!command) {
    usage("A non-empty command is required.");
    return;
  }

  print(await request("", {
    method: "POST",
    body: JSON.stringify({ name, command }),
  }));
});
