import { lstat, readdir, readFile, realpath, stat } from "node:fs/promises";
import { dirname, isAbsolute, join, relative, resolve, sep } from "node:path";

export type GitWorktreeAccess = {
  isRepository: boolean;
  roots: string[];
};

type GitRepositoryLayout = {
  worktreeRoot: string;
  commonDirectory: string;
};

export const GIT_WORKTREES_PROMPT = "All Git worktrees for the current repository are accessible.";

export async function discoverGitWorktrees(projectRoot: string): Promise<GitWorktreeAccess> {
  const layout = await findRepositoryLayout(projectRoot);
  if (!layout) return { isRepository: false, roots: [] };

  const roots = [layout.worktreeRoot, layout.commonDirectory];
  const mainWorktree = await worktreeForGitDirectory(layout.commonDirectory);
  if (mainWorktree) roots.push(mainWorktree);

  const registrationsDirectory = join(layout.commonDirectory, "worktrees");
  let registrations: string[] = [];
  try {
    registrations = (await readdir(registrationsDirectory, { withFileTypes: true }))
      .filter((entry) => entry.isDirectory())
      .map((entry) => join(registrationsDirectory, entry.name));
  } catch {
    // Repositories without linked worktrees do not have this directory. If it
    // cannot be inspected, retain only the roots already validated above.
  }

  const linkedWorktrees = await Promise.all(
    registrations.map((registration) => linkedWorktreeRoot(registration, layout.commonDirectory)),
  );
  roots.push(...linkedWorktrees.filter((path): path is string => path !== undefined));
  return { isRepository: true, roots: minimalRoots(roots) };
}

async function findRepositoryLayout(projectRoot: string): Promise<GitRepositoryLayout | undefined> {
  let current: string;
  try {
    current = await realpath(projectRoot);
  } catch {
    return undefined;
  }

  while (true) {
    const dotGit = join(current, ".git");
    const gitDirectory = await resolveGitDirectory(dotGit);
    if (gitDirectory) {
      return {
        worktreeRoot: current,
        commonDirectory: await resolveCommonDirectory(gitDirectory),
      };
    }
    const parent = dirname(current);
    if (parent === current) return undefined;
    current = parent;
  }
}

async function resolveGitDirectory(dotGit: string): Promise<string | undefined> {
  try {
    const info = await stat(dotGit);
    if (info.isDirectory()) return await validatedGitDirectory(dotGit);
    if (!info.isFile()) return undefined;
    const pointer = parseGitDirectoryPointer(await readFile(dotGit, "utf8"));
    if (!pointer) return undefined;
    return await validatedGitDirectory(isAbsolute(pointer) ? pointer : resolve(dirname(dotGit), pointer));
  } catch {
    return undefined;
  }
}

async function validatedGitDirectory(path: string): Promise<string | undefined> {
  try {
    const directory = await realpath(path);
    return (await stat(join(directory, "HEAD"))).isFile() ? directory : undefined;
  } catch {
    return undefined;
  }
}

async function resolveCommonDirectory(gitDirectory: string): Promise<string> {
  try {
    const value = stripLineEnding(await readFile(join(gitDirectory, "commondir"), "utf8"));
    if (value) {
      const commonDirectory = await validatedGitDirectory(
        isAbsolute(value) ? value : resolve(gitDirectory, value),
      );
      if (commonDirectory) return commonDirectory;
    }
  } catch {
    // A main worktree and repositories without linked-worktree metadata use
    // their Git directory directly as the common directory.
  }
  return gitDirectory;
}

async function worktreeForGitDirectory(gitDirectory: string): Promise<string | undefined> {
  const root = dirname(gitDirectory);
  const resolved = await resolveGitDirectory(join(root, ".git"));
  return resolved === gitDirectory ? root : undefined;
}

async function linkedWorktreeRoot(
  registrationDirectory: string,
  expectedCommonDirectory: string,
): Promise<string | undefined> {
  try {
    const value = stripLineEnding(await readFile(join(registrationDirectory, "gitdir"), "utf8"));
    if (!value) return undefined;
    const gitFile = isAbsolute(value) ? value : resolve(registrationDirectory, value);
    const root = await realpath(dirname(gitFile));
    if (!(await lstat(root)).isDirectory()) return undefined;
    const [registeredGitDirectory, candidateGitDirectory] = await Promise.all([
      realpath(registrationDirectory),
      resolveGitDirectory(join(root, ".git")),
    ]);
    if (registeredGitDirectory !== candidateGitDirectory) return undefined;
    return (await resolveCommonDirectory(registeredGitDirectory)) === expectedCommonDirectory
      ? root
      : undefined;
  } catch {
    return undefined;
  }
}

function parseGitDirectoryPointer(value: string): string | undefined {
  const line = stripLineEnding(value);
  return line.startsWith("gitdir: ") && line.length > "gitdir: ".length
    ? line.slice("gitdir: ".length)
    : undefined;
}

function stripLineEnding(value: string): string {
  return value.replace(/[\r\n]+$/, "");
}

function minimalRoots(paths: string[]): string[] {
  const roots: string[] = [];
  for (const path of [...new Set(paths)].sort((left, right) => left.length - right.length || left.localeCompare(right))) {
    if (!roots.some((root) => isWithin(root, path))) roots.push(path);
  }
  return roots.sort();
}

function isWithin(root: string, path: string): boolean {
  const child = relative(root, path);
  return child === "" || (!child.startsWith(`..${sep}`) && child !== ".." && !isAbsolute(child));
}
