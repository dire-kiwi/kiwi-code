---
name: dire-mux-threads
description: Creates, lists, renames, archives, restores, inspects, and closes Dire Mux threads, including reading bounded tmux output from Pi, Claude Code, shell, tool, and process panes. Use when coordinating work across Dire Mux threads or checking another thread's agent or process output.
compatibility: Requires Node.js 20+ and a Dire Mux agent session with DIRE_MUX_THREAD_ENDPOINT set.
metadata:
  author: dire-mux
  version: "1.0"
---

# Dire Mux threads

Use the dependency-free scripts in `scripts/` to manage threads through the Dire Mux API. They default to the current project and thread from `DIRE_MUX_PROJECT_ID` and `DIRE_MUX_THREAD_ID`; pass explicit IDs when operating elsewhere.

## Rules

- List threads first when a target is ambiguous. Use immutable project and thread IDs for mutations, never a title alone.
- Create a normal thread unless the user asks for Git isolation. For a worktree thread, pass `--worktree` and optionally `--base-branch`.
- Archive a completed thread when it should leave the active list but remain recoverable. Archiving keeps its record and tmux sessions, but starts the configured archived-thread retention period. Restore it to return it to the active list.
- Treat closing as destructive: it removes the thread record and stops both of its persistent tmux sessions, including its coding agents and process shells. It does not immediately delete an existing worktree, branch, or project files; a clean managed worktree may be removed later according to automatic cleanup settings.
- Never close the current thread unless the user explicitly requests it. The helper refuses by default because closing it terminates this agent; only then use `--allow-current`.
- Read a bounded amount of tmux output. Avoid tight polling loops and increase the line count only when needed.
- Reading is observational: it does not create a missing tmux session or start an agent. A newly-created thread has no Pi output until its coding-agent workspace has been opened.
- Do not construct tmux session names, kill sessions directly, or mutate Dire Mux's project data files. Use these helpers so the API can persist changes, publish events, and perform cleanup.
- Use the separate `kiwi-code-processes` skill to start, interrupt, send input to, or stop processes in the current thread.

Set the helper directory once when convenient:

```bash
DIRE_MUX_THREADS_SKILL="$HOME/.agents/skills/dire-mux-threads"
```

## List threads

List the current project's threads:

```bash
node "$HOME/.agents/skills/dire-mux-threads/scripts/list-threads.mjs"
```

List another project or every project:

```bash
node "$HOME/.agents/skills/dire-mux-threads/scripts/list-threads.mjs" --project <project-id>
node "$HOME/.agents/skills/dire-mux-threads/scripts/list-threads.mjs" --all
```

The output includes each thread's ID, title, working directory, worktree state, branch when present, and `archivedAt` timestamp when archived.

## Create a thread

Create a normal thread in the current project:

```bash
node "$HOME/.agents/skills/dire-mux-threads/scripts/create-thread.mjs" "Investigate cache misses"
```

Create an isolated worktree thread from a local base branch:

```bash
node "$HOME/.agents/skills/dire-mux-threads/scripts/create-thread.mjs" "Fix cache misses" --worktree --base-branch main
```

Add `--project <project-id>` to create it in another project. The title is optional; without one, Dire Mux uses `New thread` and its first coding-agent prompt can name it automatically.

## Rename a thread

```bash
node "$HOME/.agents/skills/dire-mux-threads/scripts/rename-thread.mjs" <thread-id> "New title"
```

Add `--project <project-id>` for another project. This is a manual rename. It changes the managed worktree branch only when Dire Mux performs the first automatic title generation, not for later manual renames.

## Archive or restore a thread

After confirming the exact thread ID, archive it:

```bash
node "$HOME/.agents/skills/dire-mux-threads/scripts/archive-thread.mjs" <thread-id>
```

Restore an archived thread:

```bash
node "$HOME/.agents/skills/dire-mux-threads/scripts/archive-thread.mjs" <thread-id> --restore
```

Add `--project <project-id>` when the thread belongs to another project. Archiving moves the thread beneath the project's **Show more** section without stopping its tmux sessions; restoring returns it to the active list. Both operations are safe to repeat. Run the helper with `--help` for its command-line summary.

## Read tmux lines

Read recent output from the current thread's Pi pane:

```bash
node "$HOME/.agents/skills/dire-mux-threads/scripts/read-tmux-lines.mjs" pi 200
```

Read another thread's Claude Code pane, its CLIProxyAPI-backed GPT pane, or one of its process shells:

```bash
node "$HOME/.agents/skills/dire-mux-threads/scripts/read-tmux-lines.mjs" claude 300 --thread <thread-id>
node "$HOME/.agents/skills/dire-mux-threads/scripts/read-tmux-lines.mjs" claude-gpt 300 --thread <thread-id>
node "$HOME/.agents/skills/dire-mux-threads/scripts/list-processes.mjs" --thread <thread-id>
node "$HOME/.agents/skills/dire-mux-threads/scripts/read-tmux-lines.mjs" process:<process-id> 300 --thread <thread-id>
```

Supported targets are `pi`, `claude`, `claude-gpt`, `terminal` (or `shell`), `nvim`, `lazygit`, and `process:<process-id>`. Use `--window <index>` with `terminal` or `shell` to select a shell window; otherwise the active shell window is captured. Add `--project <project-id>` when the thread belongs to another project. Line counts default to 200 and must be between 1 and 5000.

ANSI control sequences may be present because the output comes from tmux's pane history. A not-found response usually means that workspace has not been opened, the selected agent is not running, or the process ID is stale.

## Close a thread

After confirming the exact ID and that the work is no longer needed:

```bash
node "$HOME/.agents/skills/dire-mux-threads/scripts/close-thread.mjs" <thread-id>
```

Add `--project <project-id>` for another project. If the user explicitly asks this agent to close its own thread, add `--allow-current` and expect the command or response to be cut off as Dire Mux terminates the session.

## Recovery

If a helper says `DIRE_MUX_THREAD_ENDPOINT` is missing, it is not running inside a Dire Mux-managed coding-agent session. Do not guess the API address. If an ID is stale, list threads or processes again before taking further action.
