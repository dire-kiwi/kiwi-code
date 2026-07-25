import { relatedProjectsPrompt, type SandboxConfig } from "./config.ts";
import { discoverGitWorktrees, GIT_WORKTREES_PROMPT } from "./worktrees.ts";

export async function sandboxSystemPrompt(
  config: SandboxConfig,
  projectRoot: string,
): Promise<string | undefined> {
  const worktrees = await discoverGitWorktrees(projectRoot);
  const sections = [
    relatedProjectsPrompt(config, projectRoot),
    worktrees.isRepository ? GIT_WORKTREES_PROMPT : undefined,
  ].filter((section): section is string => section !== undefined);
  return sections.length > 0 ? sections.join("\n") : undefined;
}
