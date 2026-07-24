---
name: disable
description: Disable Kiwi Sandbox enforcement for the current project
disable-model-invocation: true
---

!`node --experimental-strip-types "${CLAUDE_PLUGIN_ROOT}/src/control.ts" disable "${CLAUDE_PROJECT_DIR}"`

Warn that built-in command, filesystem, and network tools may now run without Seatbelt. Take no other action.
