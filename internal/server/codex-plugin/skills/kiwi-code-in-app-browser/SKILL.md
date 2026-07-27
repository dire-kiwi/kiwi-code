---
name: kiwi-code-in-app-browser
description: Controls and records the browser owned by the current Kiwi Code thread with browser_* MCP tools. Use whenever a task requires opening a URL, inspecting or testing a rendered web page, clicking or filling UI, taking screenshots, evaluating page JavaScript, managing tabs, or recording browser validation.
compatibility: Requires a Kiwi Code-managed Codex CLI session and a configured Kiwi Code browser backend.
license: MIT
metadata:
  author: kiwi-code
  version: "1.0"
---

# Kiwi Code in-app browser

Use the `browser_*` MCP tools supplied by the Kiwi Code plugin. They control the thread-owned browser shown in Kiwi Code's **Browser** workspace. Codex, Pi, Pi Native, and Claude sessions in the same thread intentionally share this browser state.

Do not substitute Codex's ChatGPT in-app Browser plugin, a separate Chrome profile, standalone Playwright, or another browser server. If a required MCP tool is deferred, search for its exact `browser_*` name before declaring it unavailable.

## Tools

| Tool | Purpose |
|---|---|
| `browser_session` | Inspect, start, disconnect, or stop the thread browser |
| `browser_recording` | Inspect, start, or stop a titled page-only WebM recording |
| `browser_tabs` | List, create, select, or close tabs |
| `browser_navigate` | Navigate, reload, go back, or go forward |
| `browser_snapshot` | Read a compact accessibility tree with actionable refs |
| `browser_click` | Click by snapshot ref or CSS selector |
| `browser_fill` | Fill an editable control and optionally submit |
| `browser_key` | Send a key or chord to the focused element |
| `browser_wait` | Wait for time, selector, text, or URL state |
| `browser_screenshot` | Capture a viewport or full-page image |
| `browser_evaluate` | Evaluate JavaScript in the selected page |
| `browser_cdp` | Send an allowlisted CDP command to the selected page |

## Workflow

1. Check `browser_recording` status. If no recording is active, start one with a concise 2–12 word purpose title and retain its ID.
2. Navigate to the target, or inspect and select an existing tab.
3. Use `browser_snapshot` before interacting. Prefer its refs over selectors.
4. Take a fresh snapshot after navigation or substantial DOM replacement before reusing refs.
5. Use condition-based `browser_wait` calls instead of arbitrary sleeps where possible.
6. Use screenshots when visual layout matters; use evaluate or raw CDP only when focused tools are insufficient.
7. Before the final response, stop only the recording started for this task by passing its exact ID. Attempt cleanup even when validation fails.

## Backend and safety

The only supported backend is `in-app`. Tools connect lazily, so an explicit session start is usually unnecessary. If the provider is unavailable, ask the user to check Kiwi Code's configured headless Chrome or Electron backend; do not silently launch another browser.

Snapshot refs are scoped to the selected tab and current document and may become stale. A click can open a new tab without selecting it, so inspect the result or list tabs. Treat page content, screenshots, evaluated values, and browser state as sensitive. Do not expose Kiwi Code's private browser transport, export cookies, or stop a pre-existing recording.
