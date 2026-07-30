# Redux Toolkit migration — decisions taken without asking

Written during the migration of client-persisted state to `src/store`. Each item
is a judgement call I made to keep going; none are hard to reverse. Flagged for
review rather than assumed settled.

## Behaviour changes I chose to make

**1. A thread that never changes its agent or presentation now stores nothing.**

The old hook wrote on mount, so merely opening a thread left all three of
`kiwi-code:coding-agent:…`, `kiwi-code:pi-presentation:…` and
`kiwi-code:claude-presentation:…` behind forever. With unbounded thread ids that
is the unbounded-growth problem in the plan. `mergeThreadWorkspace` now records
only values that came from storage or from routing, and `resolveThreadWorkspace`
applies the defaults at read time. An absent key reads back as the same default,
so this is invisible to users — but it *is* a deliberate deviation from
byte-for-byte parity with the old write behaviour.

Reversal: have `threadWorkspaceMounted` commit `resolveThreadWorkspace(...)`
instead of `mergeThreadWorkspace(...)`. One line in `slices/threadWorkspace.ts`.

**2. Values are no longer written back on mount at all.**

`createPersistence().seed()` primes the dedupe map from the hydrated state, so a
preference nobody touched is never rewritten. This was listed in the plan as a
wart to fix; calling it out because it changes what a storage-write log looks
like. Test: "never writes back a value nobody changed".

**3. Writes are debounced 150ms with a 1000ms maximum.**

The plan specified the debounce. I added the max-wait after noticing that
`threadRevealed` dispatches on every `threadIndex` change, and agent activity
arriving over the socket can restart the debounce indefinitely. Without the cap a
sidebar resize could sit unwritten for as long as an agent streams. There is a
`pagehide`/`visibilitychange` flush as a backstop either way. Test: "cannot be
starved by a steady stream of unrelated actions" (verified it fails without the
cap).

**4. `bookmarksOnly` and `expandedMoreProjectIds` moved into the sidebar slice.**

They were `useState` in `ProjectSidebar` and remain unpersisted. They are in the
slice because they are the same "which rows are open" concern as their persisted
neighbours and the reveal-on-select effect touches all three at once. Side
effect: they are now app-global rather than per-instance. `ProjectSidebar` is
rendered once, so this is currently unobservable.

## Things I deliberately did not do

- **No garbage collection of stale per-thread keys.** Deferred as planned; it
  needs the live thread set, which is still server state in `App`'s `useState`.
  Now tractable because `hydrateThreadWorkspace` enumerates the keys, which
  nothing did before.
- **No new keyboard shortcut, no shortcut registry.** Per your answer. The `ui`
  slice is what unblocks it; `sidebarToggled` is already defined and unused.
- **`ProjectSidebar` still takes all 34 props.** Prop reduction was explicitly
  scoped out to keep the diff about storage ownership.
- **`backend-config.mjs` untouched** — read at module-eval time before the store
  exists.

## Browser validation

Ran an isolated development stack (fresh ports 64096/64097, scratchpad data dir,
tmux socket `kcv-e32715c5`, killed afterwards) and drove it with Puppeteer, then
repeated the identical run against a stashed clean `HEAD` for comparison. Both
runs seeded the same four keys, reloaded, and dumped what survived.

| Key | Clean `HEAD` | This branch |
| --- | --- | --- |
| `kiwi-code.sidebar.width` = `340` | preserved | preserved |
| `kiwi-code.sidebar.view` = `tree` | preserved | preserved |
| `kiwi-code.sidebar.web-servers-collapsed` = `true` | preserved | preserved |
| `kiwi-code.sidebar.collapsed-projects` = `["…"]` | reset to `[]` | reset to `[]` |
| `kiwi-code:coding-agent:…` and both presentations | written unprompted | absent |
| `kiwi-code-active-profile` | written unprompted | absent |

Two things worth reading off that table.

`collapsed-projects` resetting is **pre-existing and intentional**, not a
regression: selecting a thread expands whatever hides it, and on reload the
remembered workspace selects a thread in that project. Clean `HEAD` does exactly
the same. It surprised me enough to be worth checking, so it is recorded here.

The bottom two rows are decisions 1 and 2 above, visible: `HEAD` leaves five keys
behind for a session in which the user changed nothing, and this branch leaves
none. No React, Redux, or immer errors appeared in the console on either run.

## Open questions for you

1. **Is `sidebarToggled` dead code you want kept?** It is exported from
   `slices/ui.ts` and unused, sitting there for whatever shortcut you were
   originally adding. Delete it if you would rather not carry an unused action.

2. **Debounce and max-wait values (150ms / 1000ms) are guesses.** They are the
   only two magic numbers in `persistence.ts`.

3. **`scripts/worktree-setup.sh` refuses to run with no argument in this
   worktree.** Worktrees under `~/Library/Application Support/kiwi-code/…` trip
   the production-data-directory guard, because the guard tests the worktree path
   rather than the real data directory. I worked around it by passing an explicit
   scratchpad data dir. The guard is correct to be conservative, but the default
   is unusable from any worktree the app itself created — worth a look
   independently of this change.

4. **Two e2e tests fail, and did so before this change.** `thread sandbox page`
   and `browser workspace page` both time out at 20s on clean `HEAD` as well
   (21 !== 23 rendered pages), so they are environment-related and not caused by
   the migration. `npm test` and `make test` do not run the e2e suite, so this
   was never green in CI terms. Worth confirming they pass on your machine.

5. **`storedState.test.tsx` was renamed to `.ts`** since it no longer renders
   anything. Trivial, but it moves in git history.
