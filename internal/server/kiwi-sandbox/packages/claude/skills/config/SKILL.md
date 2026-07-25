---
name: config
description: Explain Kiwi Sandbox configuration paths, JSON format, precedence, and command rules
---

# Kiwi Sandbox configuration

Use this reference when the user asks where Kiwi Sandbox is configured, how its JSON file is structured, or how to grant filesystem access to a command or file tool.

## Configuration paths

Kiwi Sandbox reads these files in order:

1. Global: `~/.config/kiwi-sandbox/sandbox.json`
2. Project: `<project-root>/.config/kiwi-sandbox.json`

The project file overrides top-level fields from the global file. A field omitted from the project file keeps its global value. Pi honors the project file only when the project is trusted. Claude Code honors it when the Kiwi Sandbox plugin is enabled for that project.

The per-project enable/disable markers under `~/.config/kiwi-sandbox/state/` are internal state, not configuration files, and should not be edited as sandbox policy.

## JSON format

Configuration is JSON without comments:

```json
{
  "defaults": {
    "read": ["$CWD"],
    "write": ["$CWD", "$TMPDIR"]
  },
  "network": false,
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
        "read": ["$CWD", "$HOME/.gitconfig", "$HOME/.ssh/known_hosts"],
        "write": ["$CWD"]
      }
    },
    {
      "pattern": "read *",
      "files": {
        "read": ["$CWD"],
        "write": []
      }
    },
    {
      "pattern": "write *",
      "files": {
        "read": ["$CWD"],
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

### Fields

- `defaults.read`: directories or files readable when no command rule matches.
- `defaults.write`: directories or files writable when no command rule matches. Write paths are automatically readable.
- `network`: whether sandboxed processes may use the network.
- `shell`: absolute shell path used by command execution.
- `relatedProjects`: project-config-only paths injected into Pi and Claude's system context and added to the default read/write policy. Relative paths resolve from the project root. Ordinary read/write roots are not injected into context.
- `commands`: ordered command rules. The first matching rule replaces `defaults`, including automatic related-project access, for that operation.
- A string command entry is an unrestricted-filesystem shorthand; `"gh *"` equals `{ "pattern": "gh *" }`.
- `commands[].pattern`: one glob or a non-empty list of globs supporting `*`, `?`, and character classes. Every pattern in a list shares the object's policy.
- `commands[].network`: optional network override for the matching command; omission inherits top-level `network`.
- `commands[].files.read`: read allowlist for the matching rule.
- `commands[].files.write`: write allowlist for the matching rule.

A matching command object with no `files` field grants unrestricted filesystem reads and writes. An explicit empty policy uses `"files": { "read": [], "write": [] }`.

## Paths and substitutions

Allowed path values include:

- `$CWD`: command working directory; for file tools this is the project root.
- `$HOME`: current user's home directory.
- `$TMPDIR`: operating-system temporary directory.
- `~` and `~/...`: current user's home directory.
- Absolute paths.
- Relative paths, resolved against `$CWD`.

Paths are canonicalized through existing ancestors before policy checks to prevent symlink aliases from bypassing an allowlist.

Fixed macOS/toolchain runtime paths are always readable so shells, Node, and command-line executables can start. `/dev/null` and `/dev/tty` are always writable.

## File tool patterns

Native filesystem tools are evaluated as command-like patterns:

- `read <absolute-path>`
- `write <absolute-path>`
- `edit <absolute-path>`

Rules such as `read *`, `write *`, and `edit *` therefore govern Pi's overridden tools and Claude's `sandbox_read`, `sandbox_write`, and `sandbox_edit` MCP tools.

## Safety details

For shell execution, command-specific rules apply only to a conservative single command. Shell composition, redirection, command substitution, and malformed quoting fall back to `defaults`. This prevents an unrestricted `gh *` rule from authorizing a composed command such as `gh status; cat ~/.ssh/id_ed25519`.

Command-level network overrides follow the same rule: compound commands use the top-level network policy. For deny-by-default networking, set top-level `network` to `false` and opt specific simple commands in with `"network": true`.

When helping modify a configuration, preserve unrelated fields, keep unrestricted patterns narrow, and clearly warn before adding a command rule that omits `files`. Kiwi Sandbox protects both active config files from writes while enabled; use `/kiwi-sandbox:disable` only for the confirmed edit and immediately restore enforcement with `/kiwi-sandbox:enable`.
