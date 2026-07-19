# Agent instructions

## Stable tmux identities

Dire Mux persistence depends on finding the same tmux server, sessions, and windows after browser and backend restarts. Treat these names as a persistent-data compatibility contract, not as internal implementation details.

Do not change any of the following:

- tmux socket: `kiwi-code` (used as `tmux -L kiwi-code`)
- per-thread Shell session: `kiwi-code-<projectID>-<threadID>-terminal`
- per-thread shared tools session: `kiwi-code-<projectID>-<threadID>-tools`
- fixed windows in the shared tools session: `nvim`, `lazygit`, and `pi`

The terminal tool mapping must remain:

- `terminal` -> the `-terminal` session
- `nvim`, `lazygit`, `pi`, and `process` -> the `-tools` session

The canonical implementation is in `internal/server/terminal.go`, especially `tmuxSocketName`, `tmuxSessionName`, and the shared-window setup. Tests asserting the exact names belong in `internal/server/terminal_test.go`.

## Runtime environment safety

The canonical `kiwi-code` tmux server, legacy migration server `dire-mux`, and port `4000` are reserved exclusively for production. Development mode must never use any of them, and production started from a Git checkout must only run from `main`. Keep these startup checks fail-closed when changing launchers or runtime configuration.

## Isolate tests and validation

The canonical `kiwi-code` tmux server and legacy migration server `dire-mux` are reserved exclusively for the user's production environment. Tests, browser validation, development, and agent-launched parallel stacks must never connect to either one, inspect it, create sessions in it, or kill it.

Every test or validation application process must use all of the following:

- a fresh loopback port for every HTTP listener (including separate Vite and Go ports);
- a fresh temporary data directory; and
- a unique, short tmux socket name, such as `dmv-<port>-<random>`.

Pass `-mode development` plus the isolated socket with `-tmux-socket <name>` (or `DIRE_MUX_TMUX_SOCKET`) to direct application launches. The development stack fixes the mode automatically and also accepts `--tmux-socket <name>`. Agent test servers must also pass `-add-current-directory` directly, or `--add-current-directory` through the development stack, so the isolated store starts with the current checkout as a project. Before starting the process, explicitly verify that the generated name is non-empty and is neither `kiwi-code` nor `dire-mux`. After stopping the exact application process, clean up only that generated server with `tmux -L <name> kill-server`; a missing isolated server is harmless. Never run a cleanup command against `tmux -L kiwi-code` or `tmux -L dire-mux`.

Go tests that execute real tmux commands must use `isolatedTmuxServer(t)` or an equivalent per-test socket allocated before the first tmux command. Do not construct a handler on the default socket and overwrite its socket afterward.

If a rename ever becomes unavoidable, do not simply change the lookup string. First implement and test a backward-compatible migration that adopts existing live windows without restarting their processes or losing terminal state. Cleanup must recognize both schemas during the migration period. A deployment must never silently create a fresh session while an old-name session still contains the user's process.
