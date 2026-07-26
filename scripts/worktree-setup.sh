#!/bin/sh
# Prepare a fresh Kiwi Code worktree for isolated development.
#
# Seeds an isolated application data directory whose settings.json registers
# the CC Personal Claude Code coding-agent profile, and generates an isolated
# tmux socket name, so a development stack launched from this worktree never
# touches the production `kiwi-code` tmux server, port 4000, or the
# production data directory.
#
# Usage: scripts/worktree-setup.sh [data-dir]
#   data-dir defaults to <worktree-root>/.kiwi-code-dev/data
#
# Environment:
#   CLAUDE_PERSONAL_CONFIG_DIR  Claude Code config directory to register
#                               (default: $HOME/.claude-personal)
#
# The script is idempotent: an existing settings.json and tmux socket name
# are kept, so it is safe to run again or to wire up as the project's
# worktree environment setup script.
#
# After stopping the development stack, clean up only the generated server:
#   tmux -L "$KIWI_CODE_TMUX_SOCKET" kill-server
# Never run cleanup against `tmux -L kiwi-code`.

set -eu

root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
state_dir="$root/.kiwi-code-dev"
data_dir="${1:-$state_dir/data}"
claude_config_dir="${CLAUDE_PERSONAL_CONFIG_DIR:-$HOME/.claude-personal}"

production_data_dir="$HOME/Library/Application Support/kiwi-code"
case "$data_dir" in
"$production_data_dir" | "$production_data_dir"/*)
	echo "refusing to seed the production data directory: $data_dir" >&2
	exit 1
	;;
esac

if [ ! -d "$claude_config_dir" ]; then
	echo "warning: Claude Code config directory does not exist: $claude_config_dir" >&2
fi

mkdir -p "$data_dir" "$state_dir"

settings_file="$data_dir/settings.json"
if [ -f "$settings_file" ]; then
	echo "settings.json already present, leaving it unchanged: $settings_file"
else
	cat >"$settings_file" <<EOF
{
  "codingAgents": [
    {
      "id": "cc-personal",
      "name": "CC Personal",
      "kind": "claude",
      "configDirectory": "$claude_config_dir",
      "isDefault": true
    }
  ]
}
EOF
	echo "Seeded CC Personal coding agent (default) into $settings_file"
fi

env_file="$state_dir/env"
socket=""
if [ -f "$env_file" ]; then
	socket="$(. "$env_file" && printf '%s' "${KIWI_CODE_TMUX_SOCKET:-}")"
fi
if [ -z "$socket" ] || [ "$socket" = "kiwi-code" ]; then
	socket="kcv-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
fi
if [ -z "$socket" ] || [ "$socket" = "kiwi-code" ]; then
	echo "failed to generate an isolated tmux socket name" >&2
	exit 1
fi

cat >"$env_file" <<EOF
export KIWI_CODE_MODE=development
export KIWI_CODE_DATA_DIR="$data_dir"
export KIWI_CODE_TMUX_SOCKET="$socket"
EOF

echo "Wrote $env_file (tmux socket: $socket)"
echo
echo "Launch an isolated development stack from this worktree with:"
echo "  . $env_file"
echo "  make dev DEV_ARGS=\"--add-current-directory\""
