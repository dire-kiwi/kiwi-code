---
name: merge-to-main
description: Commits the completed changes on the current feature branch, merges the local main branch into it, validates the result, and then merges the feature branch into local main. Use when the user asks to land, finish, or merge the current branch to main.
---

# Merge to main

Carry out the complete local landing workflow: commit the current work, merge `main` into the current branch, and then merge the current branch into `main`.

## Rules

- Use the local `main` branch. Do not fetch, pull, push, delete branches, or remove the current worktree unless the user explicitly asks.
- Refuse to run from `main` or a detached HEAD; this workflow requires a named feature branch.
- Never discard changes, use a destructive reset, bypass hooks, or force-update a branch.
- Review the status and diffs before staging. Commit all changes that belong to the completed task, including intended new files and deletions. If unrelated or suspicious changes are present, stop and ask the user what to include.
- Preserve both sides when resolving merge conflicts. Do not blindly choose `ours` or `theirs`. If a conflict cannot be resolved confidently, abort that merge and report the blocker.
- The `main` worktree must be clean before it is touched. Never overwrite, stash, or commit changes found there.
- Run the repository's relevant tests before landing and again after merging `main`. Do not merge known failing work into `main`.

## Workflow

1. **Inspect the repository.**
   - Find the repository root with `git rev-parse --show-toplevel` and run the workflow from there.
   - Record the feature branch with `git symbolic-ref --quiet --short HEAD`.
   - Verify that the branch is neither empty nor `main`, that local `refs/heads/main` exists, and that no merge, rebase, cherry-pick, or revert is already in progress.
   - Inspect `git status --short --branch`, the unstaged diff, the staged diff, and relevant untracked files.

2. **Locate and check `main`.**
   - Use `git worktree list --porcelain` to locate the worktree whose branch is `refs/heads/main`; paths may contain spaces.
   - If `main` has no worktree, create a temporary worktree for it and remember to remove only that temporary worktree after a successful, clean completion.
   - Verify with `git -C <main-worktree> symbolic-ref --short HEAD` that it is on `main`.
   - Require `git -C <main-worktree> status --porcelain` to be empty. Stop without changing it otherwise.

3. **Test and commit the feature work.**
   - Run the relevant tests or checks for the task.
   - Stage the intended task changes, then review the staged diff.
   - Create a normal commit with a concise message that describes the completed work. Do not amend an existing commit unless the user requested it.
   - If there are no uncommitted changes, do not create an empty commit; continue with the existing feature commits.
   - Require the feature worktree to be clean before merging.

4. **Merge local `main` into the feature branch.**
   - Record the current `main` tip.
   - Run `git merge --no-edit main` on the feature branch.
   - Resolve conflicts carefully, stage the resolutions, and complete the merge commit. If safe resolution is not possible, run `git merge --abort` and stop.
   - Run the relevant tests on the merged result and require a clean worktree. Commit only if conflict resolution left a merge to complete; do not create an extra empty commit.

5. **Merge the feature branch into `main`.**
   - Immediately recheck that the `main` worktree is on `main`, clean, and still at the tip merged into the feature branch.
   - If `main` advanced, return to the feature branch, merge the new `main`, rerun the tests, and recheck both worktrees.
   - From the `main` worktree, run `git merge --ff-only refs/heads/<feature-branch>`. The fast-forward requirement proves that the feature branch contains the current `main` and keeps conflict resolution out of the `main` worktree.
   - Never replace a failed fast-forward with a forced update. Merge the latest `main` into the feature branch and retry instead.

6. **Verify and report.**
   - Verify that `main` and the feature branch resolve to the same commit.
   - Verify that both worktrees are clean.
   - If a temporary `main` worktree was created by this workflow, remove it only after confirming it is clean.
   - Report the feature commit, resulting `main` commit, tests run, and that nothing was pushed.
