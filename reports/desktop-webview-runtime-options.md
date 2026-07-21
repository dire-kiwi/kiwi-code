# Desktop runtime options for a web-based UI with an embedded browser

_Research pass following `reports/in-app-browser-plan.md`._

## Question

Can Kiwi Code move away from Electron while retaining its React/web UI and gaining a true second embedded browser surface on macOS and Linux?

## Executive conclusion

Yes, but changing runtime does not remove the central tradeoff:

- Lightweight system-webview shells use **WKWebView on macOS** and **WebKitGTK on Linux**. They can display a real second top-level guest view, but they do not provide Chromium/CDP parity, reliable hidden rendering, or the same automation capabilities on both platforms.
- Full Chromium embedding is available through **CEF**, **Qt WebEngine**, **NW.js**, or Electron itself. Those retain a Chromium-sized distribution and browser-update obligation.

For Kiwi Code, the recommended order is:

1. **Keep Electron and prototype a separate `WebContentsView`.** It already provides the exact primitives required and is by far the lowest-risk path.
2. If leaving Electron is a firm product requirement, use **CEF with a thin C++ host**. It is the strongest non-Electron fit for cross-platform Chromium, profiles, direct CDP, native views, and offscreen rendering.
3. Consider **Qt WebEngine with a thin C++ host** if higher-level native browser widgets are preferred and LGPL/commercial Qt deployment is acceptable.
4. Consider **Tauri 2** only if smaller system-webview packaging matters more than Chromium fidelity and full browser automation. Its multi-webview feature remains unstable and has unresolved Linux/Wayland geometry concerns.
5. A split **Swift/WKWebView macOS shell plus GTK/WebKitGTK Linux shell** is feasible for visible, user-operated browsing, but it would require two automation implementations and would lose the current CDP tool parity.

The React application and Go backend do not need to be rewritten for any of these choices. The current Electron layer is already a thin wrapper around the loopback-served web app.

## Important product boundary

A native embedded view exists only in the local desktop shell. It does **not** make an arbitrary browser embeddable in the normal Kiwi Code web client opened in Chrome or over the LAN.

Therefore there are two separate product modes:

### Local desktop client

A native guest view can be shown directly:

- Electron `WebContentsView`
- CEF `CefBrowserView`
- Qt `QWebEngineView`
- Tauri/Wry child webview
- macOS `WKWebView`
- Linux `WebKitWebView`

### Ordinary browser/LAN client

The browser engine must still be projected into the page through:

- screenshots/canvas frames;
- a bounded screencast or video stream;
- normalized pointer/keyboard forwarding;
- or an existing Chrome tab controlled by the companion extension.

Changing Electron cannot bypass browser CSP, iframe, or same-origin restrictions for ordinary web clients.

## Requirements used for comparison

- React UI remains the primary application UI.
- Trusted Kiwi Code UI and arbitrary remote guest content are separate web contents.
- Guest pages are top-level in their own browser context, not iframes.
- macOS and Linux support.
- Independent profiles or ephemeral sessions.
- Back/forward/reload/navigation policy.
- Screenshots and hidden/background operation.
- Trusted mouse/keyboard input.
- DOM and accessibility snapshots.
- CDP or an equivalent automation protocol.
- Safe permission, popup, download, certificate, and file-picker handling.
- Reasonable packaging, updates, signing, and maintenance.

## Decision matrix

| Option | True second view | macOS + Linux | Engine | Profiles | Trusted input | Screenshot/offscreen | CDP parity | Maturity | Kiwi Code fit |
|---|---:|---:|---|---|---|---|---|---|---|
| Electron `WebContentsView` | Yes | Yes | Chromium | Excellent | Excellent | Excellent | Excellent, in process | Very high | **Best near-term** |
| CEF | Yes | Yes | Chromium | Excellent | Excellent | Best offscreen control | Excellent, in process | High upstream; host is low-level | **Best non-Electron** |
| Qt WebEngine | Yes | Yes | Chromium | Excellent | Good/CDP | Visible capture good; offscreen weak | Good, normally via debug port | High | Strong if Qt is acceptable |
| NW.js `<webview>` | Yes, web-authored | Yes | Chromium | Partitions | No direct documented guest input API | Visible capture | Remote debug requires SDK flavor | Mature | Interesting, but weaker automation/security story |
| Tauri 2 child webview | Yes, unstable | Qualified | WKWebView/WebKitGTK | Good with platform differences | JS/focus only | No stable per-view screenshot API | No | Framework stable; feature unstable | Prototype only |
| Direct WKWebView + WebKitGTK | Yes | Two implementations | WebKit | Good | Limited/uneven | Native snapshots; hidden unreliable | No common CDP | Mature native APIs | Good visible browser, poor parity |
| Wails v2 | No | Yes | System WebView | Weak | No | No | No | Stable | Eliminate |
| Wails v3 | Separate windows, not embedded panels | Yes | System WebView | Weak/undocumented | JS simulation | No pixel API | No | Alpha | Revisit later |
| Neutralino / `webview/webview` / Photino | One view per window | Yes | System WebView | Weak | No | Weak | No | Mixed | Eliminate |
| Ultralight | Yes | Yes | WebKit-derived | App-defined | App-defined | Strong embedding | No CDP | Commercial product | Not a full arbitrary-site browser |
| Servo | Emerging | Yes | Servo | Emerging | Emerging | Embedder-owned | No mature DevTools | Pre-1.0 | Research only |

## Option 1: Keep Electron and use `WebContentsView`

### Why it is still the strongest practical option

The current `web/electron/main.cjs` is only a thin sandboxed shell. It has no deep coupling to the React code or Go server. Electron already provides:

- multiple native browser surfaces through `BaseWindow`/`WebContentsView`;
- separate persistent or in-memory `Session` partitions;
- arbitrary remote top-level navigation without iframe framing restrictions;
- direct DevTools Protocol commands through `webContents.debugger`;
- `capturePage`;
- offscreen rendering with paint callbacks;
- native input dispatch;
- downloads, permissions, authentication, certificates, popups, and session APIs;
- macOS and Linux builds from the existing dependency.

Recommended composition:

```text
Electron native window
├── trusted Kiwi Code WebContentsView
│   └── React UI, narrow preload/IPC only
└── untrusted browser WebContentsView
    ├── separate Session partition
    ├── no preload or privileged IPC
    ├── explicit permissions/navigation/download policies
    └── CDP through webContents.debugger
```

React reserves a browser rectangle and sends its bounds/visibility to the main process. This native-view geometry synchronization is also required by CEF, Qt, Tauri child views, WKWebView, and WebKitGTK.

### Costs

- Chromium/Node distribution size and memory.
- Frequent Electron security updates.
- Native child views do not naturally obey arbitrary DOM clipping, transforms, or overlays.
- Hidden/background behavior still needs explicit tests.

### Verdict

Do this vertical slice before committing to a runtime migration. If it fails, the same geometry and guest-lifecycle problems will also affect most alternatives.

Official references:

- <https://www.electronjs.org/docs/latest/api/web-contents-view>
- <https://www.electronjs.org/docs/latest/api/base-window>
- <https://www.electronjs.org/docs/latest/api/session>
- <https://www.electronjs.org/docs/latest/api/debugger>
- <https://www.electronjs.org/docs/latest/tutorial/offscreen-rendering>
- <https://www.electronjs.org/docs/latest/tutorial/security>

## Option 2: Chromium Embedded Framework (CEF)

### Capabilities

CEF is the strongest true browser-embedding framework outside Electron:

- multiple `CefBrowser`/`CefBrowserView` instances;
- native child views or windowless/offscreen rendering;
- dirty-rectangle pixel callbacks through `CefRenderHandler`;
- explicit frame rate, resize, hidden state, and invalidation;
- native keyboard, mouse, and wheel input APIs;
- independent or shared `CefRequestContext`s for profiles, cookies, cache, and storage;
- request interception and browser-process callbacks;
- direct `ExecuteDevToolsMethod` and `AddDevToolsMessageObserver` APIs without opening a remote-debugging port;
- standard Chromium website behavior on both macOS and Linux.

This would let the existing TypeScript browser-controller semantics be adapted with minimal conceptual change.

### Host choices

- **Thin C++ shell:** recommended. It uses the upstream API and official `cef-project` starter.
- **Rust `cef-rs`:** credible prototype path. `tauri-apps/cef-rs` actively tracks current CEF and supports macOS/Linux x64 and ARM64, but remains lower-level than an application framework.
- **Go wrappers:** not recommended as the core choice. Current wrappers have smaller ecosystems and add CGO/ownership/update risk. Keep the Go backend separate instead.

### Costs

- Native CMake/toolchains and a new shell implementation.
- Large CEF framework, resources, locales, helper applications, and sandbox assets.
- macOS bundle/signing/helper-process complexity.
- Linux sandbox and runtime packaging.
- Manual Chromium/CEF security update cadence.
- Browser lifecycle, IME, accessibility, downloads, popups, permissions, certificates, and crash recovery all become Kiwi Code responsibilities.

CEF is BSD-licensed, with Chromium third-party notices and codec obligations.

### Verdict

**Best choice if Electron must be removed while retaining full browser-control fidelity.** It removes Electron/Node, not Chromium-sized distribution or maintenance.

Official references:

- <https://github.com/chromiumembedded/cef>
- <https://github.com/chromiumembedded/cef-project>
- <https://chromiumembedded.github.io/cef/general_usage.html>
- <https://chromiumembedded.github.io/cef/sandbox_setup.html>
- <https://cef-builds.spotifycdn.com/docs/145.0/classCefBrowserHost.html>
- <https://cef-builds.spotifycdn.com/docs/145.0/classCefRenderHandler.html>
- <https://cef-builds.spotifycdn.com/docs/145.0/classCefRequestContext.html>
- <https://github.com/tauri-apps/cef-rs>

## Option 3: Qt WebEngine

### Capabilities

Qt WebEngine provides a high-level Chromium widget model:

```text
QWebEngineView -> QWebEnginePage -> QWebEngineProfile
```

It supports:

- multiple normal browser widgets in one window;
- named persistent and off-the-record profiles;
- cookie, storage, cache, permission, download, popup, authentication, and certificate APIs;
- screenshots of visible widgets with normal Qt capture;
- JavaScript execution and WebChannel integration;
- in-app DevTools;
- remote CDP using `QWebEnginePage::devToolsId()`.

A React UI can run in one `QWebEngineView`, with a separate untrusted guest `QWebEngineView`. Only the trusted view receives a native WebChannel bridge.

### Language choices

- **Thin C++/Qt shell:** recommended if choosing Qt.
- **Go through MIQT:** possible and attractive because Kiwi Code is Go. MIQT includes Qt 6 WebEngine bindings, but is young/pre-1.0 and requires careful ownership/threading/packaging validation.
- **Rust/CXX-Qt:** viable when Rust is strategic, but WebEngine should still be owned by Qt/C++ or QML.
- **PySide6:** functional but adds a Python runtime and does not simplify deployment.

### Limitations

- No public CEF-style direct CDP API; raw CDP normally requires a loopback remote-debugging port.
- No robust first-class windowless/offscreen pixel callback. Qt documents rendering caveats for `WebEngineView` under offscreen `QQuickRenderControl`.
- Deployment includes Qt libraries, `QtWebEngineProcess`, resources, locales, ICU/V8 data, and plugins; static WebEngine builds are unsupported.
- Qt-specific code is commercial or LGPL/GPL, alongside Chromium obligations.
- Qt chooses and backports a Chromium baseline; browser-version/security cadence must be monitored.

### Verdict

**Best high-level non-Electron option**, especially if a broader native Qt UI is desirable. CEF is better if exact offscreen behavior and private direct CDP control are central.

Official references:

- <https://doc.qt.io/qt-6/qtwebengine-overview.html>
- <https://doc.qt.io/qt-6/qwebengineview.html>
- <https://doc.qt.io/qt-6/qwebengineprofile.html>
- <https://doc.qt.io/qt-6/qwebenginepage.html>
- <https://doc.qt.io/qt-6/qtwebengine-debugging.html>
- <https://doc.qt.io/qt-6/qtwebengine-deploying.html>
- <https://doc.qt.io/qt-6/qtwebengine-licensing.html>
- <https://github.com/mappu/miqt>

## Option 4: NW.js

### Why it is interesting

NW.js is the closest web-authored alternative to Electron. It ships Chromium and supports a real `<webview>` guest element:

```html
<webview src="https://example.com" partition="browser-thread-123"></webview>
```

Unlike an iframe, the guest is a separate browser surface/process and is not subject to the destination's iframe framing policy. The React UI can lay it out as an element. NW.js supports:

- remote pages;
- storage partitions;
- asynchronous navigation and lifecycle events;
- script injection, including all frames;
- `captureVisibleRegion`;
- an explicit untrusted-guest model when `allownw` is not enabled;
- current macOS/Linux Chromium builds.

### Limitations

- No documented `webview.sendInputEvent` equivalent.
- `chrome.debugger` is not documented as a supported NW.js extension API.
- Full CDP uses `--remote-debugging-port`, and official DevTools support requires the SDK build that NW.js recommends for development rather than normal production.
- Shipping Node-enabled app pages requires careful hardening. Remote guests must never receive `allownw` or `node-remote` privileges.
- It is still another Chromium+Node desktop runtime, so this is a runtime swap rather than a lightweight browser solution.

### Verdict

Worth a small proof of concept if having a browser surface directly represented in the web layout is more important than retaining the current browser automation architecture. It is not the recommended first production choice.

Official references:

- <https://nwjs.io/>
- <https://docs.nwjs.io/References/webview%20Tag/>
- <https://docs.nwjs.io/For%20Users/Advanced/Security%20in%20NW.js/>
- <https://docs.nwjs.io/For%20Users/Debugging%20with%20DevTools/>

## Option 5: Tauri 2 / Wry

### Capabilities

Tauri retains the React UI and uses native system webviews:

- WKWebView on macOS;
- WebKitGTK on Linux;
- WebView2 on Windows.

Tauri 2 can add multiple child webviews to one native window. Each child can load a remote top-level URL and supports bounds, focus, visibility, navigation callbacks, initialization scripts, JavaScript evaluation, incognito mode, cookies, and browsing-data clearing.

Its capability model is strong: arbitrary remote guest origins can receive zero native permissions while only the trusted React view receives application capabilities.

### Critical limitations for Kiwi Code

- Multi-webview remains behind Tauri's `unstable` feature.
- Linux child-view placement has known X11/Wayland/GTK-container complications, including open work around `gtk::Fixed` positioning and bounds.
- No stable per-webview screenshot API.
- No trusted input-dispatch API equivalent to CDP/Electron.
- No common CDP protocol; the engine is WebKit, not Chromium.
- macOS embedded WKWebView has no public WebDriver/CDP automation path.
- Persistent independent macOS webview stores are cleanest on macOS 14+.
- Hidden WebKit views can be throttled and have no supported headless mode.
- Linux behavior depends on distribution WebKitGTK/GStreamer versions.

### Verdict

**Best lightweight prototype**, not a match for the full browser-control requirement. Choose it only if a visible embedded browser and reduced bundle size matter more than CDP/raw browser automation parity.

Official references:

- <https://v2.tauri.app/reference/javascript/api/namespacewebview/>
- <https://v2.tauri.app/reference/webview-versions/>
- <https://v2.tauri.app/security/capabilities/>
- <https://docs.rs/tauri/latest/tauri/webview/struct.WebviewBuilder.html>
- <https://github.com/tauri-apps/wry>
- <https://github.com/tauri-apps/tauri/issues/10420>

## Option 6: Wails

Wails is attractive because it uses Go and a web frontend, but it does not currently solve the required layout:

- Wails v2 is stable but single-window/single-webview.
- Wails v3 is alpha and supports multiple top-level `WebviewWindow`s, not multiple embedded child views in one native window.
- An embedded `WebviewPanel` remains proposed/unmerged.
- Profiles, per-view screenshots, trusted guest input, and CDP are not available as a mature cross-platform feature set.

### Verdict

Do not choose Wails for this feature today. Revisit after v3 stabilizes and embedded panels/profile APIs ship.

References:

- <https://wails.io/>
- <https://v3.wails.io/status/>
- <https://v3.wails.io/features/windows/multiple/>
- <https://github.com/wailsapp/wails/issues/1997>

## Option 7: Separate native macOS and Linux shells

### macOS: Swift/AppKit + WKWebView

Use two sibling WKWebViews:

- trusted React UI with the only script-message/native bridge;
- arbitrary remote guest with a separate nonpersistent or named website-data store.

Available:

- normal navigation, history, policy delegates, authentication, permissions, downloads, popups;
- JavaScript injection/evaluation;
- `takeSnapshot`;
- Safari Web Inspector opt-in;
- system-provided browser security updates.

Missing:

- public CDP or production WebDriver control for an embedded WKWebView;
- deterministic trusted input injection;
- reliable hidden/headless rendering;
- exact parity with Chrome-focused sites and the existing browser tools.

### Linux: GTK 4 + WebKitGTK 6.0

Use two WebKitWebView widgets with separate WebKitNetworkSession/website-data managers.

Available:

- normal navigation, policy, permission, popup, authentication, and download signals;
- JavaScript evaluation;
- visible/full-document snapshots;
- ephemeral/persistent profiles;
- WebKit WebDriver automation when explicitly enabled;
- system/distro browser security updates.

Missing or qualified:

- no shared CDP with macOS;
- WebDriver is an opt-in application automation facility, not identical to the current browser host;
- only one WebKit context can be automation-enabled;
- hidden/unmapped GTK widgets are not reliable renderers;
- distro WebKitGTK/GStreamer versions differ.

### Verdict

This is viable if the embedded browser is primarily visible and manually operated. It is not recommended when background automation, trusted input, accessibility snapshots, and raw CDP must behave consistently.

References:

- <https://developer.apple.com/documentation/webkit/wkwebview>
- <https://developer.apple.com/documentation/webkit/wkwebsitedatastore>
- <https://developer.apple.com/documentation/webkit/wkwebview/takesnapshot%28with%3Acompletionhandler%3A%29>
- <https://webkitgtk.org/reference/webkit2gtk/stable/class.WebView.html>
- <https://webkitgtk.org/reference/webkit2gtk/stable/class.AutomationSession.html>

## Eliminated or research-only options

### Neutralino, Photino, `webview/webview`, pywebview

Generally one system webview per native window. Multiple windows are not embedded child surfaces. They lack the profile, screenshot, input, and automation controls required here.

### Ultralight

Good controlled HTML UI embedding, but not a full arbitrary-site browser: no WebGL or WebRTC, experimental media support, and proprietary/commercial licensing considerations.

### Servo / Verso

Servo embedding is active but still a work in progress with sparse embedding documentation. Verso is archived. Not appropriate for untrusted arbitrary websites in a production tool today.

### WebView2 shells

WebView2 is Windows-only. Cross-platform frameworks substitute WKWebView/WebKitGTK on macOS/Linux and inherit their limitations.

## Platform and security architecture regardless of runtime

```text
Native window/process
├── trusted application view
│   ├── React UI
│   ├── dedicated application profile
│   └── narrowly scoped native/Go bridge
└── untrusted guest view
    ├── arbitrary HTTPS content
    ├── separate ephemeral/profile storage
    ├── no native bridge or secrets
    └── explicit policy for navigation, permissions, popups, downloads, auth, certificates
```

The guest must not receive:

- the Go backend capability;
- native IPC bindings;
- application preload scripts;
- custom privileged schemes;
- Node integration;
- access to the browser-host control plane.

Kiwi Code currently has an unauthenticated all-interface production mode. Before embedding hostile pages locally, add a per-launch trusted-view capability and reject unauthorized HTTP mutations, EventSource access, and WebSocket upgrades. CORS alone is not authorization.

## Recommended proof-of-concept sequence

### POC A: Electron baseline

Implement one external `WebContentsView` in the existing shell and prove:

- exact React placeholder bounds and scaling;
- hide/show without losing session or viewport;
- isolated profile;
- CDP snapshot/click/fill/screenshot;
- focus, keyboard, IME, clipboard;
- popups, downloads, permissions, file picker, authentication;
- macOS and Linux behavior;
- Browser tab hidden while agent continues.

This gives a baseline against which every alternative must be measured.

### POC B: one non-Electron candidate

If the baseline works but Electron removal remains desirable:

- choose **CEF/C++** for full parity; or
- choose **Tauri 2** for a lightweight WebKit experiment.

Use the same acceptance suite. A candidate fails architecturally if it cannot provide:

- correct Linux Wayland bounds;
- profile isolation;
- hidden session continuity;
- trusted input;
- screenshots;
- automation parity required by the Pi tools.

### POC C: web-client fallback

Independently prove projected frames/input for an ordinary Chrome/LAN client, because no desktop runtime solves that case.

## Final recommendation

Do not migrate away from Electron merely to obtain a second embedded browser. Electron already has the strongest high-level implementation for that requirement.

If the strategic goal is specifically **no Electron/Node**, choose CEF with a thin C++ shell. If the strategic goal is specifically **small native packages**, prototype Tauri, accepting that it changes the engine and browser-control capability. If the goal is **reuse Go in the desktop shell**, Qt WebEngine through MIQT is interesting but should be treated as an exploratory spike rather than the production default.
