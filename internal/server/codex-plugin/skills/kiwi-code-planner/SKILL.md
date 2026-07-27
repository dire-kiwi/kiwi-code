---
name: kiwi-code-planner
description: Creates or revises an implementation-ready Markdown plan and publishes it to the current Kiwi Code thread. Use only when the user asks to plan; do not use it to execute a saved plan.
compatibility: Requires a Kiwi Code-managed Codex CLI session with the bundled plan MCP tools.
metadata:
  author: kiwi-code
  version: "1.0"
---

# Kiwi Code planner

Create a read-only implementation plan for the user's request, then retain it in Kiwi Code.

## Rules

- Plan only. Inspect the repository and relevant documentation, but do not edit project files, run destructive commands, implement the change, or commit.
- Resolve important uncertainty through read-only investigation instead of guessing. State uncertainty that cannot be resolved.
- Keep the plan scoped and standalone so another agent can execute it without this conversation.
- Name concrete files, symbols, data flows, compatibility constraints, tests, and validation where the repository supports that specificity.
- Do not save the plan in the workspace; Kiwi Code is the durable copy.

## Publish

Find the exact deferred MCP tool `publish_thread_plan` if it is not already visible. Call it exactly once with:

- `title`: a concise title of at most 120 characters;
- `content`: the complete standalone Markdown plan.

Do not claim the plan was saved unless the tool returns a plan ID. After success, report the title and ID and say it is available in the Thread details sidebar.

When the user instead asks to execute a saved plan, do not create another plan. Use `list_thread_plans` if the target is ambiguous, call `download_thread_plan`, and then carry out the downloaded plan in the main conversation.
