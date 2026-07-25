import { spawn } from "node:child_process";
import { constants } from "node:fs";
import { access, mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { loadConfig, type SandboxConfig } from "./config.ts";
import { assertPathAllowed, assertWorkingDirectory, canonicalPath, resolveDecision, type PolicyDecision } from "./policy.ts";
import { createSeatbeltProfile } from "./profile.ts";

export type SandboxOptions = {
  projectRoot: string;
  configPaths: string[];
  sandboxExecutable?: string;
  protectedWritePaths?: string[];
};

export type CommandResult = {
  exitCode: number;
  timedOut: boolean;
  cancelled: boolean;
  policy: PolicyDecision;
};

export type CommandOptions = {
  cwd?: string;
  env?: Record<string, string>;
  timeoutMs?: number;
  signal?: AbortSignal;
  onOutput?: (stream: "stdout" | "stderr", data: Buffer) => void;
};

export type EditBlock = { oldText: string; newText: string };

type FileWorkerRequest =
  | { operation: "read"; path: string }
  | { operation: "access"; path: string; mode: "read" | "write" }
  | { operation: "mkdir"; path: string }
  | { operation: "write"; path: string; dataBase64: string };

const FILE_WORKER_SOURCE = String.raw`
const fs = require("node:fs");
const fsp = require("node:fs/promises");
(async () => {
  const chunks = [];
  for await (const chunk of process.stdin) chunks.push(Buffer.from(chunk));
  const request = JSON.parse(Buffer.concat(chunks).toString("utf8"));
  if (request.operation === "read") {
    const data = await fsp.readFile(request.path);
    process.stdout.write(JSON.stringify({ dataBase64: data.toString("base64") }));
  } else if (request.operation === "access") {
    await fsp.access(request.path, request.mode === "read" ? fs.constants.R_OK : fs.constants.W_OK);
    process.stdout.write('{"ok":true}');
  } else if (request.operation === "mkdir") {
    await fsp.mkdir(request.path, { recursive: true });
    process.stdout.write('{"ok":true}');
  } else if (request.operation === "write") {
    await fsp.writeFile(request.path, Buffer.from(request.dataBase64, "base64"));
    process.stdout.write('{"ok":true}');
  }
})().catch((error) => { process.stderr.write(error?.message ?? String(error)); process.exit(1); });
`;

export class KiwiSandbox {
  readonly projectRoot: string;
  readonly paths: string[];
  readonly sandboxExecutable: string;
  private readonly protectedWritePaths: string[];
  private readonly mutationQueues = new Map<string, Promise<void>>();
  private enabled = true;

  constructor(options: SandboxOptions) {
    this.projectRoot = resolve(options.projectRoot);
    this.paths = [...options.configPaths];
    this.sandboxExecutable = options.sandboxExecutable ?? process.env.KIWI_SANDBOX_EXECUTABLE ?? "/usr/bin/sandbox-exec";
    this.protectedWritePaths = [...(options.protectedWritePaths ?? [])];
  }

  setEnabled(enabled: boolean): void {
    this.enabled = enabled;
  }

  isEnabled(): boolean {
    return this.enabled;
  }

  async validateConfig(): Promise<SandboxConfig> {
    if (process.platform !== "darwin" && this.sandboxExecutable === "/usr/bin/sandbox-exec") {
      throw new Error(`kiwi-sandbox requires macOS; refusing an unsandboxed fallback on ${process.platform}`);
    }
    return loadConfig(this.paths);
  }

  async runCommand(command: string, options: CommandOptions = {}): Promise<CommandResult> {
    const config = await this.validateConfig();
    const cwd = await assertWorkingDirectory(
      this.projectRoot,
      options.cwd ?? this.projectRoot,
      config.relatedProjects,
    );
    const policy = this.enabled ? await this.decision(config, command, cwd) : disabledPolicy();
    const profile = this.enabled ? createSeatbeltProfile(policy) : undefined;
    const executable = this.enabled ? this.sandboxExecutable : config.shell;
    const args = this.enabled ? ["-p", profile!, config.shell, "-lc", command] : ["-lc", command];
    const child = spawn(executable, args, {
      cwd,
      detached: true,
      env: { ...process.env, ...options.env },
      stdio: ["ignore", "pipe", "pipe"],
    });
    child.stdout.on("data", (data: Buffer) => options.onOutput?.("stdout", data));
    child.stderr.on("data", (data: Buffer) => options.onOutput?.("stderr", data));

    let timedOut = false;
    let cancelled = false;
    const kill = () => {
      if (!child.pid) return;
      try { process.kill(-child.pid, "SIGKILL"); } catch { child.kill("SIGKILL"); }
    };
    const abort = () => { cancelled = true; kill(); };
    options.signal?.addEventListener("abort", abort, { once: true });
    if (options.signal?.aborted) abort();
    const timer = options.timeoutMs && options.timeoutMs > 0
      ? setTimeout(() => { timedOut = true; kill(); }, options.timeoutMs)
      : undefined;

    try {
      const exitCode = await new Promise<number>((resolveExit, reject) => {
        child.once("error", reject);
        child.once("close", (code) => resolveExit(code ?? -1));
      });
      return { exitCode, timedOut, cancelled, policy };
    } finally {
      if (timer) clearTimeout(timer);
      options.signal?.removeEventListener("abort", abort);
    }
  }

  async readFile(path: string, tool: "read" | "edit" = "read"): Promise<Buffer> {
    const { policy, canonical } = await this.filePolicy(tool, path, "read");
    const result = await this.runFileWorker(policy, { operation: "read", path: canonical }) as { dataBase64: string };
    return Buffer.from(result.dataBase64, "base64");
  }

  async access(
    path: string,
    mode: "read" | "write" = "read",
    tool: "read" | "write" | "edit" = mode === "read" ? "read" : "write",
  ): Promise<void> {
    const { policy, canonical } = await this.filePolicy(tool, path, mode);
    await this.runFileWorker(policy, { operation: "access", path: canonical, mode });
  }

  async mkdir(path: string, tool: "write" | "edit" = "write"): Promise<void> {
    const { policy, canonical } = await this.filePolicy(tool, path, "write");
    await this.runFileWorker(policy, { operation: "mkdir", path: canonical });
  }

  async writeFile(path: string, content: string | Buffer, tool: "write" | "edit" = "write"): Promise<void> {
    const { policy, canonical } = await this.filePolicy(tool, path, "write");
    await this.runFileWorker(policy, {
      operation: "write",
      path: canonical,
      dataBase64: Buffer.from(content).toString("base64"),
    });
  }

  async writeFileWithParents(path: string, content: string | Buffer): Promise<void> {
    await this.mkdir(dirname(resolve(path)));
    await this.writeFile(path, content);
  }

  async editFile(path: string, edits: EditBlock[]): Promise<{ replacements: number }> {
    const { policy, canonical } = await this.filePolicy("edit", path, "read");
    if (this.enabled) await assertPathAllowed(canonical, policy, "write");
    return this.withMutationQueue(canonical, async () => {
      const readResult = await this.runFileWorker(policy, { operation: "read", path: canonical }) as { dataBase64: string };
      let content = Buffer.from(readResult.dataBase64, "base64").toString("utf8");
      let replacements = 0;
      for (const edit of edits) {
        const first = content.indexOf(edit.oldText);
        if (first < 0) throw new Error(`oldText not found in ${path}`);
        if (content.indexOf(edit.oldText, first + edit.oldText.length) >= 0) {
          throw new Error(`oldText is not unique in ${path}`);
        }
        content = `${content.slice(0, first)}${edit.newText}${content.slice(first + edit.oldText.length)}`;
        replacements++;
      }
      await this.runFileWorker(policy, {
        operation: "write",
        path: canonical,
        dataBase64: Buffer.from(content).toString("base64"),
      });
      return { replacements };
    });
  }

  private async filePolicy(tool: "read" | "write" | "edit", path: string, mode: "read" | "write") {
    if (!this.enabled) return { policy: disabledPolicy(), canonical: await canonicalPath(path) };
    const config = await this.validateConfig();
    const policy = await this.decision(config, `${tool} ${path}`, this.projectRoot);
    const canonical = await assertPathAllowed(path, policy, mode);
    return { policy, canonical };
  }

  private async decision(config: SandboxConfig, command: string, cwd: string): Promise<PolicyDecision> {
    const decision = await resolveDecision(
      config,
      command,
      cwd,
      [dirname(dirname(process.execPath))],
      this.projectRoot,
    );
    const protectedPaths = [...this.paths, ...this.protectedWritePaths];
    const canonicalProtected = await Promise.all(protectedPaths.map((path) => canonicalPath(path)));
    decision.deniedWrite = [...new Set([...protectedPaths.map((path) => resolve(path)), ...canonicalProtected])];
    return decision;
  }

  private async runFileWorker(policy: PolicyDecision, request: FileWorkerRequest): Promise<unknown> {
    if (!this.enabled) return executeFileRequest(request);
    const profile = createSeatbeltProfile(policy);
    const child = spawn(this.sandboxExecutable, ["-p", profile, process.execPath, "-e", FILE_WORKER_SOURCE], {
      cwd: this.projectRoot,
      stdio: ["pipe", "pipe", "pipe"],
      env: process.env,
    });
    const stdout: Buffer[] = [];
    const stderr: Buffer[] = [];
    child.stdout.on("data", (data: Buffer) => stdout.push(data));
    child.stderr.on("data", (data: Buffer) => stderr.push(data));
    child.stdin.end(JSON.stringify(request));
    const exitCode = await new Promise<number>((resolveExit, reject) => {
      child.once("error", reject);
      child.once("close", (code) => resolveExit(code ?? -1));
    });
    if (exitCode !== 0) {
      throw new Error(Buffer.concat(stderr).toString("utf8") || `sandboxed file worker exited with status ${exitCode}`);
    }
    const text = Buffer.concat(stdout).toString("utf8");
    return text ? JSON.parse(text) : {};
  }

  private async withMutationQueue<T>(path: string, operation: () => Promise<T>): Promise<T> {
    const previous = this.mutationQueues.get(path) ?? Promise.resolve();
    let release!: () => void;
    const current = new Promise<void>((resolveRelease) => { release = resolveRelease; });
    const queued = previous.then(() => current);
    this.mutationQueues.set(path, queued);
    await previous;
    try {
      return await operation();
    } finally {
      release();
      if (this.mutationQueues.get(path) === queued) this.mutationQueues.delete(path);
    }
  }
}

function disabledPolicy(): PolicyDecision {
  return { rule: "disabled", unrestricted: true, read: [], write: [], deniedWrite: [], network: true };
}

async function executeFileRequest(request: FileWorkerRequest): Promise<unknown> {
  switch (request.operation) {
    case "read":
      return { dataBase64: (await readFile(request.path)).toString("base64") };
    case "access":
      await access(request.path, request.mode === "read" ? constants.R_OK : constants.W_OK);
      return { ok: true };
    case "mkdir":
      await mkdir(request.path, { recursive: true });
      return { ok: true };
    case "write":
      await writeFile(request.path, Buffer.from(request.dataBase64, "base64"));
      return { ok: true };
  }
}
