# Native chat

Choose **Codex Native** in the new-thread composer or the workspace agent menu.
It uses the installed `codex app-server` and its existing login/configuration.
Codex CLI remains a separate terminal choice. Sign in with `codex login` if the
CLI has not been configured on the server machine.

The conversation layout follows T3 Code: a centered transcript, user bubbles,
inline assistant Markdown, expandable tool activity and a rounded composer.
Colors come from Kiwi's theme tokens. The composer supports images, installed
models, reasoning effort, stopping a turn, command/file approvals, and questions.
The sidebar receives activity heartbeats and native token usage.

Interactive questions require Codex to allow its `request_user_input` tool.
With Codex CLI 0.154.0, Default mode rejects that tool unless
`features.default_mode_request_user_input = true` is enabled in Codex's
configuration. Real CLI and Kiwi tests both reproduced the rejection with the
flag off and completed a selectable question with it on. The flag is marked
under development; Kiwi does not enable it automatically or expose Plan mode.
Kiwi also does not yet expose an approval-mode selector or explicitly set
`approvalsReviewer`; the installed Codex configuration supplies that setting.

## Provider boundary

- `internal/server/native_chat.go` defines provider-neutral messages, items,
  pending requests, usage and snapshots.
- `internal/server/codex_native.go` owns the Codex stdio JSON-RPC adapter. It
  initializes the connection, translates items/deltas, correlates replies,
  and forwards only supported browser actions. Unknown provider requests receive
  an explicit unsupported-request error; arbitrary RPC forwarding is not exposed.
- `web/src/features/workspace/panes/agent/nativeChat.ts` reduces sequenced updates;
  `NativeChatPane.tsx` renders the shared contract. Add a provider descriptor and
  a server adapter emitting that contract to support native Claude here. The
  existing Claude Native implementation is independent and remains available.

A WebSocket reconnect receives an authoritative snapshot before later updates.
Sequence numbers discard events already represented in that snapshot. Drafts
remain local until the server acknowledges a prompt; reconnect never blindly
resubmits a possibly delivered prompt.

## Persistence and lifecycle

Each Kiwi thread stores its exact Codex ID under
`codex-native-sessions/<project>/<thread>/thread-id`. Codex owns the transcript.
A durable `prompt-attempted` marker distinguishes a conversation that needs
`thread/resume` from an unused thread: Codex does not create a saved rollout until
its first prompt. An ambiguous transport failure retains the marker and ID;
an explicit first-prompt rejection removes the marker. Failure to resume an
existing conversation never silently creates a replacement.

Settlement uses the existing launch/mutation fence, refuses working threads,
stops the app-server, and preserves the ID. Unsettling resumes on next opening.
Deleting a Kiwi thread removes its native mapping; it does not delete Codex's
own global conversation history. Shutdown stops all native processes.

The adapter currently exposes one active turn per thread. Stop it before sending
a different prompt. It uses workspace-write sandboxing and on-request approvals.
Cost estimates are not inferred from token usage; app-server supplies tokens,
not billed dollar amounts. Remote provider login management and a Claude adapter
for this shared surface are future extensions.

## Validation

`codex_native_test.go` drives a deterministic Python app-server fixture, covering
streaming, approvals, interruption, WebSocket acknowledgements/heartbeats,
settlement and exact resume, plus empty/rejected first-turn recovery.
`NativeChatPane.test.tsx` covers draft acknowledgement, provider errors,
reconnect deduplication, approval/stop controls and sequenced updates.
Run Go integration tests with isolated tmux resources as required by AGENTS.md.
