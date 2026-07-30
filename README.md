# Kiwi Code

Kiwi Code is a local, terminal-first project workspace. Add folders from your machine, create threads beneath each project, then switch between a regular shell, Neovim, Lazygit, a process console, Pi, Codex CLI, or Claude Code without leaving the browser.

The backend is written in Go and provides persistent project storage plus tmux-backed terminal sessions over WebSockets. The frontend uses Vite, React, Tailwind CSS, and xterm.js with the canvas renderer. JetBrains Mono and Nerd Font symbols are configured for icon-heavy terminal applications.

## Requirements

- Go 1.23 or newer
- Node.js 22.6 or newer
- Google Chrome or Chromium for the default server-managed **Browser** workspace; set `KIWI_CODE_CHROME_BIN` when it is not in a standard location
- `tmux` on your `PATH` for persistent terminal sessions
- `git` on your `PATH` for branch controls and worktree-backed threads
- `nvim` and `lazygit` on your `PATH` for those tabs
- `pi`, `codex`, and/or `claude` on your `PATH` for their terminal-based coding-agent choices; the bundled Codex integration requires a plugin-capable Codex CLI
- macOS `/usr/bin/sandbox-exec` for the bundled Kiwi Sandbox permission boundary used by Pi
- For **Claude Code (with gpt)**, a running [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) instance with GPT/Codex credentials; Kiwi Code uses `http://127.0.0.1:8317` and the client key `sk-dummy` by default

If Neovim, Lazygit, Pi, Codex CLI, or Claude Code is unavailable, that terminal tab opens a shell with a short explanation instead.

Projects are grouped into profiles so personal, work, or client-specific project lists can be shown independently. Personal and Work profiles are available by default, and more profiles can be created from the profile picker beside **Projects**. New projects are added to the top of the visible profile; the project details sidebar can move a project to another profile. Existing projects are migrated to Personal. Each expanded project initially shows its five most recently prompted active threads; **Show more** reveals older threads. A working thread or one with an unread completion remains visible even when it falls outside that limit. Threads that have never been prompted use their creation time for recency. Drag the handles beside projects or threads to reorder their displayed order; that order is persisted and shared with every connected client. Press **Ctrl-F** anywhere, including inside a terminal, to fuzzy-find and open a project or thread across all profiles.

Each project contains threads. New projects start with one thread at the project root. For Git repositories, the new-thread screen defaults to an isolated Git worktree; users can still choose the project folder when shared files are intentional. Choose the agent, its model and thinking level, then the working directory and local base branch; Pi Native is selected by default, and Kiwi Code remembers each agent's last model and thinking choices separately for each project after a thread is created. The initial prompt is optional and accepts text plus pasted, dropped, or selected PNG, JPEG, GIF, and WebP images: supplying either opens the selected agent immediately with that task, while leaving both blank still opens the selected agent without sending a prompt. Large text pastes in the initial prompt and Pi Native composer collapse to Pi-style `[paste #1 +19 lines]` markers while retaining and sending their full text. Pi models are populated from the models available to the local Pi installation. Claude Code agents are opt-in on the Settings page: standard instances offer Claude's supported model aliases and effort levels, while GPT instances load only `gpt-*` model IDs from CLIProxyAPI's `/v1/models` response. New worktrees begin on a temporary `kiwi-code/thread-...` branch; the first Pi, Codex CLI, or Claude Code prompt gives both the thread and branch their task-specific names. Managed worktrees are stored beside Kiwi Code's project data by default; their base location can be changed from the Settings page. Changing it only affects new worktrees.

Archive a thread from its sidebar action to move it into the project's **Show more** group alongside older active threads; expand that group to open, restore, or immediately delete archived threads. Archived threads are retained for 30 days by default. The Settings page can change that period or set it to `0` to keep them forever. Kiwi Code checks at startup and once per hour, stops an expired thread's tmux sessions, and removes its record. Deleting a thread immediately still stops its sessions and detaches its managed worktree. Unattached worktrees are retained for 30 days by default, then removed only when `git status` reports no staged, unstaged, or untracked changes. Dirty worktrees are kept and checked again later, and cleanup never deletes their Git branches. The **Cleanup** page lists every archived thread and unattached worktree, its deletion-eligibility time, and worktrees currently protected by uncommitted changes or a failed status check.

Threads own their detached sessions in Kiwi Code's dedicated tmux server (`tmux -L kiwi-code`), so switching threads never attaches to another thread's processes. A collapsible details sidebar on the right shows the active thread and project; click the thread name there to rename it, or lock the title to prevent agents and automatic title generation from overriding it.

Each thread has a standalone Shell tmux session plus a shared tools session. Shell tmux windows appear as tabs beneath the tool selector, and the `+` button creates another shell window in that session. Neovim, Lazygit, and Pi run in the shared session as fixed windows named `nvim`, `lazygit`, and `pi`. If Neovim or Lazygit exits, its window is recreated automatically when the terminal reconnects.

The Process workspace shows zero or more agent-created process shells in that same shared tools session. There is no default Process shell. Agents create one shell per long-running server, watcher, or test loop through the process API, then read its tmux history, send input, interrupt it, or remove it through that API. Process tabs appear and disappear as agents manage those shells.

Project settings include a named local environment for managed worktrees. Each environment can define Default, macOS, Linux, and Windows setup and cleanup scripts, reusable variables, and toolbar actions. The platform-specific command overrides Default when present. Setup runs at the project root after a worktree is created; cleanup runs before a clean unattached worktree is removed. Actions appear in the workspace header and open their command in a persistent Process shell. Environment commands receive the configured variables plus project, thread, and worktree metadata through `CODEX_*` and `KIWI_CODE_*` variables.

The Settings page can install the bundled `kiwi-code-processes` and `kiwi-code-threads` Agent Skills into `~/.agents/skills/`. Their dependency-free `.mjs` helpers let agents manage persistent processes; list, create, rename, archive, restore, and close threads; start a selected coding agent with a model, thinking level, and initial prompt in a newly-created thread; and read bounded tmux output from Pi, Codex CLI, Claude Code, shell, tool, and process panes. New Pi sessions discover the installed skills automatically; an existing Pi session can load updates with `/reload`. Claude Code and Codex CLI launched through Kiwi Code receive the process skill directly from their bundled Kiwi Code plugins, so they do not depend on the global skill installation. New Claude sessions load plugin updates automatically; an existing session can use `/reload-plugins`. Codex picks up plugin updates in a newly launched session.

The status bar at the bottom shows the active Pi session’s current context-window utilization, with warning colors as it fills; immediately after compaction it shows that usage is recalculating until Pi receives a fresh response. An already-running terminal Pi session can load the bundled context reporter with `/reload`. For Git working directories, the same bar shows the checked-out branch. Its branch picker filters local branches, switches without discarding local changes, and can create a new branch from the current HEAD.

The coding-agent tab has a dropdown for switching between Pi, Pi Native, Codex CLI, and the Claude Code instances configured in Settings. Codex CLI keeps the user's normal `CODEX_HOME`, authentication, configuration, plugins, and session storage. Kiwi Code writes a namespaced managed profile and plugin cache beside them without changing the base `config.toml`, disables the unrelated ChatGPT in-app Browser plugin only in that profile, then launches with the profile, `--dangerously-bypass-approvals-and-sandbox`, and `--dangerously-bypass-hook-trust`; model and reasoning overrides selected for a new thread remain Codex CLI flags. Standard Claude instances launch with their configured `CLAUDE_CONFIG_DIR`, keeping account login and session state separate while using the same user settings, installed plugins, Kiwi Code skills and MCP integration, and permission-bypass configuration as the default Claude profile. GPT instances run the same Claude executable through CLIProxyAPI. Every terminal agent remains persistent per thread inside the fixed `pi` tmux window. Pi Native runs Pi's RPC mode as a saved per-thread conversation and renders its messages and tool timeline directly in React. After compaction, Pi Native keeps the full active-branch transcript visible—including messages no longer sent to the model—and places the persisted compaction summary at the retained-context boundary, before the recent messages that followed the summarized snapshot. It renders LaTeX math delimited by `\[...\]`, `$$...$$`, `\(...\)`, or `$...$` with KaTeX, while malformed expressions remain readable as source. Its activity monitor includes Pi's cumulative input, output, cache-read, cache-write, cache-hit, and cost totals, shows the current run phase and elapsed time, independently checks that the Pi process is answering state probes, records recent RPC lifecycle events, and can copy a diagnostic summary. Kiwi Code also persists Pi usage by session and shows each thread's usage. Optional per-thread token and USD limits are configured in Thread details and prevent further Pi prompts after either limit is reached. None of the agent choices changes the shell, editor, process, details-sidebar, or branch-bar surfaces.

Kiwi Code loads bundled Pi extensions into Pi sessions and bundled integration plugins into Codex CLI and Claude Code sessions. Claude Code (with gpt) points the same `claude` executable at CLIProxyAPI, uses the selected GPT model as the session default, maps Opus to `gpt-5.6-sol`, Sonnet to `gpt-5.6-terra`, and Haiku and small-model tasks to `gpt-5.6-luna`, and stores its session state under `<data-dir>/claude-code-gpt-profile` instead of the user's normal Claude profile. Before each GPT launch, Kiwi Code copies the normal profile's non-model user settings into that isolated profile and points Claude at the normal profile's plugin root, so enabled plugins and plugin updates are shared by both Claude modes. Proxy credentials, provider selection, model defaults, and model allowlists remain isolated and controlled by Kiwi Code. The bundled Kiwi Sandbox plugin is temporarily not loaded into Claude Code. The bundled Kiwi Code Codex and Claude plugins include lifecycle and thread-title hooks, process skills and an MCP server that exposes the thread's in-app `browser_*` tools. Pi, Pi Native, Codex CLI, and both Claude Code modes in the same thread intentionally share the same in-app browser session. Kiwi Code launches Claude Code with `--dangerously-skip-permissions` and suppresses Claude's bypass-mode warning for that session. Paths in the thread's Kiwi Sandbox `relatedProjects` configuration are expanded and passed to Codex CLI and Claude Code with `--add-dir`; Kiwi Sandbox enforcement itself is temporarily disabled in those harnesses. The Pi command line receives Kiwi Sandbox plus the bundled thread-title, activity, context, usage, and browser extension stack. After the first user message, Pi, Codex CLI, and both Claude Code modes use a short isolated Pi call with `openai-codex/gpt-5.6-luna` and low reasoning to generate a concise thread title. They update through the Kiwi Code API and, for a worktree thread, rename its managed branch to `kiwi-code/<title-slug>-<thread-id-prefix>`. Title generation uses Pi's normal authentication.

Kiwi Sandbox reads `~/.config/kiwi-sandbox/sandbox.json` followed by the project override at `<project>/.config/kiwi-sandbox.json`. Pi loads the `kiwi-sandbox-config` Agent Skill and exposes `/kiwi-sandbox-enable` and `/kiwi-sandbox-disable`; its toggles are session-scoped. The bundled Claude sandbox plugin remains available in the source tree but is temporarily not loaded by Kiwi Code. Claude and Codex launches still use the project override's `relatedProjects` entries through `--add-dir`.

The Pi extension and the Codex CLI and Claude Code plugins report agent lifecycle to Kiwi Code. A spinner appears beside the thread whose agent is working. When an agent settles, its unread completion dot appears on that thread. Opening the thread clears its completion indicator; if it was already open when the agent finished, the next interaction inside its workspace clears it.

When a tmux mouse selection finishes in any terminal, Kiwi Code copies it to the browser device's system clipboard as well as tmux's paste buffer. Middle-click paste therefore keeps working, and the same text is available to other applications. When the Pi terminal is focused, pasting a clipboard image with the browser's normal paste shortcut uploads it to a temporary file and inserts that path into Pi's editor, matching Pi's native clipboard-image flow. PNG, JPEG, GIF, and WebP images up to 50 MB are supported.

Sessions keep running when the browser disconnects or the Go server exits or restarts, and the next connection attaches to them. Kiwi Code checks at startup and once per hour and closes a thread's Shell and shared tools sessions after 24 hours without workspace use, tmux activity, attachment, or a new prompt; attached sessions and working coding agents are kept. Opening the thread again creates fresh sessions. The **Session log** page records the latest 500 automatic inactivity closures and their last-activity times. Deleting a thread also stops its standalone Shell session and shared tools session, including every agent-created process shell. The **tmux** page, opened from the sidebar above Settings, lists every persistent session and window in Kiwi Code's dedicated tmux server. Select a session or window to attach through a temporary linked view without stopping or renaming the underlying process.

## Run locally

```sh
make run
```

The server listens on every network interface. Open [http://127.0.0.1:4000](http://127.0.0.1:4000) locally, or use `http://<this-machine's-LAN-IP>:4000` from another device. To use a non-default CLIProxyAPI listener or client key, set `KIWI_CODE_CLIPROXY_BASE_URL` (the gateway root, without `/v1`) and `KIWI_CODE_CLIPROXY_API_KEY` before starting Kiwi Code; `CLIPROXY_API_BASE_URL` and `CLIPROXY_API_KEY` are accepted as compatibility fallbacks. Project, profile, settings, Pi usage, tmux inactivity closures, and pending worktree-cleanup metadata are stored under your operating system's user config directory in `kiwi-code/projects.json`, `kiwi-code/profiles.json`, `kiwi-code/settings.json`, `kiwi-code/thread-usage.json`, `kiwi-code/tmux-session-closures.json`, and `kiwi-code/orphaned-worktrees.json`. This command runs in production mode. When launched from a Git checkout, production mode refuses to start unless that checkout is on `main`; a deployed binary started outside a checkout is unaffected. The restart control beside Settings gracefully shuts down the current Go process while leaving persistent tmux sessions and their processes running. After that process has fully exited, the `make run` launcher rebuilds the frontend, compiles and starts a fresh backend, and the open browser reloads when the new instance is ready. A binary launched directly still requires an external supervisor to honor restart requests.

To launch the production app in an Electron window instead, run:

```sh
make run:desktop
```

The desktop target uses the same all-interface `0.0.0.0:4000` default as `make run`, while Electron connects locally over `127.0.0.1`. Other devices can open `http://<this-machine's-LAN-or-Tailscale-IP>:4000`. Its server runs through the same supervised `make run` launcher, so the in-app restart control replaces the backend without closing the desktop window. Quitting Electron or stopping the command with `Ctrl-C` shuts down the server. It uses the same production data and tmux server as `make run` and has the same `main`-branch requirement. Set `KIWI_CODE_ADDR=127.0.0.1:4000` when launching it to restrict access to this machine.

The **Backend** dropdown at the top of the project sidebar is available in both the normal web frontend and Electron. Choose **Add backend…** and enter another instance's HTTP or HTTPS origin, such as `http://workstation:4000`; a bare machine name defaults to HTTP port `4000`. Choices are saved in that frontend's browser storage. Switching reloads the current frontend and sends its API and WebSocket connections directly to the selected backend. An HTTPS frontend can only select an HTTPS backend because browsers block mixed active content.

The **Browser** workspace supports two implementations behind the same per-thread API. Normal web/server launches default to a server-managed headless Chrome process with one isolated ephemeral browser context per thread and an interactive projected stream for mouse, wheel, keyboard, paste, and viewport input. Desktop launches explicitly retain the separate sandboxed Electron `WebContentsView` and its native guest surface. Remote sites never run in the trusted Kiwi Code renderer. Terminal Pi, Pi Native, Codex CLI, Claude, and the visible workspace share the selected thread session; stopping it discards its site data. Select the implementation with `-browser-backend=headless|electron` or `KIWI_CODE_BROWSER_BACKEND`, and override Chrome discovery with `-chrome-binary` or `KIWI_CODE_CHROME_BIN`. Existing-profile Chrome integration is not supported. Both implementations can record the selected page as a bounded, video-only WebM without capturing Kiwi Code chrome, terminals, or other workspaces. Every recording requires a concise 2–12 word purpose title, remains available in the thread's Browser workspace for inline playback, range-based seeking, download, and deletion, and is retained for up to 24 hours subject to count and size limits. Agent-started recordings use an inactivity deadline so abandoned work is finalized automatically. The Pi and Claude variants of the bundled `kiwi-code-in-app-browser` skill run browser tasks in a separate agent context while sharing the thread profile; the Codex variant drives the same browser through its plugin MCP server. Pi progressively loads its `browser_*` tools through `browser_tool_search`. If `@dire-pi/chrome-devtools` or another `browser_*` extension is already installed, Kiwi Code leaves that extension active and prints a migration warning instead of loading ambiguous duplicate tools; disable the older package and run `/reload` to switch managed Pi sessions to the in-app backend.

## Development

Run the full development environment with:

```sh
make dev
```

Vite listens on port 5173 and the Go server listens independently on port 8080. Open [http://127.0.0.1:5173](http://127.0.0.1:5173) locally, or use `http://<this-machine's-LAN-IP>:5173` from another device. The browser calls the Go port directly; Vite does not proxy API or WebSocket traffic. Vite reloads the React frontend as its files change, and the Go development runner rebuilds and restarts the backend when `.go`, `go.mod`, or `go.sum` files change or the in-app restart control is used. Terminal panes reattach to their tmux sessions automatically after a backend restart.

Development mode cannot bind or target production port `4000` or use the canonical `kiwi-code` tmux socket. With no `--tmux-socket` option, the launcher derives a stable isolated socket name from the checkout path, so `make dev` cannot reach production sessions. Explicit socket names are still useful for parallel runs.

Choose distinct ports for a parallel development instance with command-line arguments. Agent and test instances must also use a unique tmux socket instead of the production `kiwi-code` server:

```sh
make dev DEV_ARGS="--vite-port 15173 --go-port 18080 --tmux-socket kcv-dev-a1"
# Equivalent direct npm invocation:
cd web && npm run dev:servers -- --vite-port 15173 --go-port 18080 --tmux-socket kcv-dev-a1
```

Use a fresh temporary `KIWI_CODE_DATA_DIR` as well when the parallel instance must not read or modify normal application data. Pass `--loopback` to the npm launcher for isolated validation that must bind both listeners to `127.0.0.1`. Pass `--add-current-directory` to seed that isolated store with the checkout root as a project; agent browser-validation runs use this automatically. Backend restarts reuse the seeded project and its initial thread.

To launch the development stack as a desktop app, run:

```sh
make dev:desktop
```

This starts the Go backend and Vite as separate processes, waits for both ports to become ready, and opens the Vite URL in Electron. Frontend hot reload and backend restart-on-change continue to work in the desktop window. Quit Electron or stop the command with `Ctrl-C` to shut down the stack.

Agents and other parallel callers can give each desktop stack unique ports and an isolated tmux server:

```sh
make dev:desktop DEV_ARGS="--vite-port 25173 --go-port 28080 --tmux-socket kcv-desktop-a1"
# Equivalent direct npm invocation:
cd web && npm run dev:desktop -- --vite-port 25173 --go-port 28080 --tmux-socket kcv-desktop-a1
```

To run only one side of the application, use `go run .` for the backend or `cd web && npm run dev` for Vite. A manually separated development backend must set `-mode development` and an isolated tmux socket. It can target a separate Vite port with `-allowed-origin-port`; for example: `VITE_KIWI_CODE_API_PORT=18080 npm run dev -- --port 15173` and `go run . -mode development -addr 0.0.0.0:18080 -allowed-origin-port 15173 -tmux-socket kcv-manual-a1`.

## Build and test

```sh
make build
make test
```

## Headless multi-client check

With a Kiwi Code server running, exercise the UI-state and tmux WebSockets without a browser:

```sh
make headless-test
```

After a health preflight, the client opens three simultaneous protocol-v1 state sockets, subscribes each one to `projects` and `agentActivity`, creates a temporary project and two threads, and verifies project/thread mutations plus Pi working heartbeats, finished, and idle snapshots on every socket. It then attaches three clients to one tmux session and one client to another, verifies fan-out and isolation, and confirms that thread and project deletion close the affected terminal streams.

Use `go run ./cmd/headless-client -help` for options. Pass `-skip-terminal` when tmux is unavailable. When checking a server on another machine, pass `-project-path` with an absolute directory that exists on the server.

`make build` compiles the frontend into the Go server's embedded static directory, then produces `bin/kiwi-code`. The production server listens on `0.0.0.0:4000` by default. Override it with `-addr` or `KIWI_CODE_ADDR`; override the project data location with `-data-dir` or `KIWI_CODE_DATA_DIR`. Runtime mode defaults to `production` and can be set with `-mode` or `KIWI_CODE_MODE`. Browser mode defaults to `headless`; use `-browser-backend`/`KIWI_CODE_BROWSER_BACKEND` and `-chrome-binary`/`KIWI_CODE_CHROME_BIN` to select its implementation and Chrome executable. The canonical tmux socket is `kiwi-code`; development, test, and agent instances must override it with `-tmux-socket` or `KIWI_CODE_TMUX_SOCKET`. Direct isolated development-server launches can pass `-add-current-directory` to ensure their working directory is present as a project at startup; production mode rejects this test-only convenience.

The restart API gracefully closes the current HTTP server and lets the application process terminate instead of re-executing the current binary. Persistent tmux sessions are not stopped. Supported production and development launchers wait for that process to exit completely before they build and launch the replacement; crashes and other nonzero exits are not treated as restart requests.

Kiwi Code does not enable tmux's native verbose logging, avoiding `tmux-client-*.log` and `tmux-server-*.log` diagnostic files during normal operation.

> **Security:** kiwi-code exposes terminal access and does not provide authentication. Backend switching intentionally accepts API and WebSocket clients from other HTTP(S) browser origins. Only use the all-interface default on a trusted network with trusted browser content, or bind it back to loopback with `KIWI_CODE_ADDR=127.0.0.1:4000`.

## Historical event-streaming reports

- [Architecture review before the fix](reports/event-streaming-review.html)
- [Implementation and verification report](reports/event-streaming-fix-report.html)

## API

- `GET /api/state` opens the UI-state WebSocket.
- `GET|PUT /api/settings` reads or updates application settings.
- `GET|POST /api/projects` lists or creates projects.
- `PATCH|DELETE /api/projects/{id}` updates or deletes a project.
- `POST /api/projects/{id}/threads` creates a thread, optionally in a Git worktree.
- `GET|PATCH|DELETE /api/projects/{id}/threads/{threadId}` reads, updates, or deletes a thread.
- `POST /api/projects/{id}/threads/{threadId}/coding-agent` starts the selected coding agent.
- `GET /api/projects/{id}/threads/{threadId}/terminal` opens a terminal WebSocket.
- `GET /api/projects/{id}/threads/{threadId}/pi/native` and `/claude/native` open native-agent WebSockets.
- Thread-scoped browser, Git branch, shell-window, process-shell, usage, sandbox, context-status, and activity endpoints support the corresponding workspace surfaces.
