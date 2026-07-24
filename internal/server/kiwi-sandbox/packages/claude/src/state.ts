import { createHash } from "node:crypto";
import { mkdir, rm, writeFile } from "node:fs/promises";
import { existsSync, realpathSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join, resolve } from "node:path";

export function sandboxStatePath(projectRoot: string): string {
  const canonical = canonicalProjectRoot(projectRoot);
  const projectID = createHash("sha256").update(canonical).digest("hex").slice(0, 24);
  return join(homedir(), ".config", "kiwi-sandbox", "state", `${projectID}.disabled`);
}

export function isSandboxEnabled(projectRoot: string): boolean {
  return !existsSync(sandboxStatePath(projectRoot));
}

export async function setSandboxEnabled(projectRoot: string, enabled: boolean): Promise<void> {
  const path = sandboxStatePath(projectRoot);
  if (enabled) {
    await rm(path, { force: true });
  } else {
    await mkdir(dirname(path), { recursive: true });
    await writeFile(path, `${canonicalProjectRoot(projectRoot)}\n`, { mode: 0o600 });
  }
}

export function canonicalProjectRoot(projectRoot: string): string {
  if (!projectRoot || projectRoot.includes("${CLAUDE_PROJECT_DIR}")) {
    throw new Error("Claude project directory is unavailable");
  }
  return realpathSync.native(resolve(projectRoot));
}
