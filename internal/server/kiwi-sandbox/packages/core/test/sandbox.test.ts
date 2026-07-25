import assert from "node:assert/strict";
import { chmod, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { KiwiSandbox } from "../src/sandbox.ts";

async function fixture() {
  const root = await mkdtemp(join(tmpdir(), "kiwi-sandbox-core-"));
  const fake = join(root, "sandbox-exec");
  await writeFile(fake, '#!/bin/sh\nshift 2\nexec "$@"\n');
  await chmod(fake, 0o700);
  const config = join(root, "sandbox.json");
  await writeFile(config, JSON.stringify({
    defaults: { read: ["$CWD"], write: ["$CWD"] },
    commands: [],
    network: false,
  }));
  return { root, fake, config, sandbox: new KiwiSandbox({ projectRoot: root, configPaths: [config], sandboxExecutable: fake }) };
}

test("runs commands through the configured sandbox executable", async () => {
  const { sandbox } = await fixture();
  const chunks: Buffer[] = [];
  const result = await sandbox.runCommand("printf command-ok", { onOutput: (_stream, data) => chunks.push(data) });
  assert.equal(result.exitCode, 0);
  assert.equal(Buffer.concat(chunks).toString(), "command-ok");
});

test("reports signal termination using conventional shell exit codes", async () => {
  const { sandbox } = await fixture();
  const result = await sandbox.runCommand("kill -ABRT $$");
  assert.equal(result.exitCode, 134);
});

test("reports command output that indicates a Seatbelt denial", async () => {
  const { sandbox } = await fixture();
  const chunks: Buffer[] = [];
  const result = await sandbox.runCommand(
    "printf 'cat: secret: Operation not permitted\\n' >&2; exit 1",
    { onOutput: (_stream, data) => chunks.push(data) },
  );
  assert.equal(result.sandboxDenied, true);
  assert.match(Buffer.concat(chunks).toString(), /Kiwi Sandbox: access was denied by the active defaults policy/);

  const ordinary = await sandbox.runCommand("exit 1");
  assert.equal(ordinary.sandboxDenied, false);
});

test("reports Seatbelt denials from sandboxed file workers", async () => {
  const { root, fake, sandbox } = await fixture();
  const path = join(root, "sample.txt");
  await writeFile(path, "sample");
  await writeFile(fake, "#!/bin/sh\necho 'worker: Operation not permitted' >&2\nexit 1\n");
  await assert.rejects(
    () => sandbox.readFile(path),
    /Kiwi Sandbox: read access denied by defaults policy/,
  );
});

test("read, write, and edit use sandboxed file workers", async () => {
  const { root, sandbox } = await fixture();
  const path = join(root, "nested", "file.txt");
  await sandbox.writeFileWithParents(path, "hello world");
  assert.equal((await sandbox.readFile(path)).toString(), "hello world");
  assert.deepEqual(await sandbox.editFile(path, [{ oldText: "world", newText: "sandbox" }]), { replacements: 1 });
  assert.equal(await readFile(path, "utf8"), "hello sandbox");
});

test("sandbox config files cannot rewrite their own active policy", async () => {
  const { config, sandbox } = await fixture();
  await assert.rejects(
    () => sandbox.writeFile(config, JSON.stringify({ commands: ["*"] })),
    /protected Kiwi Sandbox path/,
  );
});

test("additional runtime paths can be protected from unrestricted writes", async () => {
  const { root, fake, config } = await fixture();
  const implementation = join(root, "sandbox-runtime.ts");
  await writeFile(implementation, "protected");
  await writeFile(config, JSON.stringify({ commands: ["write *"] }));
  const sandbox = new KiwiSandbox({
    projectRoot: root,
    configPaths: [config],
    sandboxExecutable: fake,
    protectedWritePaths: [implementation],
  });
  await assert.rejects(() => sandbox.writeFile(implementation, "tampered"), /protected Kiwi Sandbox path/);
  assert.equal(await readFile(implementation, "utf8"), "protected");
});

test("path-specific file tool rules match unquoted absolute paths", async () => {
  const { root, fake, config } = await fixture();
  const path = join(root, "notes with spaces.txt");
  await writeFile(path, "specific rule");
  await writeFile(config, JSON.stringify({
    defaults: { read: [], write: [] },
    commands: [{ pattern: `read ${path}`, files: { read: [path], write: [] } }],
    network: false,
  }));
  const sandbox = new KiwiSandbox({ projectRoot: root, configPaths: [config], sandboxExecutable: fake });
  assert.equal((await sandbox.readFile(path)).toString(), "specific rule");
});

test("file tools reject paths outside configured directories", async () => {
  const { sandbox } = await fixture();
  const outside = join(tmpdir(), `kiwi-sandbox-outside-${process.pid}.txt`);
  await assert.rejects(() => sandbox.writeFile(outside, "denied"), /write access denied/);
});

test("enable and disable toggles sandbox enforcement", async () => {
  const { sandbox } = await fixture();
  const outside = join(tmpdir(), `kiwi-sandbox-disabled-${process.pid}.txt`);
  await assert.rejects(() => sandbox.writeFile(outside, "denied"), /write access denied/);
  sandbox.setEnabled(false);
  assert.equal(sandbox.isEnabled(), false);
  await sandbox.writeFile(outside, "allowed while disabled");
  assert.equal(await readFile(outside, "utf8"), "allowed while disabled");
  sandbox.setEnabled(true);
  await rm(outside, { force: true });
});

test("working directory cannot escape the project", async () => {
  const { sandbox } = await fixture();
  await assert.rejects(() => sandbox.runCommand("pwd", { cwd: tmpdir() }), /must remain inside project root/);
});
