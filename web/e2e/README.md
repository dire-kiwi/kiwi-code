# Browser end-to-end tests

The suite in `app-pages.test.mjs` builds the production web assets, embeds them
in a fresh Go binary, starts that binary in development mode, and drives the
result through headless Chrome with `puppeteer-core`.

Run it from `web`:

```sh
npm run test:e2e
```

Chrome is discovered with the same candidates as Kiwi Code's browser host.
Set `KIWI_CODE_CHROME_BIN` when Chrome or Chromium is installed elsewhere.
The test also needs `go`, `git`, `tmux`, and the real `pi` executable on
`PATH`.

## Isolation and coverage

Every run allocates all of the following from scratch:

- a non-4000 loopback HTTP port;
- an application data directory and home directory;
- an isolated Pi agent directory;
- a Git fixture repository;
- a Chrome profile;
- a Pi request-report directory; and
- a short random tmux socket that is explicitly checked not to be
  `kiwi-code`.

The server is always launched with `-mode development`,
`-add-current-directory`, and the generated `-tmux-socket`. Cleanup stops the
exact child application, closes Chrome, kills only that generated tmux
server, and removes the run's temporary root. The production port, data, home,
and canonical `kiwi-code` tmux server are never inspected or changed.

The browser assertions cover the empty-profile landing state, all standalone
pages, all global and project settings sections, new-thread and thread-sandbox
pages, and all seven workspace tools. Canonical redirects for settings,
projects, threads, the root route, and unknown routes are exercised too.
Console errors, uncaught page errors, failed same-origin requests, and HTTP
error responses are collected and fail the suite.

## Real Pi with deterministic fixtures

`support/pi-fixture-provider.ts` is a project-local Pi extension, not a fake
`pi` executable or a network server. The harness copies it to
`.pi/extensions/kiwi-e2e-provider.ts` in the temporary repository and commits
it before Kiwi Code creates a worktree. The Go child receives absolute
`KIWI_CODE_E2E_PI_FIXTURE` and `KIWI_CODE_E2E_PI_REPORT_DIR` paths, while
`PI_OFFLINE=1` keeps the run off external model providers.

The isolated Pi directory also contains a metadata-only `models.json` entry
for `kiwi-e2e/chat`. This lets Kiwi Code's safety-preserving New Thread model
discovery show the model before it approves any project extension. When the
thread starts, the committed extension registers the same provider name and
replaces that metadata entry with its in-memory `fauxProvider`; no HTTP model
endpoint is started or contacted.

The New Thread screen explicitly selects:

- agent: Pi Native;
- model: `kiwi-e2e/chat`; and
- thinking level: Low.

Kiwi Code then launches the installed real Pi agent. The version-1 fixture in
`fixtures/real-chat.json` makes Pi use its real `read` tool against the
temporary repository's `README.md`, answers a second user turn, and replaces
the automatic-title model in memory. The test verifies the rendered
conversation, generated title, session usage, successful tool result, and all
matched JSONL request-report steps.

The fixture parser is intentionally strict: unknown fields, invalid types,
ambiguous matches, unmatched requests, duplicate deterministic tool-call IDs,
and requests beyond `maxRequests` fail loudly. Add a new fixture step only
when an additional model request is part of the behavior being tested.

### Writing chat fixtures

Copy `fixtures/real-chat.json` and point `KIWI_CODE_E2E_PI_FIXTURE` at the
absolute path when using the extension outside the bundled harness. A
version-1 fixture contains:

- `model`: the fake model ID, display name, reasoning support, and thinking
  levels exposed to Pi;
- `titleSteps`: expected automatic-title prompts and their title text;
- `steps`: context predicates paired with assistant replies;
- `tokensPerSecond`: optional real-time pacing (`0` streams immediately); and
- `maxRequests`: a hard guard against unexpected model calls.

Chat predicates can match `modelId`, exact or partial last-user text, a suffix
of conversation roles, and the latest tool result's ID, name, error state, or
text. Replies can contain streamed `text`, `thinking`, and `toolCall` blocks.
Tool calls require an explicit stable ID so the following tool-result step can
match it.

Matching is context-based and idempotent, not cursor-based: exactly one step
must match each request, and retrying the same context intentionally replays
the same reply. Use additional role/tool predicates when repeated user text
needs a different response later in the conversation.

The extension also replaces the `openai-codex/gpt-5.6-luna` title model inside
that isolated Pi process. This is what prevents automatic naming from reaching
a real provider; do not load this test extension in a normal Pi session.
