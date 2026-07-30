---
name: kiwi-code-in-app-browser
description: Controls Kiwi Code's in-app browser with the browser_* MCP tools. Invoke this skill BEFORE the first browser_* tool call in a task, and never call browser_navigate, browser_snapshot, browser_click, browser_fill, browser_key, browser_wait, browser_screenshot, browser_evaluate, browser_cdp, browser_tabs, or browser_session without it. Triggers whenever a task requires opening a URL or web page, browsing or checking a website, interacting with or verifying a running web app or dev server in the browser, inspecting rendered pages, filling forms, clicking UI, taking screenshots of a page, evaluating JavaScript in a page, managing tabs, or sending raw CDP commands.
compatibility: Requires a Kiwi Code-managed Claude Code session and a configured Kiwi Code in-app browser provider (server-managed headless Chrome or Electron).
license: MIT
context: fork
metadata:
  author: kiwi-code
  version: "1.0"
---

# Kiwi Code in-app browser control

This skill is the entry point for all browser work. Every `browser_*` tool call belongs inside this forked context; if a task in the parent conversation needs the browser, invoke this skill instead of calling the tools directly.

Use the `browser_*` MCP tools bundled with the Kiwi Code Claude Code plugin to operate Kiwi Code's thread-owned browser surface. The implementation may be a server-managed headless Chrome context projected into the Browser workspace or the Electron native guest. Browser state is shared with Pi and Pi Native sessions in that thread.

## Tool discovery

Claude Code may defer MCP tool definitions until they are needed. Use Claude Code's built-in `ToolSearch` when a required `browser_*` tool is not yet loaded. There is no separate `browser_tool_search` MCP tool; that name belongs to Pi's dynamic extension. Search for the smallest set needed for the current task, such as "browser navigate and snapshot" or the exact tool name.

Available tools:

| Tool | Use it for |
|---|---|
| `browser_session` | Check status or start, disconnect, or stop the in-app browser session |
| `browser_tabs` | List, create, select, or close page targets |
| `browser_navigate` | Go to a URL, reload, go back, or go forward |
| `browser_snapshot` | Inspect the page as a compact accessibility tree with actionable refs |
| `browser_click` | Click an element by snapshot ref, or by CSS selector as a fallback |
| `browser_fill` | Replace or append text in an editable control and optionally press Enter |
| `browser_key` | Send a key or chord to the focused element |
| `browser_wait` | Wait for time, selector visibility, page text, or a URL substring |
| `browser_screenshot` | Capture the viewport or a full-page PNG/JPEG |
| `browser_evaluate` | Evaluate JavaScript in the current page's main frame |
| `browser_cdp` | Send an allowlisted CDP `Domain.method` command to the selected page target |

## Browser backend

The only supported backend is `in-app`. Browser tools start or connect to it lazily, so most tasks do not need an explicit session call. Use `browser_session` with `action: "start"` only when explicit lifecycle control is useful.

If an action reports that the in-app provider is unavailable, ask the user to check the configured backend: headless mode needs a supported Chrome installation and Electron mode needs the desktop app. Do not silently switch backends, launch another Chrome yourself, or install a separate browser-control package.

## Preferred workflow

1. Load only the browser tools needed for the current step if the coding agent deferred them.
2. Open the destination with `browser_navigate`, or inspect/select existing pages with `browser_tabs`.
3. Call `browser_snapshot` to read the rendered page's accessibility tree.
4. Use snapshot refs such as `e1` with `browser_click` and `browser_fill`.
5. After navigation or a substantial page update, take a fresh snapshot before using more refs.
6. Use `browser_wait` when an action triggers asynchronous UI or navigation.
7. Use `browser_screenshot` when visual layout matters or the accessibility tree is insufficient.
8. Use `browser_evaluate` or `browser_cdp` only when the focused semantic tools cannot perform the task.

## Snapshots and element refs

Prefer refs over selectors because refs represent controls from the rendered accessibility tree. Refs are scoped to the selected tab and latest document. They can become stale after navigation, DOM replacement, tab selection, or a newer snapshot. Take a fresh snapshot when a ref is unknown or stale. Use a CSS selector only when the relevant element does not receive an accessibility ref.

For large pages, narrow the snapshot with `interactiveOnly`, `maxDepth`, or `maxNodes` when appropriate.

## Navigation, tabs, and waits

A click can open another tab without selecting it. Check the click result for new targets or list tabs, then select the desired target. Target IDs may be supplied as unique prefixes.

Wait conditions supplied together are conjunctive: all must match. Prefer a page condition over an arbitrary delay when a stable selector, text, or URL transition is available.

## Input and visual inspection

Use `browser_fill` for text controls. It clears existing content by default and does not echo entered text in its result. Use `browser_key` for focused-control interactions and shortcuts such as `Enter`, `Tab`, `Escape`, `CTRL+A`, or `META+L`.

Use `browser_screenshot` when the task depends on layout, canvas content, visual state, or an element missing from the accessibility tree. Prefer JPEG with a lower `quality` for large full-page captures. Avoid repeated screenshots when a text snapshot is sufficient.

## JavaScript and raw CDP

`browser_evaluate` can read or mutate the current page. `browser_cdp` is the escape hatch for protocol capabilities without focused tools. Only the selected page target is available. Browser-wide, target-management, host-filesystem, cookie-export, download, and crash commands are blocked. Use official protocol parameter names. Treat evaluated values and CDP results as sensitive.

## Lifecycle and safety

- `browser_session` with `action: "disconnect"` releases the MCP server's control connection without asking Kiwi Code to destroy the in-app browser session.
- `browser_session` with `action: "stop"` destroys the thread's in-app browser session and ephemeral profile; the next start uses fresh site data.
- The profile can contain authenticated sessions and private page data. Treat snapshots, screenshots, evaluated values, and raw CDP results as sensitive.
- Do not try to discover or expose Kiwi Code's private browser transport or an unauthenticated CDP endpoint.
