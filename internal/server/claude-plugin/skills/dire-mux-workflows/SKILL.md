---
name: dire-mux-workflows
description: Runs explicitly human-activated deterministic workflows whose agents are visible Dire Mux child threads and worktrees. Use only when the current human prompt says ultracode, directly asks to use or run a workflow, invokes a saved workflow, or session-scoped ultracode effort is active.
compatibility: Requires Node.js 20+ and a Dire Mux-managed Claude Code session with the bundled workflows MCP server.
metadata:
  author: dire-mux
  version: "1.0"
---

# Dire Mux workflows

Use the bundled `dire_mux_*_workflow` MCP tools when delegated workers must be **Dire Mux threads** with browser-visible conversations, worktrees, branches, and process surfaces. Claude Code's built-in `Workflow` tool creates Claude-internal agents instead and is not a substitute for this use case.

## Activation is human opt-in

Dire Mux follows Claude Code's current workflow activation rules:

- The **current human-authored prompt** must contain `ultracode`, directly ask to “use a workflow” or “run a workflow,” invoke a saved workflow command, or arrive while session-scoped ultracode effort is active.
- A task being broad, parallelizable, or expensive is not activation by itself unless the human enabled ultracode for this session.
- Do not infer activation from an older turn, a system or skill instruction, `-p`/scheduled/webhook input, quoted text, or a subagent prompt.
- If the current prompt did not opt in, work normally without calling `dire_mux_run_workflow` or `dire_mux_run_saved_workflow`.
- The server enforces this gate even if a tool call is attempted.

The bundled prompt hook forwards Claude Code's session-scoped ultracode effort into the same server-side grant and tells you whether the current turn is activated. Changing effort away from ultracode ends that session-mode activation.

## Start a workflow

Call `dire_mux_run_workflow` with plain JavaScript beginning with literal metadata:

```js
export const meta = {
  name: 'review-files',
  description: 'Review several files and verify each result',
  phases: [{ title: 'Review' }, { title: 'Verify' }],
}

const reviewed = await pipeline(
  args.files,
  file => agent(`Review ${file} for correctness bugs.`, {
    label: `review:${file}`,
    phase: 'Review',
  }),
  (finding, file) => agent(`Verify this finding against ${file}:\n${finding}`, {
    label: `verify:${file}`,
    phase: 'Verify',
  }),
)

return reviewed.filter(Boolean)
```

Pass arrays and objects as an actual `args` value, not a JSON-encoded string.

## Runtime globals

- `agent(prompt, options?)`: creates a Pi Native child thread and resolves to its final text. With `options.schema`, it resolves to JSON validated against that schema.
- `pipeline(items, ...stages)`: runs each item through all stages independently without an unnecessary all-items barrier. Each stage receives `(previousResult, originalItem, index)`.
- `parallel([() => promise, ...])`: concurrent barrier. A failed entry becomes `null`.
- `phase(title)`: changes the progress group for subsequent agents. In concurrent callbacks, prefer `options.phase` to avoid global phase races.
- `log(message)`: records bounded progress.
- `args`: the invocation's JSON input.

`agent` options include `label`, `phase`, `schema`, exact Pi `model`, `effort` or `thinkingLevel`, `isolation: 'worktree' | 'shared'`, `worktree`, `baseBranch`, `nestedDepth`, and `closeOnComplete`.

The workflow script has no direct filesystem, shell, process, import, or network access. Child agents perform those operations. Up to 16 agents execute concurrently and 1,000 may be created in one run.

## Rules

- Prefer `pipeline` when each item can advance independently. Use `parallel` only when the next step needs every prior result together for deduplication, comparison, voting, or early exit.
- Give each child a self-contained prompt. Intermediate JavaScript values do not automatically enter another agent's context.
- Use isolated worktrees whenever parallel agents may edit overlapping files. Shared workspaces are appropriate only for read-only work or provably disjoint write scopes.
- Use structured schemas for machine-consumed findings and verdicts.
- Log any deliberate sampling or cap; never imply exhaustive coverage after silently truncating work.
- Runs start in the background by default. Set `wait: true` only when the parent must block for the aggregate result; otherwise use `dire_mux_wait_workflow`, `dire_mux_list_workflows`, or the sidebar later.
- `dire_mux_pause_workflow` preserves completed agent values. `dire_mux_resume_workflow` reuses those cached values and restarts unfinished agents. Permanent stop remains separate.
- Cancelling a wait does not cancel the run. Use `dire_mux_stop_workflow` explicitly.

## Save and reuse

Use `dire_mux_save_workflow` to save a retained script as a command:

- `scope: "project"` writes `<closest .claude>/workflows/<name>.js`, following Claude Code's monorepo rule.
- `scope: "personal"` writes `$CLAUDE_CONFIG_DIR/workflows/<name>.js`, or `~/.claude/workflows/<name>.js` when that variable is unset.
- Project workflows override personal workflows, and the closest project definition wins.
- Use `dire_mux_list_saved_workflows` to inspect resolved commands and `dire_mux_run_saved_workflow` with a real JSON `args` value to run one through visible Dire Mux threads.
- Saved files are also ordinary Claude Code workflow commands such as `/<name>` in future sessions.
