---
name: kiwi-code-processes
description: Starts, inspects, interacts with, and stops long-running development processes in persistent Kiwi Code process shells. Use for dev servers, file watchers, test loops, builds, or commands that must keep running while Codex continues working.
compatibility: Requires Node.js 20+ and a Kiwi Code-managed Codex CLI session.
metadata:
  author: kiwi-code
  version: "1.1"
---

# Kiwi Code processes

Use the bundled scripts to manage long-running commands through the Kiwi Code API. Each command gets a persistent tmux shell and appears in the **Process** workspace.

The file locator shown for this skill ends in `/skills/kiwi-code-processes/SKILL.md`. Remove that suffix to determine the absolute `<plugin-root>`, then substitute that path in every command below. Never run the literal placeholder and do not replace it with a global skill path.

## Rules

- Use a process shell for servers, watchers, test loops, and other commands that must outlive one shell call.
- Do not use `&`, `nohup`, background shell jobs, or an unrelated tmux session.
- Give each process a short descriptive name and run its managed command in the foreground.
- Keep the returned process ID; later operations use it rather than a tmux index.
- Read bounded output and avoid tight polling.
- Stop processes no longer needed unless the user asks to keep them.

## Start and inspect

```bash
node "<plugin-root>/skills/kiwi-code-processes/scripts/start-process.mjs" web "npm run dev"
node "<plugin-root>/skills/kiwi-code-processes/scripts/list-processes.mjs"
node "<plugin-root>/skills/kiwi-code-processes/scripts/read-logs.mjs" <id> 200
```

The start command prints JSON with the process `id`. The optional log line count defaults to 200.

## Input, interrupt, and stop

```bash
node "<plugin-root>/skills/kiwi-code-processes/scripts/send-input.mjs" <id> "rs"
node "<plugin-root>/skills/kiwi-code-processes/scripts/interrupt-process.mjs" <id>
node "<plugin-root>/skills/kiwi-code-processes/scripts/stop-process.mjs" <id>
```

Input is followed by Enter unless `--no-enter` is supplied. Interrupt sends Ctrl-C while retaining the shell. Stop removes the process shell and history, so read final logs first.

If a helper says `KIWI_CODE_THREAD_ENDPOINT` is missing, it is not running in a Kiwi Code-managed session. Do not guess an API URL. If an ID is missing, list processes and match by name before starting a replacement.
