---
name: dire-mux-browser-validation
description: Builds and starts this Dire Mux application on isolated loopback ports, a temporary data directory, and a unique tmux server, then validates completed changes through a real browser.
compatibility: Requires Node.js, Go, tmux, the kiwi-code-processes skill, and the Chrome DevTools browser capability.
---

# Dire Mux browser validation

Validate completed changes in a real browser against a fresh instance from the current worktree. Do not reuse the developer's normal Dire Mux application or tmux server: parallel agents may be running, and their data, ports, and terminal sessions belong to the user.

## Rules

- Run the relevant automated tests first. Browser validation supplements tests; it does not replace them.
- Build and serve the current worktree, not `main`, another worktree, or an already-running Dire Mux instance.
- Allocate a fresh loopback port for every listener in every run. A split Vite/Go stack requires two different fresh ports. Never use production port `4000`, never assume `8080` or `5173` is available, and never stop an unrelated port owner.
- Bind every validation listener to `127.0.0.1` and use a fresh temporary data directory.
- Allocate a unique, short tmux socket name for every run and pass it to the application with `-tmux-socket` or `--tmux-socket`.
- The canonical `kiwi-code` tmux server and legacy `dire-mux` server are reserved for the user's production environment. Never connect to either one, list it, create sessions in it, or kill it during validation.
- Before startup, explicitly check that the generated tmux socket is non-empty and is neither `kiwi-code` nor `dire-mux`. If that check fails, stop and generate another name.
- Use the `kiwi-code-processes` skill for the validation process. Do not use `&`, `nohup`, or an unmanaged tmux session.
- Use the Chrome DevTools browser capability to open and interact with the rendered application. A successful `curl` or health request alone is not browser validation.
- Exercise the user-visible behavior changed by the task, including meaningful interactions and resulting state. Do not stop at confirming that the landing page renders.
- Check for uncaught console errors and failed relevant network requests. Distinguish pre-existing or unrelated noise from regressions caused by the change.
- Do not modify real repositories through the validation instance unless the scenario requires it and the operation is known to be safe. Prefer temporary fixture directories.
- Stop only the process ID created by this workflow, close validation tabs, kill only the generated tmux server, and remove only the temporary directories created for this run.

## 1. Define the validation plan

Inspect the diff and identify the smallest set of browser journeys that proves the change works. Include:

- the route or screen to open;
- the controls to operate;
- the expected visible and persisted result;
- any loading, empty, error, narrow-screen, or reconnect state affected by the change; and
- whether the embedded Go app or the split Vite/Go development stack is required.

For non-visual backend changes, use the browser through the UI path that consumes the changed API or WebSocket behavior.

## 2. Build the browser application

Run the frontend tests and build before validation:

```bash
cd web
npm install
npm test
npm run build
cd ..
```

The build is required for the embedded Go application. It also catches type and production-bundle failures before validating the split development stack. If it fails, fix or report it before browser validation.

## 3. Allocate isolated ports, data, and tmux identity

From the repository root, allocate two fresh loopback ports, a temporary data directory, and a short random tmux socket:

```bash
fresh_port() {
  node -e 'const net=require("node:net"); const server=net.createServer(); server.listen(0,"127.0.0.1",()=>{ process.stdout.write(String(server.address().port)); server.close(); });'
}
VALIDATION_GO_PORT="$(fresh_port)"
VALIDATION_VITE_PORT="$(fresh_port)"
while [ "$VALIDATION_VITE_PORT" = "$VALIDATION_GO_PORT" ] || \
      [ "$VALIDATION_VITE_PORT" = "4000" ] || \
      [ "$VALIDATION_GO_PORT" = "4000" ]; do
  VALIDATION_GO_PORT="$(fresh_port)"
  VALIDATION_VITE_PORT="$(fresh_port)"
done
VALIDATION_DATA_DIR="$(mktemp -d "${TMPDIR:-/tmp}/dire-mux-browser-validation.XXXXXX")"
VALIDATION_SUFFIX="$(node -e 'process.stdout.write(require("node:crypto").randomBytes(4).toString("hex"))')"
VALIDATION_TMUX_SOCKET="dmv-${VALIDATION_GO_PORT}-${VALIDATION_SUFFIX}"
if [ -z "$VALIDATION_TMUX_SOCKET" ] || \
   [ "$VALIDATION_TMUX_SOCKET" = "kiwi-code" ] || \
   [ "$VALIDATION_TMUX_SOCKET" = "dire-mux" ]; then
  echo "Refusing to use a production Dire Mux tmux server" >&2
  exit 1
fi
printf 'Go URL: %s\nVite URL: %s\nData: %s\ntmux socket: %s\n' \
  "http://127.0.0.1:$VALIDATION_GO_PORT" \
  "http://127.0.0.1:$VALIDATION_VITE_PORT" \
  "$VALIDATION_DATA_DIR" \
  "$VALIDATION_TMUX_SOCKET"
```

Keep all four literal values in context. Shell variables do not persist between agent tool calls. If a selected port is taken before startup, allocate a replacement rather than killing its owner. Never substitute `kiwi-code` or `dire-mux` for the generated socket.

## 4. Start the validation application

Load and follow the `kiwi-code-processes` skill. Start one uniquely named process shell with the literal values printed above.

For the embedded production-style application, keep the runtime in development mode because validation is running from a feature checkout:

```bash
node "$HOME/.agents/skills/kiwi-code-processes/scripts/start-process.mjs" \
  "browser-validation-<go-port>" \
  "go run . -mode development -addr 127.0.0.1:<go-port> -data-dir '<data-dir>' -tmux-socket '<tmux-socket>' -add-current-directory"
```

For behavior involving the split development stack, start Vite and Go through the development launcher. `--loopback` is mandatory for validation:

```bash
node "$HOME/.agents/skills/kiwi-code-processes/scripts/start-process.mjs" \
  "browser-validation-<vite-port>" \
  "cd web && DIRE_MUX_DATA_DIR='<data-dir>' npm run dev:servers -- --loopback --vite-port <vite-port> --go-port <go-port> --tmux-socket '<tmux-socket>' --add-current-directory"
```

Both launch forms add the current worktree root to the isolated project store before the server listens. The operation is idempotent, so a Go backend restart keeps the same project and initial thread instead of creating duplicates.

Record the returned process ID and read bounded logs:

```bash
node "$HOME/.agents/skills/kiwi-code-processes/scripts/read-logs.mjs" <process-id> 200
```

For the embedded app, require the expected Go URL and the `Added current directory project` startup message. For the split stack, require both expected URLs, the generated tmux socket, and the current worktree's `Project:` line in the launcher output. If logs show `tmux: kiwi-code` or `tmux: dire-mux`, stop immediately and fix the launch command before opening the browser. If startup reports an address collision, stop this validation process, allocate a new port, and retry. Do not continue with another server that happens to answer the URL.

## 5. Validate in Chrome

Load the `chrome-devtools-browser` skill and dynamically enable the browser tools needed for navigation, interaction, inspection, screenshots, console messages, and network requests.

Open the correct unique URL:

- embedded app: `http://127.0.0.1:<go-port>`
- split stack: `http://127.0.0.1:<vite-port>`

Then:

1. Confirm the page is served by the validation URL and reaches a stable rendered state.
2. Create only the isolated fixture data needed for the planned journey.
3. Perform the actual user interactions for the changed behavior.
4. Verify visible output and any state that should survive navigation or reload.
5. For split-stack changes, verify API, event-stream, and WebSocket requests target the unique Go port directly rather than the Vite port.
6. Check relevant responsive states when layout behavior changed.
7. Inspect browser console errors and failed relevant requests.
8. Capture a screenshot when it helps demonstrate visual correctness or diagnose a failure.

When a browser check exposes a bug, fix it, rerun the relevant automated checks, rebuild if necessary, restart the isolated validation process from the updated worktree, and repeat the browser journey. Reuse neither the old ports nor the old tmux socket for a new run.

## 6. Clean up

Close validation browser tabs, then stop the exact process ID returned at startup:

```bash
node "$HOME/.agents/skills/kiwi-code-processes/scripts/stop-process.mjs" <process-id>
```

After the application has stopped, kill only the literal isolated tmux server generated for this run:

```bash
tmux -L '<tmux-socket>' kill-server 2>/dev/null || true
```

The socket argument must be the recorded `dmv-...` value and must never be `kiwi-code` or `dire-mux`. Finally, remove the exact temporary data and fixture directories created for this run. Never use a broad wildcard or remove a path that was not printed by this workflow.

## 7. Report evidence

In the final response, state:

- the unique URL or URLs used;
- the isolated tmux socket name used;
- the browser journeys exercised and their outcomes;
- whether console and relevant network checks were clean;
- the automated checks run; and
- any behavior that could not be validated.
