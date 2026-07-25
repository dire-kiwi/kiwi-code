import { dirname, isAbsolute, relative, resolve, sep } from "node:path";
import { lstat, realpath } from "node:fs/promises";
import {
  expandConfiguredPath,
  RUNTIME_READ_PATHS,
  RUNTIME_WRITE_PATHS,
  type FileAccess,
  type SandboxConfig,
} from "./config.ts";

export type PolicyDecision = {
  rule: string;
  unrestricted: boolean;
  read: string[];
  write: string[];
  deniedWrite: string[];
  network: boolean;
};

export async function resolveDecision(
  config: SandboxConfig,
  command: string,
  cwd: string,
  extraRuntimeRead: string[] = [],
  projectRoot = cwd,
): Promise<PolicyDecision> {
  let files: FileAccess | undefined = config.defaults;
  let rule = "defaults";
  let unrestricted = false;
  let network = config.network;
  let includeRelatedProjects = true;

  if (isSimpleCommand(command)) {
    for (const candidate of config.commands) {
      const patterns = typeof candidate.pattern === "string" ? [candidate.pattern] : candidate.pattern;
      const matchedPattern = patterns.find((pattern) => globMatches(pattern, command.trim()));
      if (!matchedPattern) continue;
      files = candidate.files;
      rule = matchedPattern;
      unrestricted = candidate.files === undefined;
      includeRelatedProjects = false;
      network = candidate.network ?? config.network;
      break;
    }
  }

  if (unrestricted) return { rule, unrestricted, read: [], write: [], deniedWrite: [], network };
  const relatedProjects = includeRelatedProjects
    ? await resolvePaths(config.relatedProjects, projectRoot)
    : [];
  const read = await resolvePaths([...RUNTIME_READ_PATHS, ...extraRuntimeRead, ...(files?.read ?? [])], cwd);
  const write = await resolvePaths([...RUNTIME_WRITE_PATHS, ...(files?.write ?? [])], cwd);
  return {
    rule,
    unrestricted,
    read: uniqueSorted([...read, ...write, ...relatedProjects]),
    write: uniqueSorted([...write, ...relatedProjects]),
    deniedWrite: [],
    network,
  };
}

export function isSimpleCommand(command: string): boolean {
  let quote: "'" | '"' | undefined;
  let escaped = false;
  for (let index = 0; index < command.length; index++) {
    const character = command[index]!;
    if (escaped) {
      escaped = false;
      continue;
    }
    if (quote !== "'" && character === "\\") {
      escaped = true;
      continue;
    }
    if (character === "'" || character === '"') {
      if (!quote) quote = character;
      else if (quote === character) quote = undefined;
      continue;
    }
    if (quote === "'") continue;
    if (character === "`" || character === "\n" || character === "\r") return false;
    if (character === "$" && command[index + 1] === "(") return false;
    if (!quote && ";&|<>".includes(character)) return false;
  }
  return !quote && !escaped && command.trim() !== "";
}

export function globMatches(pattern: string, value: string): boolean {
  let expression = "^";
  for (let index = 0; index < pattern.length; index++) {
    const character = pattern[index]!;
    if (character === "*") expression += ".*";
    else if (character === "?") expression += ".";
    else if (character === "[") {
      const end = pattern.indexOf("]", index + 1);
      if (end < 0) throw new Error(`Invalid command pattern ${JSON.stringify(pattern)}: unterminated character class`);
      expression += pattern.slice(index, end + 1);
      index = end;
    } else expression += escapeRegex(character);
  }
  return new RegExp(`${expression}$`).test(value);
}

export async function assertPathAllowed(path: string, decision: PolicyDecision, mode: "read" | "write"): Promise<string> {
  const canonical = await canonicalPath(path);
  if (mode === "write" && decision.deniedWrite.some((denied) => isWithin(denied, canonical))) {
    throw new Error(`write access denied for protected Kiwi Sandbox path: ${path}`);
  }
  if (decision.unrestricted) return canonical;
  const allowed = mode === "read" ? decision.read : decision.write;
  if (!allowed.some((root) => isWithin(root, canonical))) {
    throw new Error(`${mode} access denied by ${decision.rule} policy: ${path}`);
  }
  return canonical;
}

export async function assertWorkingDirectory(
  projectRoot: string,
  requested: string,
  relatedProjects: string[] = [],
): Promise<string> {
  if (!isAbsolute(requested)) requested = resolve(projectRoot, requested);
  const [roots, cwd] = await Promise.all([
    Promise.all([
      canonicalPath(projectRoot),
      ...relatedProjects.map((path) => canonicalPath(expandConfiguredPath(path, projectRoot))),
    ]),
    realpath(requested),
  ]);
  if (!roots.some((root) => isWithin(root, cwd))) {
    throw new Error(`Working directory must remain inside project root ${projectRoot} or a related project`);
  }
  const stat = await lstat(cwd);
  if (!stat.isDirectory()) throw new Error(`Working directory is not a directory: ${requested}`);
  return cwd;
}

export async function canonicalPath(path: string): Promise<string> {
  let current = resolve(path);
  const suffix: string[] = [];
  while (true) {
    try {
      const existing = await realpath(current);
      return resolve(existing, ...suffix.reverse());
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
      const parent = dirname(current);
      if (parent === current) throw new Error(`Cannot resolve existing ancestor of ${path}`);
      suffix.push(current.slice(parent.length + (parent.endsWith(sep) ? 0 : 1)));
      current = parent;
    }
  }
}

function isWithin(root: string, path: string): boolean {
  const child = relative(root, path);
  return child === "" || (!child.startsWith(`..${sep}`) && child !== ".." && !isAbsolute(child));
}

async function resolvePaths(paths: string[], cwd: string): Promise<string[]> {
  return uniqueSorted(await Promise.all(paths.map((path) => canonicalPath(expandConfiguredPath(path, cwd)))));
}

function uniqueSorted(values: string[]): string[] {
  return [...new Set(values.filter(Boolean))].sort();
}

function escapeRegex(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
