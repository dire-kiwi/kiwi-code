#!/usr/bin/env node
import {
  currentProjectId,
  print,
  readFlag,
  readOption,
  rejectUnknownOptions,
  request,
  run,
  usage,
} from "./common.mjs";

run(async () => {
  const args = process.argv.slice(2);
  const explicitProject = readOption(args, "--project") || "";
  const all = readFlag(args, "--all");
  rejectUnknownOptions(args);
  if (args.length > 0 || (all && explicitProject)) {
    usage("Usage: list-threads.mjs [--project <project-id> | --all]");
    return;
  }

  const projects = await request("/api/projects");
  if (all) {
    print(projects.map((project) => ({
      id: project.id,
      name: project.name,
      path: project.path,
      threads: project.threads,
    })));
    return;
  }

  const projectId = currentProjectId(explicitProject);
  const project = projects.find((candidate) => candidate.id === projectId);
  if (!project) throw new Error(`Project not found: ${projectId}`);
  print({
    project: { id: project.id, name: project.name, path: project.path },
    threads: project.threads,
  });
});
