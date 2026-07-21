---
name: kiwi-code-threads
description: Creates, lists, renames, archives, restores, inspects, and closes Kiwi Code threads, including reading bounded tmux output from Pi, Claude Code, shell, tool, and process panes. Use when coordinating work across Kiwi Code threads or checking another thread's agent or process output.
compatibility: Requires Node.js 20+ and a Kiwi Code agent session with KIWI_CODE_THREAD_ENDPOINT set.
metadata:
  author: kiwi-code
  version: "1.0"
---

# Kiwi Code threads

Use the dependency-free scripts in `scripts/` to manage threads through the Kiwi Code API. They default to the current project and thread from `KIWI_CODE_PROJECT_ID` and `KIWI_CODE_THREAD_ID`; pass explicit IDs when operating elsewhere.

## Rules

- Never create a main thread unless the user explicitly asks to create, start, or open a new thread. Do not infer permission from a broad task, a desire for parallelism, or an opportunity to delegate work; use the current thread unless the user specifically requests another main thread.
- List threads first when a target is ambiguous. Use immutable project and thread IDs for mutations, never a title alone.
- When explicitly asked to create a thread, create a normal thread unless the user asks for Git isolation. For a worktree thread, pass `--worktree` and optionally `--base-branch`.
- Do not start a coding agent merely because the user requested a new thread. Only pass `--agent` when the user also explicitly asks to start an agent in it or supplies an initial task for that new thread; `--model`, `--thinking`, and `--prompt` configure that new agent process.
- Archive a completed thread when it should leave the active list but remain recoverable. Archiving keeps its record and tmux sessions, but starts the configured archived-thread retention period. Restore it to return it to the active list.
- Treat closing as destructive: it removes the thread record and stops both of its persistent tmux sessions, including its coding agents and process shells. It does not immediately delete an existing worktree, branch, or project files; a clean managed worktree may be removed later according to automatic cleanup settings.
- Never close the current thread unless the user explicitly requests it. The helper refuses by default because closing it terminates this agent; only then use `--allow-current`.
- Read a bounded amount of tmux output. Avoid tight polling loops and increase the line count only when needed.
- Reading is observational: it does not create a missing tmux session or start an agent. A newly-created thread has no Pi output until its coding-agent workspace has been opened.
- Do not construct tmux session names, kill sessions directly, or mutate Kiwi Code's project data files. Use these helpers so the API can persist changes, publish events, and perform cleanup.
- Use the separate `kiwi-code-processes` skill to start, interrupt, send input to, or stop processes in the current thread.

Set the helper directory once when convenient:

```bash
KIWI_CODE_THREADS_SKILL="$HOME/.agents/skills/kiwi-code-threads"
```

## List threads

List the current project's threads:

```bash
node "$HOME/.agents/skills/kiwi-code-threads/scripts/list-threads.mjs"
```

List another project or every project:

```bash
node "$HOME/.agents/skills/kiwi-code-threads/scripts/list-threads.mjs" --project <project-id>
node "$HOME/.agents/skills/kiwi-code-threads/scripts/list-threads.mjs" --all
```

The output includes each thread's ID, title, working directory, worktree state, branch when present, and `archivedAt` timestamp when archived.

## Create a thread

Create a normal thread in the current project:

```bash
node "$HOME/.agents/skills/kiwi-code-threads/scripts/create-thread.mjs" "Investigate cache misses"
```

Create an isolated worktree thread from a local base branch and immediately start Pi Native with its first task:

```bash
node "$HOME/.agents/skills/kiwi-code-threads/scripts/create-thread.mjs" "Fix cache misses" \
  --worktree --base-branch main \
  --agent pi-native --model openai-codex/gpt-5.6-sol --thinking high \
  --prompt "Find and fix the cache misses, then run the relevant tests."
```

`--agent` accepts `pi-native`, `pi`, `claude-native`, `claude`, or `claude-gpt`. The `pi` and `claude` values start terminal presentations; `claude-gpt` is terminal-only. Model IDs and thinking levels must be supported by the selected agent. `--model`, `--thinking`, and `--prompt` require `--agent`. Omitting `--prompt` starts the selected agent without sending a task.

Add `--project <project-id>` to create the thread in another project. The title is optional; without one, Kiwi Code uses `New thread` and its first coding-agent prompt can name it automatically. If agent startup fails, the helper reports the ID of the thread that was still created.

## Rename a thread

```bash
node "$HOME/.agents/skills/kiwi-code-threads/scripts/rename-thread.mjs" <thread-id> "New title"
```

Add `--project <project-id>` for another project. This is a manual rename. It changes the managed worktree branch only when Kiwi Code performs the first automatic title generation, not for later manual renames.

## Archive or restore a thread

After confirming the exact thread ID, archive it:

```bash
node "$HOME/.agents/skills/kiwi-code-threads/scripts/archive-thread.mjs" <thread-id>
```

Restore an archived thread:

```bash
node "$HOME/.agents/skills/kiwi-code-threads/scripts/archive-thread.mjs" <thread-id> --restore
```

Add `--project <project-id>` when the thread belongs to another project. Archiving moves the thread beneath the project's **Show more** section without stopping its tmux sessions; restoring returns it to the active list. Both operations are safe to repeat. Run the helper with `--help` for its command-line summary.

## Read tmux lines

Read recent output from the current thread's Pi pane:

```bash
node "$HOME/.agents/skills/kiwi-code-threads/scripts/read-tmux-lines.mjs" pi 200
```

Read another thread's Claude Code pane, its CLIProxyAPI-backed GPT pane, or one of its process shells:

```bash
node "$HOME/.agents/skills/kiwi-code-threads/scripts/read-tmux-lines.mjs" claude 300 --thread <thread-id>
node "$HOME/.agents/skills/kiwi-code-threads/scripts/read-tmux-lines.mjs" claude-gpt 300 --thread <thread-id>
node "$HOME/.agents/skills/kiwi-code-threads/scripts/list-processes.mjs" --thread <thread-id>
node "$HOME/.agents/skills/kiwi-code-threads/scripts/read-tmux-lines.mjs" process:<process-id> 300 --thread <thread-id>
```

Supported targets are `pi`, `claude`, `claude-gpt`, `terminal` (or `shell`), `nvim`, `lazygit`, and `process:<process-id>`. Use `--window <index>` with `terminal` or `shell` to select a shell window; otherwise the active shell window is captured. Add `--project <project-id>` when the thread belongs to another project. Line counts default to 200 and must be between 1 and 5000.

ANSI control sequences may be present because the output comes from tmux's pane history. A not-found response usually means that workspace has not been opened, the selected agent is not running, or the process ID is stale.

## Close a thread

After confirming the exact ID and that the work is no longer needed:

```bash
node "$HOME/.agents/skills/kiwi-code-threads/scripts/close-thread.mjs" <thread-id>
```

Add `--project <project-id>` for another project. If the user explicitly asks this agent to close its own thread, add `--allow-current` and expect the command or response to be cut off as Kiwi Code terminates the session.

## Recovery

If a helper says `KIWI_CODE_THREAD_ENDPOINT` is missing, it is not running inside a Kiwi Code-managed coding-agent session. Do not guess the API address. If an ID is stale, list threads or processes again before taking further action.
