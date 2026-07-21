---
name: kiwi-code-in-app-browser
description: Controls Kiwi Code's in-app browser by dynamically loading browser_* tools with browser_tool_search. Use when a task requires opening or interacting with websites, inspecting rendered pages, filling forms, taking screenshots, evaluating JavaScript in a page, managing tabs, or sending raw CDP commands.
license: MIT
compatibility: Requires a Kiwi Code-managed Pi session and the Kiwi Code desktop in-app browser provider.
context: fork
allowed-tools: browser_tool_search browser_session browser_tabs browser_navigate browser_snapshot browser_click browser_fill browser_key browser_wait browser_evaluate browser_screenshot browser_cdp
---

# Kiwi Code in-app browser control

Use the `browser_*` tools to operate the real browser surface embedded in the Kiwi Code desktop app. Browser state belongs to the current Kiwi Code thread; terminal Pi and Pi Native in that thread share it. The first-party Pi extension sends semantic actions to Kiwi Code rather than opening a separate Chrome process itself.

## Dynamic tool loading

Only `browser_tool_search` is initially active. Before using another browser tool, call the loader with the smallest set needed for the current task:

```typescript
browser_tool_search({
  query: "open and inspect a website",
  toolNames: ["browser_navigate", "browser_snapshot"]
})
```

When exact names are not known, omit `toolNames` and let the loader search by capability:

```typescript
browser_tool_search({ query: "fill and submit a login form" })
```

The loaded definitions become available on the **next model turn**. Do not attempt to call a newly loaded tool in the same assistant response as `browser_tool_search`. Loading is additive: tools already loaded remain active, so load more only when the task requires them. Prefer exact `toolNames` from the table below for deterministic activation.

`browser_click` and `browser_fill` automatically load `browser_snapshot` as a dependency.

## Browser backend

The only supported backend is `in-app`. Browsing tools start or connect to it lazily, so most tasks do not need an explicit session call. Use `browser_session({ action: "start", backend: "in-app" })` only when explicit lifecycle control is useful. Existing-profile, companion-extension, external-CDP, and standalone desktop-provider backends are not available through this bundled extension.

If an action reports that the in-app desktop provider is unavailable (HTTP 503), ask the user to start or reconnect the Kiwi Code desktop app. Do not silently switch to another browser, launch Chrome yourself, or install `@dire-pi/chrome-devtools`. A 404 with no Kiwi Code error payload indicates that the running version does not expose the browser endpoint; page- or element-specific 404 errors are ordinary operation failures and should be handled as instructed.

## Preferred workflow

1. Use `browser_tool_search` to activate only the capabilities needed now.
2. Open the destination with `browser_navigate`, or inspect/select existing pages with `browser_tabs`.
3. Call `browser_snapshot` to read the rendered page's accessibility tree.
4. Use snapshot refs such as `e1` with `browser_click` and `browser_fill`.
5. After navigation or a substantial page update, take a fresh snapshot before using more refs.
6. Use `browser_wait` when an action triggers asynchronous UI or navigation.
7. Use `browser_screenshot` when visual layout matters or the accessibility tree is insufficient.
8. Use `browser_evaluate` or `browser_cdp` only when the focused semantic tools cannot perform the task.

Do not load or call `browser_session` before every task: all browsing tools connect or auto-launch lazily. Use it only for explicit status and lifecycle management.

## Tool selection

| Tool | Use it for |
|---|---|
| `browser_tool_search` | Dynamically find and activate the browser tools needed for the task |
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

## Snapshots and element refs

Prefer refs over selectors because refs come from the rendered accessibility tree and usually represent the control a user perceives.

```typescript
browser_snapshot({})
browser_click({ ref: "e4" })
browser_fill({ ref: "e7", text: "search terms", submit: true })
```

Refs are scoped to the currently selected tab and latest document. They may become stale when:

- the page navigates;
- a client-side application replaces DOM nodes;
- another tab is selected; or
- a newer snapshot supersedes the previous one.

If a ref is unknown or stale, call `browser_snapshot` again. Use a CSS selector only when the relevant element does not receive an accessibility ref.

For large pages, narrow the snapshot when appropriate:

```typescript
browser_snapshot({ interactiveOnly: true, maxNodes: 500 })
```

## Navigation, tabs, and waits

```typescript
browser_navigate({ action: "goto", url: "https://example.com" })
browser_navigate({ action: "back" })
browser_tabs({ action: "list" })
browser_tabs({ action: "select", targetId: "target-id-or-unique-prefix" })
browser_wait({ selector: "[data-loaded]", state: "visible", timeoutMs: 15000 })
```

A click can open another tab without selecting it. Check the click result for new targets or call `browser_tabs({ action: "list" })`, then select the desired target.

Wait conditions supplied together are conjunctive: all must match. Prefer a page condition over an arbitrary delay when a stable selector, text, or URL transition is available.

## Page input

Use `browser_fill` for text controls. It clears existing content by default and does not echo entered text in its result.

Use `browser_key` for focused-control interactions and shortcuts:

```typescript
browser_key({ key: "Enter" })
browser_key({ key: "Tab" })
browser_key({ key: "CTRL+A" })
browser_key({ key: "META+L" })
```

## Visual inspection

Use `browser_screenshot` when the task depends on layout, canvas content, visual state, or an element missing from the accessibility tree.

```typescript
browser_screenshot({ format: "png" })
browser_screenshot({ format: "jpeg", quality: 70, fullPage: true })
```

Avoid repeated screenshots when a text snapshot is sufficient, because image results are larger and are sent to the model.

## JavaScript and raw CDP

`browser_evaluate` can read or mutate the current page:

```typescript
browser_evaluate({ expression: "document.title" })
browser_evaluate({ expression: "JSON.parse(localStorage.getItem('state'))" })
```

`browser_cdp` is the escape hatch for protocol capabilities without focused tools:

```typescript
browser_cdp({ method: "Network.enable", params: {} })
browser_cdp({
  method: "Emulation.setDeviceMetricsOverride",
  params: {
    width: 390,
    height: 844,
    deviceScaleFactor: 3,
    mobile: true
  }
})
```

Only the selected page target is available. Browser-wide, target-management, host-filesystem, cookie-export, download, and crash commands are blocked. Use official protocol parameter names. A raw call returns only that command's direct response; it does not create a persistent event stream. Treat `browser_evaluate` and `browser_cdp` as privileged operations because they can read and alter the selected page's state.

## Lifecycle and safety

- `browser_session({ action: "disconnect" })` releases the current browser connection without asking Kiwi Code to destroy the in-app browser session.
- `browser_session({ action: "stop" })` destroys the current thread's in-app browser session and its ephemeral profile; the next start uses fresh site data.
- The in-app profile can contain authenticated sessions and private page data. Treat snapshots, screenshots, evaluated values, and raw CDP results as sensitive.
- Do not try to discover or expose Kiwi Code's private browser transport or an unauthenticated CDP endpoint.
