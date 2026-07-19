---
name: kiwi-code-processes
description: Starts, inspects, interacts with, and stops long-running development processes in Dire Mux process shells. Use for dev servers, file watchers, test loops, builds, or any command that must keep running while the agent continues working.
compatibility: Requires Node.js 20+ and a Dire Mux agent session with DIRE_MUX_THREAD_ENDPOINT set.
context: fork
metadata:
  author: dire-mux
  version: "1.0"
---

# Kiwi Code processes

Use the scripts in `scripts/` to manage long-running commands through the Dire Mux API. Each started command gets its own persistent tmux shell and appears in the **Process** workspace. There may be zero, one, or many process shells.

## Rules

- Use a process shell for servers, watchers, test loops, and other commands that must outlive a single tool call.
- Do **not** use `&`, `nohup`, background Bash jobs, or an unrelated tmux session.
- Give every process a short, descriptive name such as `web`, `api`, or `tests-watch`.
- Run the managed command in the foreground so its output and interrupts remain observable.
- Keep the returned process ID. All later operations use that ID, not a tmux window index.
- Read a bounded amount of output and avoid tight polling loops.
- Stop processes that are no longer needed unless the user asked to leave them running.

Set the helper directory once in a shell command when convenient:

```bash
KIWI_CODE_PROCESSES_SKILL="$HOME/.agents/skills/kiwi-code-processes"
```

## Start a process

Pass the name and command as separate quoted arguments:

```bash
node "$HOME/.agents/skills/kiwi-code-processes/scripts/start-process.mjs" web "npm run dev"
```

The script prints JSON containing the process `id`. The command is entered into a persistent login shell rooted at the thread working directory.

## List processes

```bash
node "$HOME/.agents/skills/kiwi-code-processes/scripts/list-processes.mjs"
```

Use this after compaction or whenever an ID is no longer in context.

## Read logs

```bash
node "$HOME/.agents/skills/kiwi-code-processes/scripts/read-logs.mjs" <id> 200
```

The optional line count defaults to 200 and is capped by the API. Read logs after startup to detect readiness or failure, and again after relevant changes. Wait briefly between checks when a process needs startup time.

## Send terminal input

```bash
node "$HOME/.agents/skills/kiwi-code-processes/scripts/send-input.mjs" <id> "rs"
```

Input is followed by Enter by default. Add `--no-enter` to send text without Enter. Use this only when the process intentionally accepts interactive input.

## Interrupt a foreground command

```bash
node "$HOME/.agents/skills/kiwi-code-processes/scripts/interrupt-process.mjs" <id>
```

This sends Ctrl-C. The shell remains available and its output remains readable.

## Stop and remove a process shell

```bash
node "$HOME/.agents/skills/kiwi-code-processes/scripts/stop-process.mjs" <id>
```

Stopping removes the tmux window and its captured history. Read any needed final logs first.

## Recovery

If a helper reports that `DIRE_MUX_THREAD_ENDPOINT` is missing, it is not running inside a Dire Mux-managed agent session. Do not guess an API URL. If an ID is not found, list processes and match by name before deciding whether to start a replacement.
