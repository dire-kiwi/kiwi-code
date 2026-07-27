import type { PolicyDecision } from "./policy.ts";

// macOS exposes the active developer-tools selection through /var/select, while
// /var itself resolves to /private/var. Policy paths are intentionally
// canonicalized, but Seatbelt does not treat a grant for /private/var/select as
// a grant for the lexical /var/select alias used by xcode-select. Keep this
// narrow, read-only alias in the generated profile so Apple developer-tool
// shims such as /usr/bin/git can discover their installation.
const LEXICAL_RUNTIME_READ_PATHS = ["/var/select"];

export function createSeatbeltProfile(decision: PolicyDecision): string {
  const lines = [
    "(version 1)",
    "(deny default)",
    "(allow process*)",
    "(allow signal)",
    "(allow sysctl-read)",
    "(allow mach-lookup)",
    "(allow ipc-posix*)",
    "(allow system-socket)",
    // Runtime path discovery must be able to inspect lexical ancestors such as
    // /var, /etc, /opt, and /tmp before macOS resolves their symlink targets.
    // This exposes metadata only; file contents and writes remain path-scoped.
    "(allow file-read-metadata)",
    // macOS 26 processes read the root directory while starting. Without this
    // exact-path grant, even /bin/pwd aborts before it can inspect an allowed cwd.
    // Keep this separate from decision.read so it does not become (subpath "/").
    '(allow file-read-data (literal "/"))',
  ];
  if (decision.pty) appendPtyAccess(lines);
  if (decision.network) lines.push("(allow network*)");
  if (decision.unrestricted) {
    lines.push("(allow file-read*)", "(allow file-write*)");
  } else {
    appendPaths(lines, "file-read*", [...decision.read, ...LEXICAL_RUNTIME_READ_PATHS]);
    appendPaths(lines, "file-write*", decision.write);
  }
  appendPaths(lines, "file-write*", decision.deniedWrite, "deny");
  return `${lines.join("\n")}\n`;
}

function appendPtyAccess(lines: string[]): void {
  // openpty() requires the pseudo-tty operation plus read/write and ioctl access
  // to the multiplexor and its dynamically selected slave. Match only modern
  // macOS PTY slaves, not arbitrary /dev nodes.
  lines.push(
    "(allow pseudo-tty)",
    "(allow file-read* file-write* file-ioctl",
    '  (literal "/dev/ptmx")',
    '  (regex #"^/dev/ttys[0-9][0-9][0-9]$")',
    ")",
  );
}

function appendPaths(lines: string[], operation: string, paths: string[], action: "allow" | "deny" = "allow"): void {
  if (paths.length === 0) return;
  lines.push(`(${action} ${operation}`);
  for (const path of paths) {
    const quoted = JSON.stringify(path);
    lines.push(`  (literal ${quoted})`, `  (subpath ${quoted})`);
  }
  lines.push(")");
}
