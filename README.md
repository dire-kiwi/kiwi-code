# dire-mux

dire-mux is a local, terminal-first project workspace. Add folders from your machine, create threads beneath each project, then switch between a regular shell, Neovim, Lazygit, code-server, a process console, and Pi or Claude Code without leaving the browser.

The backend is written in Go and provides persistent project storage plus tmux-backed terminal sessions over WebSockets. The frontend uses Vite, React, Tailwind CSS, and xterm.js with the canvas renderer. JetBrains Mono and Nerd Font symbols are configured for icon-heavy terminal applications.

## Requirements

- Go 1.23 or newer
- Node.js 20 or newer
- `tmux` on your `PATH` for persistent terminal sessions
- `git` on your `PATH` for branch controls and worktree-backed threads
- `nvim` and `lazygit` on your `PATH` for those tabs
- `code-server` on your `PATH` for the desktop **Code** tab ([installation guide](https://coder.com/docs/code-server/install)); set `DIRE_MUX_CODE_SERVER_BIN` to an explicit executable path when needed
- `pi` and/or `claude` on your `PATH` for their terminal-based coding-agent choices
- For Claude Code, the `sandbox-exec@dire-agent-extensions` plugin enabled at user scope; Dire Mux skips Claude's native permission prompts and relies on this plugin's `PreToolUse` sandbox enforcement
- For **Claude Code (with gpt)**, a running [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) instance with GPT/Codex credentials; Dire Mux uses `http://127.0.0.1:8317` and the client key `sk-dummy` by default

If Neovim, Lazygit, Pi, or Claude Code is unavailable, that terminal tab opens a shell with a short explanation instead.

Projects are grouped into profiles so personal, work, or client-specific project lists can be shown independently. Personal and Work profiles are available by default, and more profiles can be created from the profile picker beside **Projects**. New projects are added to the visible profile; the project details sidebar can move a project to another profile. Existing projects are migrated to Personal. Each expanded project initially shows its five most recently prompted active threads; **Show more** reveals older threads. A working thread or one with an unread completion remains visible even when it falls outside that limit. Threads that have never been prompted use their creation time for recency. Drag the handles beside projects or threads to reorder their displayed order; that order is persisted and shared with every connected client. Press **Ctrl-F** anywhere, including inside a terminal, to fuzzy-find and open a project or thread across all profiles.

Each project contains threads. New projects start with one thread at the project root. For Git repositories, the new-thread screen defaults to an isolated Git worktree; users can still choose the project folder when shared files are intentional. Choose the agent, its model and thinking level, then the working directory and local base branch; Pi Native is selected by default, and Dire Mux remembers each agent's last model and thinking choices separately for each project after a thread is created. The initial prompt is optional and accepts text plus pasted, dropped, or selected PNG, JPEG, GIF, and WebP images: supplying either opens the selected agent immediately with that task, while leaving both blank still opens the selected agent without sending a prompt. Pi models are populated from the models available to the local Pi installation; Claude Code offers its supported model aliases and effort levels; Claude Code (with gpt) loads only `gpt-*` model IDs from CLIProxyAPI's `/v1/models` response. New worktrees begin on a temporary `dire-mux/thread-...` branch; the first Pi or Claude Code prompt gives both the thread and branch their task-specific names. Managed worktrees are stored beside Dire Mux's project data by default; their base location can be changed from the Settings page. Changing it only affects new worktrees.

Dire Mux workflows provide sub-agent orchestration without putting the control loop in a parent model's context. They are exposed through Pi only for now. The current human prompt can activate a run by containing `ultracode`, directly asking to use/run a workflow, or invoking a saved workflow command; session-scoped ultracode effort can also let Pi choose workflows for substantive turns. Broad work alone, older conversation text, extension-injected prompts, and subagent text do not activate one; the Pi extension gates tool calls and the server independently enforces the grant. Pi supports `/effort ultracode`, which combines xhigh reasoning with that session mode until effort changes. While composing a one-off prompt, Option/Alt+W dismisses an accidental `ultracode` keyword trigger. Claude Code's built-in Ultracode option remains available in both Claude modes and uses Claude's native workflow facilities; it does not activate or expose Dire Mux workflows.

A workflow is plain JavaScript with top-level `await` plus `agent()`, `pipeline()`, `parallel()`, `phase()`, `log()`, and structured JSON `args`. The script runs in a restricted Node process inside a named Process workspace shell on the Dire Mux server: it has no direct filesystem, shell, import, process, or network access, while each `agent()` call creates a visible Pi Native child thread that can own an isolated worktree and branch. Up to 16 children execute concurrently and 1,000 may be scheduled per run. Workflow status, bounded logs, child identities, script, and final JSON result are persisted for the 25 most recent runs per thread under Dire Mux's data directory; the scoped runner capability cannot invoke unrelated managed-agent APIs. Runs start in the background by default; Pi persists its watch and delivers completion back into the session, while the Dire Mux UI can inspect and manage retained runs. The Thread details sidebar groups agents by phase, opens retained child conversations, warns after 25 scheduled agents, and provides pause, resume, stop, and save controls. Pausing preserves completed values; resuming returns those cached values and restarts unfinished agents. Cancelling a wait never stops the run.

Any retained script can be saved as `/<name>` to the closest project `.claude/workflows/<name>.js` or the personal `$CLAUDE_CONFIG_DIR/workflows/` (default `~/.claude/workflows/`) directory. Project commands override personal commands, and the closest project definition wins in a monorepo. Pi registers those files as slash commands and can also run them with structured `args`. Direct `create_child_threads`-style tools and the general child-creation endpoint remain disabled so general-purpose orchestration is owned by this server-side workflow control plane. Global, project, and per-thread nesting and usage limits apply to every workflow and child.

Pi and Pi Native also honor Claude Code-compatible `context: fork` skill frontmatter. The bundled `run_forked_skill` tool renders the skill body and Claude-style `$ARGUMENTS`, indexed, positional, and named argument placeholders, then directly starts one isolated Pi Native child context sharing the current workspace. This narrow skill-fork path neither creates a workflow run nor requires `ultracode` or another workflow activation; workflows remain a separate orchestration feature. Explicit `/skill:<name>` invocations are routed to that tool, and model-invocable forked skills are identified separately from skills that Pi should load with `read`. The child inherits the parent Pi model and thinking level, and its settled conversation is retained for review. Optional `agent: Explore` and `agent: Plan` values add their read-only role instructions; `general-purpose` uses the normal worker. Other agent names are retained as role hints because Dire Mux workers remain Pi Native and do not load Claude custom-agent definitions. Claude Code uses its own native `context: fork` implementation.

The first-party `kiwi-code-planner` skill runs with the read-only Plan profile, investigates the requested work, and publishes a standalone Markdown implementation plan back to its parent thread. The 25 newest plans per thread are retained under Dire Mux's data directory, appear immediately in the right-hand Thread details sidebar, and can be downloaded there. Pi exposes `list_thread_plans` and `download_thread_plan` so a parent agent asked to execute a saved plan can load the exact backend copy into context before editing; `publish_thread_plan` is restricted to a live non-workflow fork child.

Child threads do not appear as top-level sidebar rows. While a child is open, a fork badge and child count on the parent remain visible and the adjacent disclosure button reveals its indented row with normal activity, archive, delete, and workspace controls. Once the agent closes a settled child, that row leaves the left sidebar and moves into the main thread's **Agent threads** list in the right details sidebar. Selecting an entry starts a temporary Pi Native process to reopen its saved conversation for review; the process stops after the last reviewer leaves while the child remains closed. Opening any child uses Pi Native, and its coding-agent picker is locked to that presentation. The native prompt, image, stop, model, thinking, and session-mutation controls are read-only because the parent agent manages the delegated run; tool-call details and the Pi activity monitor remain interactive. Completed sibling entries and a return-to-main-thread action remain available while reviewing a child. Deleting the main thread atomically removes its retained child records, stops every thread's sessions, and starts normal unattached-worktree cleanup for their managed worktrees.

Archive a thread from its sidebar action to move it into the project's **Show more** group alongside older active threads; expand that group to open, restore, or immediately delete archived threads. Archived threads are retained for 30 days by default. The Settings page can change that period or set it to `0` to keep them forever. Dire Mux checks at startup and once per hour, stops an expired thread's tmux sessions, and removes its record. Deleting a thread immediately still stops its sessions and detaches its managed worktree. Unattached worktrees are retained for 30 days by default, then removed only when `git status` reports no staged, unstaged, or untracked changes. Dirty worktrees are kept and checked again later, and cleanup never deletes their Git branches. The **Cleanup** page lists every archived thread and unattached worktree, its deletion-eligibility time, and worktrees currently protected by uncommitted changes or a failed status check.

Threads own their detached sessions in Dire Mux's dedicated tmux server (`tmux -L kiwi-code`), so switching threads never attaches to another thread's processes. Existing `dire-mux` servers are adopted through a compatibility socket alias, and existing `dire-mux-…` sessions remain linked while their live processes continue; no pane is restarted during migration. A collapsible details sidebar on the right shows the active thread and project; click the thread name there to rename it.

Each thread has a standalone Shell tmux session plus a shared tools session. Shell tmux windows appear as tabs beneath the tool selector, and the `+` button creates another shell window in that session. Neovim, Lazygit, and Pi run in the shared session as fixed windows named `nvim`, `lazygit`, and `pi`. If Neovim or Lazygit exits, its window is recreated automatically when the terminal reconnects.

The Process workspace shows zero or more agent-created process shells in that same shared tools session. There is no default Process shell. Agents create one shell per long-running server, watcher, or test loop through the process API, then read its tmux history, send input, interrupt it, or remove it through that API. Process tabs appear and disappear as agents manage those shells.

The Settings page controls dynamic-workflow enablement, the ultracode keyword trigger, workflow-size guidance, the global sub-agent nesting depth, archived-thread and unattached-worktree retention, and the application and terminal theme. The terminal font family and size, interface surfaces, cursor and selection colors, and all 16 ANSI colors can be customized. Dire Mux starts with its original Ghostty-inspired palette and JetBrains Mono terminal settings, persists overrides in `settings.json`, and loads them in every browser client.

The Settings page can install the bundled `kiwi-code-processes` and `dire-mux-threads` Agent Skills into `~/.agents/skills/`. Their dependency-free `.mjs` helpers let agents manage persistent processes; list, create, rename, archive, restore, and close threads; and read bounded tmux output from Pi, Claude Code, shell, tool, and process panes. The process skill declares `context: fork`, so process-management tasks run in a separate agent context. New Pi sessions discover the installed skills automatically; an existing Pi session can load updates with `/reload`. Claude Code launched through Dire Mux receives the process skill directly from the bundled Dire Mux plugin, so it does not depend on the global skill installation. New Claude sessions load plugin updates automatically; an existing session can use `/reload-plugins`.

The status bar at the bottom shows the active Pi session’s current context-window utilization, with warning colors as it fills; immediately after compaction it shows that usage is recalculating until Pi receives a fresh response. An already-running terminal Pi session can load the bundled context reporter with `/reload`. For Git working directories, the same bar shows the checked-out branch. Its branch picker filters local branches, switches without discarding local changes, and can create a new branch from the current HEAD.

The coding-agent tab has a dropdown for switching between Pi, Pi Native, Claude Code, and Claude Code (with gpt). Every terminal agent remains persistent per thread inside the fixed `pi` tmux window. Pi Native runs Pi's RPC mode as a saved per-thread conversation and renders its messages and tool timeline directly in React. After compaction, Pi Native keeps the full active-branch transcript visible—including messages no longer sent to the model—and marks the persisted compaction summary in place. It renders LaTeX math delimited by `\[...\]`, `$$...$$`, `\(...\)`, or `$...$` with KaTeX, while malformed expressions remain readable as source. Its activity monitor includes Pi's cumulative input, output, cache-read, cache-write, cache-hit, and cost totals, shows the current run phase and elapsed time, independently checks that the Pi process is answering state probes, records recent RPC lifecycle events, and can copy a diagnostic summary. Dire Mux also persists Pi usage by session and shows both a thread's own usage and a main-thread total that includes every open or completed child. Optional per-thread token and USD limits are configured in Thread details; main-thread limits use the combined total and prevent further Pi prompts and child creation after either limit is reached. None of the agent choices changes the shell, editor, process, details-sidebar, or branch-bar surfaces.

Dire Mux loads bundled Pi extensions into Pi sessions and a bundled integration plugin into Claude Code sessions. Claude Code (with gpt) points the same `claude` executable at CLIProxyAPI, uses the selected GPT model as the session default, maps Opus to `gpt-5.6-sol`, Sonnet to `gpt-5.6-terra`, and Haiku and small-model tasks to `gpt-5.6-luna`, and stores its session state under `<data-dir>/claude-code-gpt-profile` instead of the user's normal Claude profile. Before each GPT launch, Dire Mux copies the normal profile's non-model user settings into that isolated profile and points Claude at the normal profile's plugin root, so enabled plugins and plugin updates are shared by both Claude modes. Proxy credentials, provider selection, model defaults, and model allowlists remain isolated and controlled by Dire Mux. The sandbox plugin is also loaded explicitly as the permission boundary. The bundled Dire Mux Claude plugin includes lifecycle and thread-title hooks, the process skill, and an MCP server that exposes the thread's in-app `browser_*` tools; it does not expose Dire Mux workflow skills, hooks, or MCP tools. Claude's own built-in Ultracode and workflow features remain available. Pi, Pi Native, and both Claude Code modes in the same thread intentionally share the same in-app browser session. Dire Mux launches Claude Code with `--dangerously-skip-permissions` and suppresses Claude's bypass-mode warning for that session; the separately installed sandbox-exec plugin still runs its `PreToolUse` hooks and remains the permission boundary. The Pi command line receives the bundled thread-title, activity, context, workflow, forked-skill, and child-thread compatibility extension stack. Child relationship metadata is not passed to Claude Code, direct child-thread tools remain disabled, and Dire Mux workflow workers remain Pi Native. After the first user message, Pi and both Claude Code modes use a short isolated Pi call with `openai-codex/gpt-5.6-luna` and low reasoning to generate a concise thread title. They update through the Dire Mux API and, for a worktree thread, rename its managed branch to `dire-mux/<title-slug>-<thread-id-prefix>`. Title generation uses Pi's normal authentication.

The Pi extension and Claude Code plugin both report agent lifecycle to Dire Mux. A spinner appears beside the thread whose agent is working. When an agent settles, its unread completion dot appears only on the top-level root thread for that thread tree, including for deeply nested child agents. Opening the root thread clears its completion indicators. If that root was already open when the agent finished, the next interaction inside its workspace clears them.

When a tmux mouse selection finishes in any terminal, Dire Mux copies it to the browser device's system clipboard as well as tmux's paste buffer. Middle-click paste therefore keeps working, and the same text is available to other applications. When the Pi terminal is focused, pasting a clipboard image with the browser's normal paste shortcut uploads it to a temporary file and inserts that path into Pi's editor, matching Pi's native clipboard-image flow. PNG, JPEG, GIF, and WebP images up to 50 MB are supported.

All sessions keep running when the browser disconnects or the Go server exits or restarts, and the next connection attaches to them. Deleting a thread stops its standalone Shell session and shared tools session, including every agent-created process shell. The **tmux** page, opened from the sidebar above Settings, lists every persistent session and window in Dire Mux's dedicated tmux server. Select a session or window to attach through a temporary linked view without stopping or renaming the underlying process.

## Run locally

```sh
make run
```

The server listens on every network interface. Open [http://127.0.0.1:4000](http://127.0.0.1:4000) locally, or use `http://<this-machine's-LAN-IP>:4000` from another device. To use a non-default CLIProxyAPI listener or client key, set `DIRE_MUX_CLIPROXY_BASE_URL` (the gateway root, without `/v1`) and `DIRE_MUX_CLIPROXY_API_KEY` before starting Dire Mux; `CLIPROXY_API_BASE_URL` and `CLIPROXY_API_KEY` are accepted as compatibility fallbacks. Project, profile, settings, Pi usage, and pending worktree-cleanup metadata are stored under your operating system's user config directory in `dire-mux/projects.json`, `dire-mux/profiles.json`, `dire-mux/settings.json`, `dire-mux/thread-usage.json`, and `dire-mux/orphaned-worktrees.json`. This command runs in production mode. When launched from a Git checkout, production mode refuses to start unless that checkout is on `main`; a deployed binary started outside a checkout is unaffected. The restart control beside Settings gracefully shuts down the current Go process while leaving persistent tmux sessions and their processes running. After that process has fully exited, the `make run` launcher rebuilds the frontend, compiles and starts a fresh backend, and the open browser reloads when the new instance is ready. A binary launched directly still requires an external supervisor to honor restart requests.

To launch the production app in an Electron window instead, run:

```sh
make run:desktop
```

The desktop target uses the same all-interface `0.0.0.0:4000` default as `make run`, while Electron connects locally over `127.0.0.1`. Other devices can open `http://<this-machine's-LAN-or-Tailscale-IP>:4000`. Its server runs through the same supervised `make run` launcher, so the in-app restart control replaces the backend without closing the desktop window. Quitting Electron or stopping the command with `Ctrl-C` shuts down the server. It uses the same production data and tmux server as `make run` and has the same `main`-branch requirement. Set `DIRE_MUX_ADDR=127.0.0.1:4000` when launching it to restrict access to this machine.

The **Backend** dropdown at the top of the project sidebar is available in both the normal web frontend and Electron. Choose **Add backend…** and enter another instance's HTTP or HTTPS origin, such as `http://workstation:4000`; a bare machine name defaults to HTTP port `4000`. Choices are saved in that frontend's browser storage. Switching reloads the current frontend and sends its API, event-stream, and WebSocket connections directly to the selected backend. An HTTPS frontend can only select an HTTPS backend because browsers block mixed active content.

The **Browser** workspace uses a separate sandboxed Electron `WebContentsView`; remote sites never run in the trusted Dire Mux renderer. Each thread has its own ephemeral browser profile shared by terminal Pi, Pi Native, and the visible Browser workspace. Stopping a session discards that profile. Native desktop clients show the live guest surface only for the backend paired with that Electron process. When another backend is selected, the workspace uses that backend's projected preview, like an ordinary Chrome/LAN client. The bundled `dire-mux-in-app-browser` skill declares `context: fork`, so browser tasks run in a separate agent context while sharing the thread's browser profile; its Pi variant exposes progressively loaded `browser_*` tools through `browser_tool_search`. Existing-profile Chrome integration is not part of this backend. If `@dire-pi/chrome-devtools` or another `browser_*` extension is already installed, Dire Mux leaves that extension active and prints a migration warning instead of loading ambiguous duplicate tools; disable the older package and run `/reload` to switch managed Pi sessions to the in-app backend.

The desktop **Code** workspace lazily starts one local `code-server` process on a dynamic loopback port, authenticates it with a private random password, and displays the active thread folder in a separate sandboxed `WebContentsView`. Editor settings and extensions live in the Electron profile, while the active thread opens its own editor view and folder URL. The native editor is available only for the backend paired with that Electron process; other backends show an explanation instead of opening a remote path on the local machine. The process stops when Dire Mux Desktop exits. Ordinary browser and LAN clients likewise show a desktop-only explanation instead of exposing the privileged editor service.

## Development

Run the full development environment with:

```sh
make dev
```

Vite listens on port 5173 and the Go server listens independently on port 8080. Open [http://127.0.0.1:5173](http://127.0.0.1:5173) locally, or use `http://<this-machine's-LAN-IP>:5173` from another device. The browser calls the Go port directly; Vite does not proxy API, event-stream, or WebSocket traffic. Vite reloads the React frontend as its files change, and the Go development runner rebuilds and restarts the backend when `.go`, `go.mod`, or `go.sum` files change or the in-app restart control is used. Terminal panes reattach to their tmux sessions automatically after a backend restart.

Development mode cannot bind or target production port `4000`, or use the canonical `kiwi-code` tmux socket or legacy production socket `dire-mux`. With no `--tmux-socket` option, the launcher derives a stable isolated socket name from the checkout path, so `make dev` cannot reach production sessions. Explicit socket names are still useful for parallel runs.

Choose distinct ports for a parallel development instance with command-line arguments. Agent and test instances must also use a unique tmux socket instead of the production `kiwi-code` or legacy `dire-mux` server:

```sh
make dev DEV_ARGS="--vite-port 15173 --go-port 18080 --tmux-socket dmv-dev-a1"
# Equivalent direct npm invocation:
cd web && npm run dev:servers -- --vite-port 15173 --go-port 18080 --tmux-socket dmv-dev-a1
```

Use a fresh temporary `DIRE_MUX_DATA_DIR` as well when the parallel instance must not read or modify normal application data. Pass `--loopback` to the npm launcher for isolated validation that must bind both listeners to `127.0.0.1`. Pass `--add-current-directory` to seed that isolated store with the checkout root as a project; agent browser-validation runs use this automatically. Backend restarts reuse the seeded project and its initial thread.

To launch the development stack as a desktop app, run:

```sh
make dev:desktop
```

This starts the Go backend and Vite as separate processes, waits for both ports to become ready, and opens the Vite URL in Electron. Frontend hot reload and backend restart-on-change continue to work in the desktop window. Quit Electron or stop the command with `Ctrl-C` to shut down the stack.

Agents and other parallel callers can give each desktop stack unique ports and an isolated tmux server:

```sh
make dev:desktop DEV_ARGS="--vite-port 25173 --go-port 28080 --tmux-socket dmv-desktop-a1"
# Equivalent direct npm invocation:
cd web && npm run dev:desktop -- --vite-port 25173 --go-port 28080 --tmux-socket dmv-desktop-a1
```

To run only one side of the application, use `go run .` for the backend or `cd web && npm run dev` for Vite. A manually separated development backend must set `-mode development` and an isolated tmux socket. It can target a separate Vite port with `-allowed-origin-port`; for example: `VITE_DIRE_MUX_API_PORT=18080 npm run dev -- --port 15173` and `go run . -mode development -addr 0.0.0.0:18080 -allowed-origin-port 15173 -tmux-socket dmv-manual-a1`.

## Build and test

```sh
make build
make test
```

## Headless multi-client check

With a Dire Mux server running, exercise the global event stream and tmux WebSocket routing without a browser:

```sh
make headless-test
```

After a health preflight, the client opens three simultaneous global event consumers, creates a temporary project and two threads, and verifies project/thread mutations plus Pi working heartbeats, finished, and idle snapshots on every consumer. It then attaches three clients to one tmux session and one client to another, verifies fan-out and isolation, and confirms that thread and project deletion close the affected terminal streams.

Use `go run ./cmd/headless-client -help` for options. Pass `-skip-terminal` when tmux is unavailable. When checking a server on another machine, pass `-project-path` with an absolute directory that exists on the server.

`make build` compiles the frontend into the Go server's embedded static directory, then produces `bin/dire-mux`. The production server listens on `0.0.0.0:4000` by default. Override it with `-addr` or `DIRE_MUX_ADDR`; override the project data location with `-data-dir` or `DIRE_MUX_DATA_DIR`. Runtime mode defaults to `production` and can be set with `-mode` or `DIRE_MUX_MODE`. The canonical tmux socket is `kiwi-code`; development, test, and agent instances must override it with `-tmux-socket` or `DIRE_MUX_TMUX_SOCKET` and may not use the legacy production name `dire-mux`. Direct isolated development-server launches can pass `-add-current-directory` to ensure their working directory is present as a project at startup; production mode rejects this test-only convenience.

The restart API gracefully closes the current HTTP server and lets the application process terminate instead of re-executing the current binary. Persistent tmux sessions are not stopped. Supported production and development launchers wait for that process to exit completely before they build and launch the replacement; crashes and other nonzero exits are not treated as restart requests.

Dire Mux enables tmux's native verbose logs for server-creating commands and long-lived terminal and control clients. Logs are written as `tmux-server-PID.log` and `tmux-client-PID.log` under `<data-dir>/tmux-logs/<tmux-socket>/`. An already-running tmux server cannot be switched to that directory, so its server log begins with the next server incarnation; newly attached clients log immediately. Treat these diagnostics as sensitive because they can contain terminal and command metadata.

> **Security:** dire-mux exposes terminal access and does not provide authentication. Backend switching intentionally accepts API and WebSocket clients from other HTTP(S) browser origins. Only use the all-interface default on a trusted network with trusted browser content, or bind it back to loopback with `DIRE_MUX_ADDR=127.0.0.1:4000`.

## Event-streaming reports

- [Architecture review before the fix](reports/event-streaming-review.html)
- [Implementation and verification report](reports/event-streaming-fix-report.html)

## API

- `GET /api/health` returns the current application instance identifier and health status.
- `POST /api/restart` gracefully exits the current application process and requests a fresh instance from its launcher. Persistent tmux sessions keep running.
- `GET /api/settings` returns the effective/default worktree base locations, archived-thread and unattached-worktree retention in days, the global and maximum sub-agent nesting depths, and themes.
- `GET /api/cleanup` returns archived threads and unattached worktrees with their scheduled deletion-eligibility times and current uncommitted-change status.
- `PUT /api/settings` updates any supplied setting. Send `{"worktreeBasePath":""}` to restore the default worktree location, use `0` retention days to disable that automatic deletion, set `subAgentNestingDepth` from `0` through `4`, or send the returned `defaultTheme` as `theme` to restore the default appearance.
- `GET /api/settings/agent-skills` reports aggregate and per-skill installation status for the bundled process and thread skills.
- `GET /api/coding-agents` lists Pi, Claude Code, and Claude Code (with gpt) model and thinking-level choices; the GPT choice is populated from CLIProxyAPI and excludes non-`gpt-*` IDs. Pass `projectId` to discover Pi capabilities from that project's cwd while respecting Pi trust. This safe GET does not pass `--approve`, so merely opening a form cannot execute an unapproved project-local extension; explicit materialized and already trusted/global providers remain discoverable. Each explicit Pi model includes its exact advertised `reasoningLevels`; RPC discovery fails closed rather than synthesizing levels from `--list-models`.
- `POST /api/settings/agent-skills` installs or updates both skills under `~/.agents/skills/`.
- `GET /api/profiles` lists profiles; Personal and Work are always available.
- `POST /api/profiles` creates a profile.
- `GET /api/tmux/sessions` lists persistent sessions and windows in the dedicated `kiwi-code` tmux server (including linked legacy sessions during migration); temporary browser-view sessions are omitted.
- `GET /api/tmux/terminal?session=…&window=…` upgrades to a PTY WebSocket attached to the selected existing window through an isolated linked view.
- `GET /api/projects` lists saved projects.
- `GET /api/filesystem/directories?path=…` returns matching local directories for project-path autocomplete.
- `GET /api/events` streams independently ordered, named `projects`, `profiles`, `pi-activity`, and `thread-usage` snapshots to every connected client. This is the browser's primary global status stream. A stalled consumer is disconnected before its bounded backlog can grow indefinitely; EventSource reconnects and receives authoritative initial snapshots.
- `GET /api/projects/events` is the legacy project-only snapshot stream.
- `POST /api/projects` adds an existing local directory, optionally using `profileId` (Personal by default).
- `PUT /api/projects/order` replaces one profile's project order using `{"profileId":"personal","projectIds":["…"]}`.
- `PATCH /api/projects/{id}` updates the supplied project fields. Use `profileId` to move profiles, an integer `subAgentNestingDepthOverride` from `0` through `4` for a project-specific delegation limit, or `null` to inherit the global setting.
- `DELETE /api/projects/{id}` stops all of its tmux sessions and removes it from the list without deleting the project folder; each managed worktree becomes eligible for the configured clean-worktree cleanup.
- `GET /api/projects/{id}/git/branches` returns the project's current branch and local branches for worktree creation.
- `POST /api/projects/{id}/threads` creates a top-level thread. Pass `{"worktree":true,"baseBranch":"main"}` to create an isolated Git worktree from a local branch; `baseBranch` defaults to `HEAD`, `nestedDepth` can cap the number of child-agent generations below this thread without exceeding the project limit, and `title` is optional because the coding agents name new threads from their first prompt. The browser's new-thread form selects a worktree and Pi Native by default for Git projects, opens the configured agent after creation, and sends an initial task only when a prompt or image is supplied. This endpoint rejects `parentThreadId`; child relationships are created only by the scoped workflow runner or forked-skill route described below.
- `POST /api/projects/{id}/threads/{threadId}/workflows/activation` records or clears a short-lived current-prompt grant after validating a managed human input source and the `ultracode`/direct-request/saved-command rules. `POST .../workflows` requires that grant, validates and persists a script plus optional JSON `args`, then starts its restricted Node runner in a dedicated Process shell. `GET .../workflows` lists retained runs, `GET .../workflows/{runId}` reads one run, and `POST .../{runId}/pause|resume|stop` manages its exact process while keeping pause distinct from permanent stop. `POST .../{runId}/save` writes a reusable project or personal `.claude/workflows/*.js` command. `GET .../workflows/saved` resolves saved-command precedence and `POST .../workflows/commands/run/{name}` runs one with structured args. Start, activation, saved discovery, and saved execution require the managed-agent capability; browser run-management and save actions are available through the local Dire Mux UI. Runner event and agent routes use a separate per-run capability and are not general client APIs.
- `POST /api/projects/{id}/threads/{threadId}/children` remains disabled for general direct managed-agent calls and returns `503 Service Unavailable`. The server-owned workflow agent route reuses this child transaction after validating a scoped workflow capability; workers remain Pi Native.
- `POST /api/projects/{id}/threads/{threadId}/skill-forks` directly starts the one shared-workspace Pi Native child requested by `run_forked_skill`. It requires the private Pi agent capability but deliberately does not consult workflow settings or workflow activation, and it creates no workflow record. `POST .../skill-forks/{childId}/stop` stops and retains that child when the parent tool call is cancelled.
- `GET /api/projects/{id}/threads/{threadId}/children` lists direct, still-open children and their latest Pi run.
- `POST /api/projects/{id}/threads/{threadId}/children/{childId}/close` stops a settled child's Pi process and persists `closedAt` while retaining its thread, worktree, and saved conversation for review. It requires the private Pi agent capability.
- `GET /api/projects/{id}/threads/{threadId}/children/{childId}/runs/{runId}` returns a tracked child run as `starting`, `working`, `finished`, or `failed`, including its bounded final assistant output.
- `POST /api/projects/{id}/threads/{threadId}/messages` sends a message to a direct parent or child, and `POST .../messages/receive` atomically drains a Pi thread's pending inbox. Both require the private Pi agent capability.
- `POST /api/projects/{id}/threads/{threadId}/plans` accepts a title and standalone Markdown body from a capability-authenticated context-fork child and durably associates the plan with its immediate parent thread. `GET .../plans` lists retained metadata newest-first, resolving a child request to its parent, and `GET .../plans/{planId}` downloads the exact Markdown with an attachment filename. Plans are removed with their parent thread or project.
- `PUT /api/projects/{id}/threads/order` replaces the project's thread order using `{"threadIds":["…"]}`.
- `GET /api/projects/{id}/threads/{threadId}` returns one thread.
- `PATCH /api/projects/{id}/threads/{threadId}` updates its title or archive state. Send `{"archived":true}` to archive it and `{"archived":false}` to restore it. The coding-agent integrations send `{"title":"…","autoGenerated":true}` so the generated title is applied only once and a managed worktree branch is renamed with it.
- `PUT /api/projects/{id}/threads/{threadId}/limits` sets optional budgets with `{"tokenLimit":100000,"costLimitUsd":5}`; either value may be `null` for no limit. A main thread's limit includes all descendants.
- `GET /api/thread-usage` returns the same authoritative own, child, and combined totals sent in the `thread-usage` event. Agent-only usage and budget endpoints accept cumulative Pi session reports and enforce limits.
- `DELETE /api/projects/{id}/threads/{threadId}` stops all of the thread's tmux sessions, removes its record, and starts the unattached-worktree retention period for a managed worktree. Deleting a main thread first deletes all of its child threads through the same cleanup path.
- `GET /api/pi/activity` returns the backward-compatible aggregate snapshot of active and completed coding-agent activity.
- `GET /api/pi/activity/events` is the legacy activity-only snapshot stream.
- `PUT /api/projects/{id}/threads/{threadId}/pi/activity` receives Pi lifecycle updates. A working update can include `promptStartedAt`; bundled integrations repeat the same timestamp on heartbeats so the persisted thread recency advances only once per user prompt.
- `PUT /api/projects/{id}/threads/{threadId}/claude/activity` receives Claude Code lifecycle updates, uses the same optional `promptStartedAt`, and distinguishes the optional `agent: "claude-gpt"` source.
- `DELETE /api/projects/{id}/threads/{threadId}/pi/activity` acknowledges completed coding-agent runs for the thread.
- `PUT /api/projects/{id}/threads/{threadId}/context/status` receives a managed Pi presentation’s current context token estimate, window, percentage, and model. Pi’s bundled terminal extension reports here; Pi Native reads the equivalent `contextUsage` directly from its RPC session statistics.
- `GET /api/projects/{id}/threads/{threadId}/events` streams named `thread-status` snapshots for Pi context status, Git branches, process shells, Shell tmux windows, retained plans, and workflow progress. Dire Mux mutations wake the stream immediately, a shared tmux control-mode client reports window and command changes, and repository reconciliation detects Git changes made directly in a terminal without browser polling.
- `GET /api/projects/{id}/threads/{threadId}/browser` returns the thread's in-app browser status and tabs.
- `POST /api/projects/{id}/threads/{threadId}/browser/actions` forwards bounded session, tab, navigation, semantic page, screenshot, and allowlisted page-CDP operations to the authenticated loopback Electron provider.
- `GET /api/projects/{id}/threads/{threadId}/browser/frame` returns a no-store JPEG preview for clients that cannot host the native guest surface.
- `GET /api/projects/{id}/threads/{threadId}/git/branches` returns the current branch and local branches.
- `POST /api/projects/{id}/threads/{threadId}/git/branches` creates and checks out a branch.
- `POST /api/projects/{id}/threads/{threadId}/git/branches/switch` checks out an existing local branch.
- `GET /api/projects/{id}/threads/{threadId}/terminal?tool=terminal|nvim|lazygit|pi` upgrades a fixed tool to a PTY WebSocket. With `tool=pi`, pass `agent=pi|claude|claude-gpt` to select the coding agent and optional `model`, `thinking`, and `prompt` values to configure and start a newly launched process; child threads reject `tool=pi` because their managed agent is exposed through read-only Pi Native. Use `tool=process&processId=...` for an agent-created process shell.
- `GET /api/projects/{id}/threads/{threadId}/pi/native` upgrades to the Pi Native RPC WebSocket. Prompt commands can include temporary image references returned by the image-upload endpoint; the server validates and converts them to Pi RPC image content. In addition to prompts and session controls, clients can send `get_state` to verify process responsiveness without changing the conversation, or `reload`/`restart` to replace the Pi RPC process, reload extensions, and resume the saved conversation. Refreshes also read Pi's append-only session entries and emit the renderable messages on the current branch as `pi_native_history`, preserving pre-compaction display history without adding it back to model context or exposing abandoned branches and extension state. Browser WebSockets for child threads accept only read-only refresh, state, command, model-list, and session-stat queries; the parent agent owns child-run mutations.
- `GET` / `POST /api/projects/{id}/threads/{threadId}/shell/windows` lists or creates Shell tmux windows.
- `POST /api/projects/{id}/threads/{threadId}/shell/windows/{index}/select` selects a Shell tmux window.
- `GET` / `POST /api/projects/{id}/threads/{threadId}/processes` lists or creates agent process shells.
- `GET /api/projects/{id}/threads/{threadId}/processes/{processId}/logs` captures bounded process output.
- `GET /api/projects/{id}/threads/{threadId}/terminal/lines?tool=…` captures bounded tmux history without creating a session. Use `agent=pi|claude|claude-gpt` for the coding-agent window, `processId=...` for a process, and optional `window=...` for a Shell window.
- `POST /api/projects/{id}/threads/{threadId}/processes/{processId}/input` sends literal input and optional Enter.
- `POST /api/projects/{id}/threads/{threadId}/processes/{processId}/interrupt` sends Ctrl-C.
- `DELETE /api/projects/{id}/threads/{threadId}/processes/{processId}` stops and removes a process shell.
- `POST /api/projects/{id}/pi/images` validates and stores a pasted image temporarily for the Pi terminal editor or a Pi Native prompt.
