import assert from "node:assert/strict";
import { mkdtemp, mkdir, realpath, writeFile } from "node:fs/promises";
import { homedir, tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { configPaths, defaultConfig, GLOBAL_CONFIG_PATH, loadConfig, relatedProjectsPrompt } from "../src/config.ts";
import { assertPathAllowed, assertWorkingDirectory, isSimpleCommand, resolveDecision } from "../src/policy.ts";
import { createSeatbeltProfile } from "../src/profile.ts";

test("uses the requested global and project config paths", () => {
  assert.deepEqual(configPaths("/project"), [GLOBAL_CONFIG_PATH, "/project/.config/kiwi-sandbox.json"]);
});

test("project config replaces global top-level fields", async () => {
  const root = await mkdtemp(join(tmpdir(), "kiwi-sandbox-config-"));
  const global = join(root, "global.json");
  const project = join(root, "project.json");
  await writeFile(global, JSON.stringify({ defaults: { read: ["/global"], write: [] }, network: false }));
  await writeFile(project, JSON.stringify({ commands: [{ pattern: "gh *" }], network: true, relatedProjects: ["../shared", "~/personal"] }));
  const config = await loadConfig([global, project]);
  assert.deepEqual(config.defaults, { read: ["/global"], write: [] });
  assert.equal(config.commands[0]?.pattern, "gh *");
  assert.equal(config.network, true);
  assert.deepEqual(config.relatedProjects, ["../shared", "~/personal"]);
});

test("relatedProjects is rejected outside the project config", async () => {
  const root = await mkdtemp(join(tmpdir(), "kiwi-sandbox-config-"));
  const global = join(root, "global.json");
  await writeFile(global, JSON.stringify({ relatedProjects: ["../shared"] }));
  await assert.rejects(() => loadConfig([global]), /only allowed in the project config/);
});

test("related project prompt omits ordinary read and write roots", () => {
  const config = defaultConfig();
  config.defaults = { read: ["private-read"], write: ["private-write"] };
  config.relatedProjects = ["../shared", "../shared", "~/personal"];
  const prompt = relatedProjectsPrompt(config, "/workspace/current");
  assert.match(prompt!, /^Related Directories: \/workspace\/shared, /);
  assert.doesNotMatch(prompt!, /private-read|private-write|sandbox|filesystem|access/i);
  assert.equal(prompt!.match(/\/workspace\/shared/g)?.length, 1);
});

test("unknown configuration fields fail closed", async () => {
  const root = await mkdtemp(join(tmpdir(), "kiwi-sandbox-config-"));
  const path = join(root, "sandbox.json");
  await writeFile(path, JSON.stringify({ netowrk: false }));
  await assert.rejects(() => loadConfig([path]), /unknown field.*netowrk/);
  await writeFile(path, JSON.stringify({ commands: [{ pattern: "git *", netowrk: false }] }));
  await assert.rejects(() => loadConfig([path]), /unknown field.*netowrk/);
});

test("command strings are shorthand for unrestricted filesystem rules", async () => {
  const root = await mkdtemp(join(tmpdir(), "kiwi-sandbox-config-"));
  const path = join(root, "sandbox.json");
  await writeFile(path, JSON.stringify({ commands: ["gh", "gh *"] }));
  const config = await loadConfig([path]);
  assert.deepEqual(config.commands, [{ pattern: "gh" }, { pattern: "gh *" }]);
  const decision = await resolveDecision(config, "gh issue list", root);
  assert.equal(decision.unrestricted, true);
});

test("command rule patterns can be grouped in a list", async () => {
  const root = await mkdtemp(join(tmpdir(), "kiwi-sandbox-config-"));
  const path = join(root, "sandbox.json");
  await writeFile(path, JSON.stringify({ commands: [{ pattern: ["gh", "gh *"], network: true }] }));
  const config = await loadConfig([path]);
  assert.deepEqual(config.commands, [{ pattern: ["gh", "gh *"], network: true }]);
  assert.equal((await resolveDecision(config, "gh", root)).rule, "gh");
  assert.equal((await resolveDecision(config, "gh issue list", root)).rule, "gh *");
});

test("built-in defaults run arbitrary commands without network or broad file access", async () => {
  const cwd = await mkdtemp(join(tmpdir(), "kiwi-sandbox-defaults-"));
  const config = defaultConfig();
  const decision = await resolveDecision(config, "any-command --with arguments", cwd);

  assert.equal(config.commands.length, 0);
  assert.equal(decision.rule, "defaults");
  assert.equal(decision.network, false);
  await assertPathAllowed(join(cwd, "new-file.txt"), decision, "write");
  await assert.rejects(
    () => assertPathAllowed(join(homedir(), ".ssh", "id_ed25519"), decision, "read"),
    /read access denied/,
  );
});

test("working directory remains readable and writable under every constrained policy", async () => {
  const parent = await mkdtemp(join(tmpdir(), "kiwi-sandbox-implicit-cwd-"));
  const cwd = join(parent, "project");
  await mkdir(cwd);
  const config = defaultConfig();
  config.defaults = { read: [], write: [] };
  config.commands = [{ pattern: "locked *", files: { read: [], write: [] } }];

  for (const command of ["ordinary-command", "locked command"]) {
    const decision = await resolveDecision(config, command, cwd);
    await assertPathAllowed(join(cwd, "nested", "new-file.txt"), decision, "read");
    await assertPathAllowed(join(cwd, "nested", "new-file.txt"), decision, "write");
    await assert.rejects(
      () => assertPathAllowed(join(parent, "outside.txt"), decision, "write"),
      /write access denied/,
    );
  }
});

test("default policy grants related projects relative to the project root", async () => {
  const parent = await mkdtemp(join(tmpdir(), "kiwi-sandbox-related-"));
  const project = join(parent, "project");
  const nested = join(project, "nested");
  const related = join(parent, "related");
  await Promise.all([mkdir(nested, { recursive: true }), mkdir(related)]);

  const config = defaultConfig();
  config.relatedProjects = ["../related"];
  const decision = await resolveDecision(config, "pwd", nested, [], project);
  await assertPathAllowed(join(related, "new-file.txt"), decision, "write");
  assert.equal(await assertWorkingDirectory(project, related, config.relatedProjects), await realpath(related));

  config.commands = [{
    pattern: "pwd",
    files: { read: ["$CWD"], write: ["$CWD"] },
  }];
  const commandDecision = await resolveDecision(config, "pwd", nested, [], project);
  await assert.rejects(
    () => assertPathAllowed(join(related, "new-file.txt"), commandDecision, "write"),
    /write access denied/,
  );
});

test("command rules can override the global network policy", async () => {
  const cwd = await mkdtemp(join(tmpdir(), "kiwi-sandbox-policy-"));
  const config = defaultConfig();
  config.network = true;
  config.commands = [{ pattern: "git *", network: false }];
  const decision = await resolveDecision(config, "git status", cwd);
  assert.equal(decision.unrestricted, true);
  assert.equal(decision.network, false);
  assert.doesNotMatch(createSeatbeltProfile(decision), /\(allow network\*\)/);

  config.network = false;
  config.commands = [{ pattern: "gh *", network: true }];
  assert.equal((await resolveDecision(config, "gh api user", cwd)).network, true);
});

test("omitted command files means unrestricted", async () => {
  const cwd = await mkdtemp(join(tmpdir(), "kiwi-sandbox-policy-"));
  const config = defaultConfig();
  config.commands = [{ pattern: "gh *" }];
  const decision = await resolveDecision(config, "gh issue list", cwd);
  assert.equal(decision.unrestricted, true);
  assert.equal(decision.rule, "gh *");
});

test("protected writes remain denied under unrestricted command rules", async () => {
  const cwd = await mkdtemp(join(tmpdir(), "kiwi-sandbox-policy-"));
  const config = defaultConfig();
  config.commands = [{ pattern: "gh *" }];
  const decision = await resolveDecision(config, "gh issue list", cwd);
  decision.deniedWrite = [join(cwd, ".config", "kiwi-sandbox.json")];
  const profile = createSeatbeltProfile(decision);
  assert.match(profile, /\(allow file-write\*\)/);
  assert.match(profile, /\(deny file-write\*/);
});

test("compound commands cannot inherit an unrestricted rule", async () => {
  const cwd = await mkdtemp(join(tmpdir(), "kiwi-sandbox-policy-"));
  const config = defaultConfig();
  config.commands = [{ pattern: "gh *" }];
  const decision = await resolveDecision(config, "gh status; cat ~/.ssh/id_ed25519", cwd);
  assert.equal(decision.unrestricted, false);
  assert.equal(decision.rule, "defaults");
});

test("simple command detector rejects composition and substitution", () => {
  assert.equal(isSimpleCommand('gh issue create --title "a; b"'), true);
  assert.equal(isSimpleCommand("gh status && cat secret"), false);
  assert.equal(isSimpleCommand("gh api $(cat secret)"), false);
  assert.equal(isSimpleCommand("gh status > /tmp/result"), false);
});

test("Seatbelt profiles remain deny-by-default", async () => {
  const cwd = await mkdtemp(join(tmpdir(), "kiwi-sandbox-profile-"));
  await mkdir(join(cwd, "write"));
  const config = defaultConfig();
  config.defaults = { read: [cwd], write: [join(cwd, "write")] };
  config.network = false;
  const decision = await resolveDecision(config, "pwd", cwd);
  decision.deniedWrite = [join(cwd, ".config", "kiwi-sandbox.json")];
  const profile = createSeatbeltProfile(decision);
  assert.match(profile, /\(deny default\)/);
  assert.match(profile, /\(allow file-read-data \(literal "\/"\)\)/);
  assert.doesNotMatch(profile, /\(subpath "\/"\)/);
  assert.match(profile, /\(allow file-read\*/);
  assert.match(profile, /\(allow file-write\*/);
  assert.doesNotMatch(profile, /\(allow network\*\)/);
  assert.match(profile, /\(deny file-write\*/);
  assert.match(profile, /kiwi-sandbox\.json/);
});
