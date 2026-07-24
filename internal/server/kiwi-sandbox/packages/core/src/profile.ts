import type { PolicyDecision } from "./policy.ts";

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
  ];
  if (decision.network) lines.push("(allow network*)");
  if (decision.unrestricted) {
    lines.push("(allow file-read*)", "(allow file-write*)");
  } else {
    appendPaths(lines, "file-read*", decision.read);
    appendPaths(lines, "file-write*", decision.write);
  }
  appendPaths(lines, "file-write*", decision.deniedWrite, "deny");
  return `${lines.join("\n")}\n`;
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
