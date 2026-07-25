import { readFile } from "node:fs/promises";
import { homedir, tmpdir } from "node:os";
import { dirname, isAbsolute, join, resolve } from "node:path";

export type FileAccess = {
  read: string[];
  write: string[];
};

export type CommandRule = {
  pattern: string | string[];
  /** Omitted files deliberately means unrestricted filesystem access. */
  files?: FileAccess;
  /** Omitted network inherits the top-level network policy. */
  network?: boolean;
};

export type SandboxConfig = {
  defaults: FileAccess;
  commands: CommandRule[];
  network: boolean;
  shell: string;
  relatedProjects: string[];
};

export const GLOBAL_CONFIG_PATH = join(homedir(), ".config", "kiwi-sandbox", "sandbox.json");
export const PROJECT_CONFIG_RELATIVE_PATH = join(".config", "kiwi-sandbox.json");

export const RUNTIME_READ_PATHS = [
  "/bin", "/sbin", "/usr", "/System", "/Library", "/opt/homebrew", "/private/etc", "/dev",
];
export const RUNTIME_WRITE_PATHS = ["/dev/null", "/dev/tty"];

export function defaultConfig(): SandboxConfig {
  return {
    defaults: {
      read: ["$CWD", ...RUNTIME_READ_PATHS],
      write: ["$CWD", "$TMPDIR"],
    },
    commands: [],
    network: false,
    shell: "/bin/zsh",
    relatedProjects: [],
  };
}

export function configPaths(projectRoot: string, includeProject = true): string[] {
  const paths = [GLOBAL_CONFIG_PATH];
  if (includeProject) paths.push(join(projectRoot, PROJECT_CONFIG_RELATIVE_PATH));
  return paths;
}

export async function loadConfig(paths: string[]): Promise<SandboxConfig> {
  const config = defaultConfig();
  for (const [index, path] of paths.entries()) {
    let parsed: Partial<SandboxConfig>;
    try {
      const value = JSON.parse(await readFile(expandHome(path), "utf8")) as unknown;
      if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("configuration must be an object");
      assertKnownKeys(value, ["defaults", "commands", "network", "shell", "relatedProjects"], path);
      parsed = value as Partial<SandboxConfig>;
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code === "ENOENT") continue;
      throw new Error(`Cannot load sandbox config ${path}: ${error instanceof Error ? error.message : String(error)}`);
    }
    if (Object.hasOwn(parsed, "defaults")) config.defaults = validateFiles(parsed.defaults, `${path}: defaults`);
    if (Object.hasOwn(parsed, "commands")) config.commands = validateCommands(parsed.commands, path);
    if (Object.hasOwn(parsed, "network")) {
      if (typeof parsed.network !== "boolean") throw new Error(`${path}: network must be boolean`);
      config.network = parsed.network;
    }
    if (Object.hasOwn(parsed, "shell")) {
      if (typeof parsed.shell !== "string" || !isAbsolute(parsed.shell)) {
        throw new Error(`${path}: shell must be an absolute path`);
      }
      config.shell = parsed.shell;
    }
    if (Object.hasOwn(parsed, "relatedProjects")) {
      if (paths.length < 2 || index !== paths.length - 1) {
        throw new Error(`${path}: relatedProjects is only allowed in the project config`);
      }
      if (!Array.isArray(parsed.relatedProjects) || !parsed.relatedProjects.every((entry) => typeof entry === "string" && entry.trim() !== "")) {
        throw new Error(`${path}: relatedProjects must be a string array containing no empty paths`);
      }
      config.relatedProjects = [...parsed.relatedProjects];
    }
  }
  return config;
}

function validateFiles(value: unknown, label: string): FileAccess {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error(`${label} must be an object`);
  assertKnownKeys(value, ["read", "write"], label);
  const candidate = value as Partial<FileAccess>;
  if (!Array.isArray(candidate.read) || !candidate.read.every((path) => typeof path === "string")) {
    throw new Error(`${label}.read must be a string array`);
  }
  if (!Array.isArray(candidate.write) || !candidate.write.every((path) => typeof path === "string")) {
    throw new Error(`${label}.write must be a string array`);
  }
  return { read: [...candidate.read], write: [...candidate.write] };
}

function validateCommands(value: unknown, path: string): CommandRule[] {
  if (!Array.isArray(value)) throw new Error(`${path}: commands must be an array`);
  return value.map((raw, index) => {
    if (typeof raw === "string") {
      if (raw.trim() === "") throw new Error(`${path}: commands[${index}] must be a non-empty string`);
      return { pattern: raw };
    }
    if (!raw || typeof raw !== "object") {
      throw new Error(`${path}: commands[${index}] must be a string or object`);
    }
    assertKnownKeys(raw, ["pattern", "files", "network"], `${path}: commands[${index}]`);
    const rule = raw as Partial<CommandRule>;
    const patterns = typeof rule.pattern === "string" ? [rule.pattern] : rule.pattern;
    if (!Array.isArray(patterns) || patterns.length === 0 || !patterns.every((pattern) => typeof pattern === "string" && pattern.trim() !== "")) {
      throw new Error(`${path}: commands[${index}].pattern must be a non-empty string or string array`);
    }
    if (Object.hasOwn(rule, "network") && typeof rule.network !== "boolean") {
      throw new Error(`${path}: commands[${index}].network must be boolean`);
    }
    return {
      pattern: typeof rule.pattern === "string" ? rule.pattern : [...patterns],
      ...(Object.hasOwn(rule, "files") ? { files: validateFiles(rule.files, `${path}: commands[${index}].files`) } : {}),
      ...(Object.hasOwn(rule, "network") ? { network: rule.network } : {}),
    };
  });
}

function assertKnownKeys(value: object, allowed: string[], label: string): void {
  const unexpected = Object.keys(value).filter((key) => !allowed.includes(key));
  if (unexpected.length > 0) throw new Error(`${label}: unknown field ${JSON.stringify(unexpected[0])}`);
}

export function relatedProjectsPrompt(config: SandboxConfig, projectRoot: string): string | undefined {
  const paths = [...new Set(config.relatedProjects.map((path) => expandConfiguredPath(path, projectRoot)))];
  if (paths.length === 0) return undefined;
  return `Related Directories: ${paths.join(", ")}`;
}

export function expandConfiguredPath(value: string, cwd: string): string {
  const expanded = value
    .replaceAll("$CWD", cwd)
    .replaceAll("$HOME", homedir())
    .replaceAll("$TMPDIR", tmpdir());
  if (expanded === "~") return homedir();
  if (expanded.startsWith("~/")) return join(homedir(), expanded.slice(2));
  return resolve(cwd, expanded);
}

function expandHome(path: string): string {
  if (path === "~") return homedir();
  if (path.startsWith("~/")) return join(homedir(), path.slice(2));
  return path;
}

export function executableRuntimePaths(executable: string, workerPath: string): string[] {
  return [dirname(executable), workerPath];
}
