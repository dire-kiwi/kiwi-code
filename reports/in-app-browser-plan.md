# In-app browser control: research summary and implementation plan

_Last updated: implementation handoff for the draft pull request_

## Draft PR handoff

The first production-shaped vertical slice is implemented on this branch:

- Electron owns one ephemeral, sandboxed `WebContentsView` browser session per Dire Mux thread.
- Go proxies a private, authenticated loopback provider and exposes thread-scoped status, action, and JPEG-preview routes.
- React provides the sixth Browser workspace with tabs, navigation, native-view bounds synchronization, and a projected preview for ordinary web/LAN clients.
- Terminal Pi and Pi Native materialize the same first-party `browser_*` tools and progressive loader, sharing the thread's session.
- Thread/project deletion and application shutdown clean up browser sessions without changing the stable tmux identity contract.
- The provider blocks privileged schemes, host files, downloads, permissions, dangerous CDP methods, and navigation back into Dire Mux's trusted renderer/control plane.
- An installed legacy `browser_*` extension no longer crashes Pi with duplicate registrations; Dire Mux leaves it active and prints a migration warning.

Validation completed before opening the draft:

- `go test ./...`
- `go vet ./...`
- `cd web && npm test`
- `cd web && npm run build`
- isolated Electron stack using fresh loopback Go/Vite/fixture ports, a temporary data directory, and a unique non-production tmux socket;
- real provider navigation, accessibility snapshot, fill, click, JavaScript evaluation, JPEG preview, blocked dangerous CDP, tabless-session state, and session stop;
- a one-off Pi launched with `--no-extensions` plus the explicit Dire Mux extension used `browser_tool_search`/`browser_snapshot` against the shared in-app page and returned the expected title and heading.

### Work remaining before the feature should leave draft

1. **Choose and implement the HTTP trust boundary.** Dire Mux currently documents its all-interface listener as unauthenticated terminal access intended only for trusted networks. JSON content-type enforcement prevents ordinary cross-site form CSRF, and the private Electron provider plus agent routes require capabilities, but the user-facing browser mutation routes inherit the app's trusted-LAN model. Decide whether this release accepts that documented boundary or adds an app-wide/per-launch trusted-renderer capability. CORS alone must not be treated as authorization.
2. **Complete hands-on native-view validation.** Visually verify the actual Electron guest (not only the projected Chrome preview), focus transfer, typing, IME/clipboard behavior, `Cmd+L`, `Cmd+R`, workspace shortcuts, overlays, resize/scale behavior, and hide/show while automation continues. The validation agent could not inspect the native window because macOS Accessibility permission was unavailable.
3. **Automate the real Electron fixture smoke test.** Cover hidden/occluded `capturePage`, snapshot/fill/click, background-tab close, tabless sessions, disconnect, provider restart, and guest crash in an integration test so compositor regressions are not detectable only by manual validation.
4. **Run Linux validation.** Exercise WebContentsView geometry, capture, input, cleanup, and packaging on the supported Linux display stacks. Current runtime validation was on macOS.
5. **Finish legacy-extension migration UX.** Add a scoped way to disable only an installed `@dire-pi/chrome-devtools` package for managed Dire Mux Pi sessions, or provide a guided settings migration. The current safe fallback keeps the legacy tools active and requires the user to disable that package and run `/reload` before the in-app tools take over.
6. **Validate packaged production lifecycle.** Test the signed/packaged desktop launcher, Go restart, Electron reconnect, provider-config rotation/removal, application quit, and cleanup of every ephemeral partition and helper process.

### Follow-up product work

- Add the explicitly selected **existing Chrome profile** backend and companion-extension pairing; never silently switch profile/storage semantics.
- Decide private-network/localhost navigation policy. Local development URLs are useful, but unrestricted local browsing is also an SSRF/local-service capability.
- Add user-versus-agent control leasing, stale-frame/generation rejection, and richer native-overlay handling before interactive remote preview input.
- Define UX for intentionally blocked permissions, downloads, popups, file pickers, authentication/certificate dialogs, and opening a page externally.
- Consider event-driven/backpressured preview streaming after the current bounded polling path is stable.
- Add longer lifecycle/race stress tests for concurrent actions, deletion during capture, browser crashes, backend restart, and many threads.
- Revisit persistent profiles only as an explicit opt-in; ephemeral per-thread storage remains the safe default.

The remainder of this document preserves the original research and longer-term architecture notes for context. Some proposed sidecar and multi-backend work is intentionally not part of the implemented Electron vertical slice.

## User goal

Dire Mux should provide a first-party browser-control experience that:

- works from its web-based UI;
- lets Pi choose between an in-app browser and an existing Chrome profile;
- does not require the user to watch or switch to a separate browser for normal automation;
- exposes controls and a preview so the user can see and, when needed, take over;
- works cross-platform where possible;
- vendors the successful `dire-pi-ext` Chrome DevTools implementation, skill, and Chrome companion extension into Dire Mux;
- moves browser ownership into the Go backend or a subprocess supervised by Go.

## Primary feasibility conclusion

A hidden, real-sized `<iframe>` is **not a viable general browser backend**.

Reasons:

- Arbitrary sites can reject framing with CSP `frame-ancestors` or `X-Frame-Options`.
- A Dire Mux page cannot inspect or manipulate a successfully loaded cross-origin iframe because of the same-origin policy.
- A normal web page cannot access the Chrome DevTools Protocol (CDP).
- An iframe is not reliably its own CDP target: same-process frames are execution contexts/frame IDs, while out-of-process frames may be child targets requiring session-aware recursive attachment.
- Transparent, offscreen, or `display:none` content is not contractually guaranteed to keep foreground layout, paint, timing, or Page Visibility behavior.
- Rewriting framing headers through a proxy would be unsafe and would still not solve same-origin, cookies, OAuth, service workers, WebSockets, integrity, or navigation reliably.

An iframe should only ever be an optional fast path for same-origin, trusted localhost, allowlisted embeddable, or explicitly cooperative `postMessage` applications.

## Recommended product architecture

Use a runtime-neutral `BrowserSession` owned by Dire Mux:

```text
Pi first-party extension ------\
                                \
Dire Mux React Browser pane ----- Go browser API/session manager
                                /                |
Chrome companion extension ----/       supervised browser host
                                                 |
                         +-----------------------+---------------------+
                         |                       |                     |
                  isolated Chromium       Electron/native guest   existing Chrome
                  projected preview       (runtime adapter)       chrome.debugger
```

### User-visible backend choices

1. **In app (default)**
   - Isolated browser profile owned by Dire Mux.
   - Explicit viewport such as 1280x800, independent of whether the preview pane is visible.
   - Browser frames projected into the React UI as screenshots/canvas frames.
   - Pointer and keyboard input forwarded through CDP.

2. **Existing Chrome profile**
   - Uses the bundled Manifest V3 companion extension and `chrome.debugger`.
   - Preserves the selected Chrome profile's cookies, SSO, certificates, and installed extensions.
   - Must be selected explicitly; never silently fall back to another profile because authentication and storage semantics differ.

3. **External CDP endpoint (advanced)**
   - Retains the existing attach-to-an-explicit-endpoint capability.

A desktop runtime may implement the in-app backend with a native embedded guest surface rather than projected frames, but it must retain the same `BrowserSession` API.

## Recommended host ownership

Dire Mux Go should own identity, authorization, lifecycle, APIs, cleanup, and event fan-out. Initially, Go should supervise a bundled Node browser-host subprocess rather than porting the mature TypeScript CDP implementation to Go.

Rationale:

- `dire-pi-ext` already has working and tested CDP transport, browser management, accessibility snapshots, input, screenshots, dynamic Pi tools, and a Chrome-extension relay.
- Porting it to Go would duplicate a substantial amount of protocol and browser behavior.
- A supervised sidecar isolates browser crashes and can be upgraded independently.
- The sidecar can be built into a pinned artifact; Dire Mux must not depend on the sibling `dire-pi-ext` checkout or runtime `npm install`.

Prefer private stdio RPC for commands/events where practical. Any required Chrome-extension or CDP listener must bind only to loopback, use a capability, and use isolated dynamic ports during development and tests.

## Existing `dire-pi-ext` assets to vendor

Vendor these as a coherent first-party subsystem, preserving the MIT license and third-party notices:

- `src/cdp-client.ts`
- `src/browser-manager.ts`
- `src/browser-controller.ts`
- `src/browser-router.ts`
- `src/accessibility.ts`
- browser tool schemas/dynamic loader from `src/index.ts`
- `skills/chrome-devtools-browser/SKILL.md`
- `src/chrome-extension-bridge.ts`
- the complete `chrome-extension/` directory
- the current test suite
- `LICENSE` and `THIRD_PARTY_NOTICES.md`

Do not vendor `node_modules`. Bundle `ws`; externalize Pi-provided modules such as `@earendil-works/pi-coding-agent`, `@earendil-works/pi-ai`, and `typebox` where the Pi adapter needs them.

## First-party Pi integration

Dire Mux already materializes extensions in `internal/server/pi_extension.go` and passes them to terminal Pi and Pi Native from `internal/server/terminal.go` and `internal/server/pi_native.go`.

The browser integration should:

- preserve the existing tool names:
  - `browser_tool_search`
  - `browser_session`
  - `browser_tabs`
  - `browser_navigate`
  - `browser_snapshot`
  - `browser_click`
  - `browser_fill`
  - `browser_key`
  - `browser_wait`
  - `browser_evaluate`
  - `browser_screenshot`
  - `browser_cdp`
- preserve additive dynamic tool loading and the current progressive-disclosure skill;
- use a thin Pi adapter that calls the Dire Mux browser service through the existing thread endpoint and `X-Dire-Mux-Agent-Token`;
- load the skill through `resources_discover` or an explicit `--skill` path, because passing only `--extension` does not load a package manifest's skills;
- make terminal Pi and Pi Native in the same Dire Mux thread intentionally share one browser session;
- give child threads separate sessions;
- detect an already installed `@dire-pi/chrome-devtools` package and produce a clear migration warning instead of ambiguous duplicate tools;
- provide a stable reload/import path so existing terminal Pi sessions can acquire the first-party extension with `/reload` where possible.

## Go backend plan

Add a package such as:

- `internal/browsercontrol/manager.go`
- `internal/browsercontrol/process.go`
- `internal/browsercontrol/protocol.go`

Responsibilities:

- session ownership keyed by project/thread;
- lazy startup and concurrent-start deduplication;
- serialized operations and cancellation;
- browser-host readiness and health;
- bounded stderr, results, screenshots, frame rate, queues, and request sizes;
- whole-process-group shutdown for Chromium and helpers;
- `StopThread`, `StopProject`, and `Close` lifecycle hooks;
- cleanup on thread/project deletion and application shutdown;
- no use of tmux and no changes to the stable tmux socket, session, window, or tool mappings.

Suggested API shape:

- `GET /api/projects/{id}/threads/{threadId}/browser`
- `POST /api/projects/{id}/threads/{threadId}/browser/actions`
- `GET /api/projects/{id}/threads/{threadId}/browser/events`
- `GET /api/projects/{id}/threads/{threadId}/browser/stream` when bidirectional frames/input are needed

Agent browser mutations must require the private agent capability. Validate project/thread and authentication before starting a browser. Do not expose raw CDP URLs to the frontend.

Browser state should include:

- backend and profile/storage policy;
- lifecycle and connection status;
- tabs and selected target;
- URL/title/history state;
- viewport dimensions;
- latest frame generation and timestamp;
- companion-extension state;
- user-versus-agent control lease.

## React UI plan

Add Browser as a sixth **non-terminal** workspace tool:

- extend `web/src/types.ts`;
- update `web/src/routes.ts`;
- add the tab/shortcut and explicit rendering branch in `web/src/components/pages/TerminalWorkspace.tsx`;
- add `web/src/components/organisms/BrowserPane.tsx`.

Do not pass `browser` to the terminal WebSocket or add it to tmux tool normalization.

Recommended controls:

- back, forward, reload, stop;
- address bar;
- backend/profile selector and capability badge;
- controlled, paused, disconnected, and error states;
- **Take control / Resume agent** lease controls;
- viewport/device presets;
- refresh/live-preview toggle;
- focus the real Chrome tab or desktop guest;
- open externally or in a separate window;
- inspect with a debugger-detach warning;
- reset site data;
- close session.

The browser pane can remain mounted while inactive following the existing absolute-pane pattern. The actual renderer viewport must be explicit and retain its last nonzero size; it must not depend on CSS opacity or an invisible iframe.

Start with automatic preview capture after each browser action and periodic refresh only while visible. Add `Page.startScreencast` later after ACK, reconnect, and backpressure behavior is implemented. Every interactive frame should carry a generation ID; stale clicks must be rejected.

Browser-specific Pi Native tool cards should include a **Show browser** action. Terminal Pi users can use the persistent Browser workspace tab and activity indicator.

## Desktop/runtime considerations already identified

The current Electron shell in `web/electron/main.cjs` contains one sandboxed application `BrowserWindow`, no preload, no IPC, and no guest surface. Automation must never target or navigate this trusted application renderer.

If Electron remains the desktop runtime, use a separate sandboxed `WebContentsView` for visible remote content or an offscreen `BrowserWindow` for background frame production. Use:

- `sandbox: true`
- `contextIsolation: true`
- `nodeIntegration: false`
- a dedicated session partition
- denied permissions by default
- explicit popup, navigation, download, certificate, crash, and cleanup handling

The React renderer should receive only a narrow typed IPC/preload API for guest bounds, visibility, input, and lifecycle.

## Chrome companion plan

Move the Manifest V3 extension and its relay ownership into Dire Mux:

- materialize a stable versioned extension directory;
- add a Browser Control section to Settings;
- show extension path/download, pairing host/port/token, installed/connected version, reconnect, revoke, and existing-tab-sharing controls;
- preserve per-thread tab-group isolation;
- bind only to loopback and authenticate every bridge connection;
- use production-compatible migration for an existing bridge token/port where possible;
- use fresh ports and temporary tokens/profiles for development and tests;
- version the custom bridge protocol and capabilities;
- never silently switch away from an existing profile on debugger/socket/service-worker loss.

## Preview limitations and required fallbacks

A screenshot/canvas preview is not fully equivalent to a native browser surface. Explicit handling or a **Focus/Open real browser** fallback is required for:

- browser-owned permission and authentication dialogs;
- file pickers and downloads;
- popups;
- IME, clipboard, drag/drop, and complex input;
- some video, GPU, compositor, and protected surfaces;
- DevTools attachment conflicts;
- page visibility and background throttling differences.

Backend fidelity must be visible to the user. Existing Chrome, an isolated Chromium profile, and a desktop guest do not have identical cookies, SSO, extensions, certificates, permissions, storage, or memory cost.

The UI should call projected content a **browser preview**, not promise a perfectly equivalent embedded browser.

## Recommended defaults

- Backend: **In app**
- Ownership: one browser session per Dire Mux thread
- Terminal Pi and Pi Native: shared browser session
- Child threads: isolated browser sessions
- Storage: ephemeral per thread initially; persistent site data is explicit opt-in
- Preview: capture after actions; live refresh only while visible
- Control conflict: user takes precedence and pauses agent input
- Existing-profile failure: pause and ask; no silent fallback
- iframe mode: deferred and restricted to trusted/allowlisted cooperative content

## Security requirements

- Keep raw CDP and bridge listeners off public interfaces.
- Use random capabilities and constant-time comparison.
- Never log pairing tokens or CDP credentials.
- Treat URLs, page text, screenshots, cookies, browser logs, and profiles as sensitive.
- Block access from guest pages to Dire Mux/browser control-plane endpoints.
- Decide explicitly how localhost/private-network navigation is approved, because local development browsing is useful but is also an SSRF/local-service risk.
- Validate schemes and block or explicitly gate `file:`, `javascript:`, browser-extension, and custom schemes.
- Constrain downloads to session-owned directories with traversal/symlink defenses.
- Preserve `Browser.close` blocking for existing-profile Chrome.
- Repair file permissions on stored tokens/configuration.
- Add protocol message-size limits, consistent timeouts, and bounded event fan-out.

## Validation plan

Retain and extend the current `dire-pi-ext` tests. Add coverage for:

- browser-host RPC parsing, cancellation, readiness, failure, and restart;
- concurrent startup deduplication and stale-stop protection;
- whole-process-tree cleanup;
- agent capability and origin rejection;
- no browser start before successful authorization/upgrade;
- per-thread and child-thread isolation;
- user/agent control leasing;
- screenshot limits and frame backpressure;
- stale frame input rejection;
- Chrome extension reconnect, debugger detach, and ownership;
- hidden Browser pane retaining a nonzero explicit viewport;
- an HTTP fixture with `X-Frame-Options: DENY`, proving the architecture does not depend on framing;
- navigation, AX snapshot, click, fill, wait, screenshot, and raw CDP;
- server restart behavior;
- Chrome web UI and desktop runtime smoke tests.

All application validation must use fresh loopback ports, a temporary data directory, and a unique non-production tmux socket. It must never inspect, connect to, create sessions in, or kill the canonical `kiwi-code` or legacy `dire-mux` production tmux server.

## Open questions for the next research phase

The next requested investigation is whether Dire Mux should move away from Electron to another desktop/runtime framework that keeps the UI web-based while allowing a true embedded browser guest. Research should compare cross-platform and platform-specific options, including:

- frameworks with a web frontend plus multiple native WebViews;
- the ability to embed an arbitrary remote browser surface separately from the trusted app UI;
- CDP or equivalent automation access;
- hidden/background rendering behavior;
- profile/session isolation;
- macOS and Linux support, packaging, security, maturity, and maintenance burden;
- whether separate macOS and Linux shells are more realistic than one cross-platform shell.
