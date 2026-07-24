# Kiwi Sandbox

A pure-TypeScript npm workspace that shares one macOS Seatbelt sandbox library between Pi and Claude Code.

```text
sandbox-extension/
├── packages/
│   ├── core/       # configuration, policy, Seatbelt profiles, command/file workers
│   ├── pi/         # Pi bash/read/write/edit overrides
│   └── claude/     # Claude Code plugin and MCP server
├── config.example.json
└── package.json
```

There is no Go daemon or private RPC protocol. Bash calls start the configured shell directly under `/usr/bin/sandbox-exec`. Read, write, and edit operations start short-lived Node workers under Seatbelt so the filesystem operation itself—not only a TypeScript path check—is sandboxed.

## Configuration

Kiwi Sandbox loads configuration in this order:

1. `~/.config/kiwi-sandbox/sandbox.json`
2. `<project>/.config/kiwi-sandbox.json`

The project file overrides top-level fields from the global file. Pi only reads the project file when the project is trusted; enabling the Claude plugin trusts its project configuration.

```json
{
  "defaults": {
    "read": ["$CWD"],
    "write": ["$CWD", "$TMPDIR"]
  },
  "network": true,
  "shell": "/bin/zsh",
  "relatedProjects": ["../shared-library", "~/projects/related-service"],
  "commands": [
    {
      "pattern": ["gh", "gh *"]
    },
    {
      "pattern": ["git", "git *"],
      "network": false,
      "files": {
        "read": ["$CWD", "$HOME/.gitconfig"],
        "write": ["$CWD"]
      }
    },
    {
      "pattern": "edit *",
      "files": {
        "read": ["$CWD"],
        "write": ["$CWD"]
      }
    }
  ]
}
```

See [`config.example.json`](config.example.json) for all tool patterns.

### Policy behavior

- The first matching command rule replaces `defaults`.
- A command string is shorthand for an unrestricted filesystem rule. For example, `"gh *"` is equivalent to `{ "pattern": "gh *" }`.
- An object rule's `pattern` may be one string or a list such as `["gh", "gh *"]`; every listed pattern shares that rule's `files` and `network` policy.
- A matching object rule with no `files` field also grants unrestricted filesystem reads and writes.
- An optional command-level `network` boolean overrides the top-level network policy for that matching simple command.
- Once `files` is present, its `read` and `write` lists constrain the operation. Write paths are also readable.
- `$CWD`, `$HOME`, `$TMPDIR`, `~`, absolute paths, and project-relative paths are supported.
- Runtime paths needed for macOS, Node, and command execution are always readable. `/dev/null` and `/dev/tty` are always writable.
- Globs support `*`, `?`, and character classes.
- Unrestricted command rules are selected only for conservative single shell commands. Composition, redirection, command substitution, and unbalanced quotes fall back to `defaults`, preventing `gh status; cat ~/.ssh/id_ed25519` from inheriting `gh *` access.
- A requested command working directory must remain inside the project root after resolving symlinks.
- There is no unsandboxed fallback if `sandbox-exec` fails, and unknown configuration fields are rejected rather than ignored.
- While enforcement is enabled, both active config files, the loaded Kiwi Sandbox runtime, and Claude's enable/disable state directory are denied write access even when a command otherwise has unrestricted filesystem access. Disable explicitly before a user-requested policy edit, then re-enable immediately.
- Command-level network overrides apply only when that simple command rule matches. For a deny-by-default network policy, set top-level `network` to `false` and opt specific commands in with `"network": true`.
- Only project config may set `relatedProjects` to relative, absolute, or home-relative project paths. Only these paths—not ordinary `defaults` or command read/write roots—are added to Pi and Claude's system context. This is informational and does not grant filesystem access.

The filesystem tools are evaluated as command-like patterns:

- `read <path>`
- `write <path>`
- `edit <path>`

This allows the same `commands` rules to govern Bash and native filesystem tools.

## Installation

Requirements:

- macOS with `/usr/bin/sandbox-exec`
- Node.js 22.6 or newer

Install workspace dependencies:

```bash
cd internal/server/kiwi-sandbox
npm install
```

### Pi

Run directly:

```bash
pi --no-extensions -e /absolute/path/to/kiwi-code/internal/server/kiwi-sandbox
```

Or install the monorepo as a local Pi package:

```bash
pi install /absolute/path/to/kiwi-code/internal/server/kiwi-sandbox
```

The Pi package overrides `bash`, `read`, `write`, and `edit`, preserving Pi's built-in schemas, truncation, mutation queue, details, and renderers. Interactive `!` commands also use Kiwi Sandbox. `/sandbox` displays current status and config paths.

Pi provides two session-scoped slash commands:

- `/kiwi-sandbox-enable`
- `/kiwi-sandbox-disable`

Pi also loads the `kiwi-sandbox-config` Agent Skill. Invoke `/skill:kiwi-sandbox-config` to inspect, create, or edit the global or project policy with configuration guidance in context.

Disabling restores unsandboxed command and filesystem execution for the current Pi session; the status bar displays a warning until enforcement is enabled again.

### Claude Code

Load the plugin from its workspace:

```bash
claude --plugin-dir /absolute/path/to/kiwi-code/internal/server/kiwi-sandbox/packages/claude
```

The plugin starts a TypeScript stdio MCP server and exposes:

- `sandbox_exec`
- `sandbox_read`
- `sandbox_write`
- `sandbox_edit`

A fail-closed `PreToolUse` hook denies Claude Code's built-in command, filesystem, and network tools—including `Bash`, `Read`, `Write`, `Edit`, `Glob`, `Grep`, notebook editing, `WebFetch`, and `WebSearch`—and directs the model to the Seatbelt-backed MCP tools.

Claude Code exposes the plugin skills as namespaced slash commands:

- `/kiwi-sandbox:enable`
- `/kiwi-sandbox:disable`
- `/kiwi-sandbox:config` — explains both config paths, precedence, JSON fields, path substitutions, and tool patterns

Claude's enabled state persists per project under `~/.config/kiwi-sandbox/state/` so the hook and MCP subprocess share the same setting. While disabled, the hook permits the built-in tools and the MCP tools run without Seatbelt. Enabling removes the disabled marker and restores fail-closed enforcement.

## Development and testing

```bash
npm test
```

This runs:

- strict TypeScript checking across all workspaces;
- policy, config-overlay, compound-command, and Seatbelt-profile tests;
- command and sandboxed read/write/edit worker tests;
- a real Pi RPC process loading the four tool overrides and invoking both toggle commands; and
- a Claude-compatible MCP process exercising exec, read, write, edit, persistent toggling, and hook behavior end to end.

Integration tests replace `sandbox-exec` with a transparent test launcher because application sandboxes generally reject nested Seatbelt activation. In production, the default always remains `/usr/bin/sandbox-exec`.

## Security notes

- `sandbox-exec` and Seatbelt profiles are deprecated private macOS facilities.
- An unrestricted command rule is intentionally powerful. Keep its pattern narrow.
- The extension passes the caller's environment to shell commands; filesystem sandboxing does not hide existing environment variables.
- The TypeScript parent performs canonical path checks, and the actual file operation runs in a separately sandboxed Node process.
- Seatbelt is defense in depth and does not replace Pi or Claude Code permission review.
