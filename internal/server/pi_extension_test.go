package server

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializePiExtensions(t *testing.T) {
	directory := t.TempDir()
	paths, err := materializePiExtensions(directory)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		contents []byte
	}{
		{name: "dire-mux-thread-title.ts", contents: piThreadTitleExtension},
		{name: "dire-mux-thread-activity.ts", contents: piThreadActivityExtension},
		{name: "dire-mux-thread-context.ts", contents: piThreadContextExtension},
		{name: "dire-mux-child-threads.ts", contents: piChildThreadsExtension},
		{name: "dire-mux-workflows.ts", contents: piWorkflowsExtension},
	}
	if len(paths) != len(tests) {
		t.Fatalf("materialized %d extensions, want %d", len(paths), len(tests))
	}
	usageContents, err := os.ReadFile(filepath.Join(directory, "extensions", "dire-mux-thread-usage.ts"))
	if err != nil || !bytes.Equal(usageContents, piThreadUsageExtension) {
		t.Fatalf("materialized usage extension differs from embedded source: %v", err)
	}
	browserContents, err := os.ReadFile(filepath.Join(directory, "extensions", "dire-mux-browser.ts"))
	if err != nil || !bytes.Equal(browserContents, piBrowserExtension) {
		t.Fatalf("materialized browser extension differs from embedded source: %v", err)
	}
	skillForksContents, err := os.ReadFile(filepath.Join(directory, "extensions", "dire-mux-skill-forks.ts"))
	if err != nil || !bytes.Equal(skillForksContents, piSkillForksExtension) {
		t.Fatalf("materialized skill-forks extension differs from embedded source: %v", err)
	}
	for index, test := range tests {
		path := paths[index]
		if filepath.Base(path) != test.name {
			t.Fatalf("extension path = %q, want %q", path, test.name)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(contents, test.contents) {
			t.Fatalf("materialized %s differs from the embedded source", test.name)
		}
	}

	skillPath := filepath.Join(directory, "skills", "dire-mux-in-app-browser", "SKILL.md")
	skillContents, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(skillContents, piBrowserSkill) {
		t.Fatal("materialized browser skill differs from the embedded source")
	}
	if !bytes.Contains(skillContents, []byte("\ncontext: fork\n")) {
		t.Fatal("Pi browser skill does not run in a forked agent context")
	}

	plannerPath := filepath.Join(directory, "skills", "kiwi-code-planner", "SKILL.md")
	plannerContents, err := os.ReadFile(plannerPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plannerContents, piPlannerSkill) {
		t.Fatal("materialized planner skill differs from the embedded source")
	}
	for _, expected := range [][]byte{
		[]byte("\ncontext: fork\n"),
		[]byte("\nagent: Plan\n"),
		[]byte("publish_thread_plan"),
		[]byte("$ARGUMENTS"),
	} {
		if !bytes.Contains(plannerContents, expected) {
			t.Fatalf("Pi planner skill does not contain %q", expected)
		}
	}
}

func TestPiBrowserExtensionHarness(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	versionCheck := exec.Command(nodePath, "-e", `
const [major, minor] = process.versions.node.split(".").map(Number);
if (major < 22 || (major === 22 && minor < 19)) process.exit(1);
`)
	if err := versionCheck.Run(); err != nil {
		t.Skip("Pi's extension runtime requires Node.js 22.19 or newer")
	}
	piPath, err := exec.LookPath("pi")
	if err != nil {
		t.Skip("pi is not installed")
	}
	resolvedPiPath, err := filepath.EvalSymlinks(piPath)
	if err != nil {
		t.Skipf("resolve Pi installation: %v", err)
	}
	piPackageRoot := filepath.Dir(filepath.Dir(resolvedPiPath))
	jitiPath := filepath.Join(piPackageRoot, "node_modules", "jiti", "lib", "jiti.mjs")
	for _, path := range []string{
		filepath.Join(piPackageRoot, "package.json"),
		jitiPath,
		filepath.Join(piPackageRoot, "node_modules", "typebox"),
		filepath.Join(piPackageRoot, "node_modules", "@earendil-works", "pi-ai"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Skipf("Pi test dependency %q is unavailable: %v", path, err)
		}
	}

	directory := t.TempDir()
	paths, err := materializePiExtensions(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("materialized extension paths are missing")
	}
	browserExtensionPath := filepath.Join(directory, "extensions", "dire-mux-browser.ts")
	if _, err := os.Stat(browserExtensionPath); err != nil {
		t.Fatalf("materialized browser extension path is missing: %v", err)
	}
	childThreadsExtensionPath := filepath.Join(directory, "extensions", "dire-mux-child-threads.ts")
	if _, err := os.Stat(childThreadsExtensionPath); err != nil {
		t.Fatalf("materialized child-thread extension path is missing: %v", err)
	}
	workflowsExtensionPath := filepath.Join(directory, "extensions", "dire-mux-workflows.ts")
	if _, err := os.Stat(workflowsExtensionPath); err != nil {
		t.Fatalf("materialized workflow extension path is missing: %v", err)
	}
	skillForksExtensionPath := filepath.Join(directory, "extensions", "dire-mux-skill-forks.ts")
	if _, err := os.Stat(skillForksExtensionPath); err != nil {
		t.Fatalf("materialized skill-forks extension path is missing: %v", err)
	}
	browserSkillPath := filepath.Join(directory, "skills", "dire-mux-in-app-browser")
	plannerSkillPath := filepath.Join(directory, "skills", "kiwi-code-planner")

	nodeModules := filepath.Join(directory, "node_modules")
	scopeDirectory := filepath.Join(nodeModules, "@earendil-works")
	if err := os.MkdirAll(scopeDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	links := []struct {
		name   string
		target string
	}{
		{name: filepath.Join(nodeModules, "typebox"), target: filepath.Join(piPackageRoot, "node_modules", "typebox")},
		{name: filepath.Join(scopeDirectory, "pi-ai"), target: filepath.Join(piPackageRoot, "node_modules", "@earendil-works", "pi-ai")},
		{name: filepath.Join(scopeDirectory, "pi-coding-agent"), target: piPackageRoot},
	}
	for _, link := range links {
		if err := os.Symlink(link.target, link.name); err != nil {
			t.Fatalf("link Pi test dependency %q: %v", link.name, err)
		}
	}

	harnessPath, err := filepath.Abs(filepath.Join("testdata", "pi-browser-extension-harness.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(nodePath, "--unhandled-rejections=strict", harnessPath)
	command.Env = append(os.Environ(),
		"DIRE_MUX_THREAD_ENDPOINT=http://127.0.0.1:43210/api/projects/project/threads/thread",
		"DIRE_MUX_AGENT_TOKEN=browser-agent-capability",
		"PI_BROWSER_EXTENSION="+browserExtensionPath,
		"PI_BROWSER_SKILL="+browserSkillPath,
		"PI_PLANNER_SKILL="+plannerSkillPath,
		"PI_CHILD_THREADS_EXTENSION="+childThreadsExtensionPath,
		"PI_WORKFLOWS_EXTENSION="+workflowsExtensionPath,
		"PI_SKILL_FORKS_EXTENSION="+skillForksExtensionPath,
		"PI_JITI_PATH="+jitiPath,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run Pi browser extension harness: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Pi browser extension harness passed") {
		t.Fatalf("Pi browser extension harness did not finish: %s", output)
	}
}

func TestPiContextExtensionReportsSerializedTerminalUsage(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}

	extensionSource := string(piThreadContextExtension)
	for _, replacement := range [][2]string{
		{`import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";`, ""},
		{`type ContextStatusUpdate = {
	source: "pi-terminal";
	tokens: number | null;
	contextWindow: number;
	percent: number | null;
	model: string;
};`, ""},
		{`function contextStatus(ctx: ExtensionContext): ContextStatusUpdate | undefined {`, `function contextStatus(ctx) {`},
		{`export default function (pi: ExtensionAPI) {`, `export default function (pi) {`},
		{`async function sendContext(status: ContextStatusUpdate): Promise<void> {`, `async function sendContext(status) {`},
		{`function queueContext(ctx: ExtensionContext): Promise<void> {`, `function queueContext(ctx) {`},
		{`let reportInterval: ReturnType<typeof setInterval> | undefined;`, `let reportInterval;`},
	} {
		if count := strings.Count(extensionSource, replacement[0]); count != 1 {
			t.Fatalf("Pi context source contains %d copies of %q, want 1", count, replacement[0])
		}
		extensionSource = strings.Replace(extensionSource, replacement[0], replacement[1], 1)
	}
	extensionPath := filepath.Join(t.TempDir(), "dire-mux-thread-context.mjs")
	if err := os.WriteFile(extensionPath, []byte(extensionSource), 0o600); err != nil {
		t.Fatal(err)
	}

	harnessPath := filepath.Join(t.TempDir(), "pi-context.mjs")
	harness := `
import { pathToFileURL } from "node:url";

const reports = [];
let inFlight = 0;
let maxInFlight = 0;
globalThis.fetch = async (url, init) => {
	if (!String(url).endsWith("/context/status")) throw new Error("unexpected URL: " + url);
	inFlight += 1;
	maxInFlight = Math.max(maxInFlight, inFlight);
	reports.push(JSON.parse(init.body));
	await new Promise((resolve) => setImmediate(resolve));
	inFlight -= 1;
	return { ok: true, status: 200 };
};

const handlers = new Map();
const pi = { on(event, handler) { handlers.set(event, handler); } };
const extension = await import(pathToFileURL(process.env.PI_CONTEXT_EXTENSION).href);
extension.default(pi);

let usage = { tokens: 1200, contextWindow: 200000, percent: 0.6 };
const context = {
	mode: "rpc",
	model: { provider: "openai-codex", id: "gpt-test", contextWindow: 200000 },
	getContextUsage() { return usage; },
};
handlers.get("session_start")({}, context);
context.mode = "tui";
handlers.get("session_start")({}, context);
usage = { tokens: 80000, contextWindow: 200000, percent: 40 };
handlers.get("turn_end")({}, context);
await handlers.get("session_shutdown")();

if (reports.length !== 2) throw new Error("unexpected report count: " + reports.length);
if (maxInFlight !== 1) throw new Error("context requests were not serialized");
if (reports[0].source !== "pi-terminal" || reports[0].tokens !== 1200 || reports[0].percent !== 0.6) {
	throw new Error("unexpected first report: " + JSON.stringify(reports[0]));
}
if (reports[1].tokens !== 80000 || reports[1].contextWindow !== 200000 || reports[1].model !== "openai-codex/gpt-test") {
	throw new Error("unexpected second report: " + JSON.stringify(reports[1]));
}
process.stdout.write("context reported\n");
`
	if err := os.WriteFile(harnessPath, []byte(harness), 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(nodePath, "--unhandled-rejections=strict", harnessPath)
	command.Env = append(os.Environ(),
		"DIRE_MUX_THREAD_ENDPOINT=http://127.0.0.1:4001/api/projects/project/threads/thread",
		"PI_CONTEXT_EXTENSION="+extensionPath,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run Pi context extension: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "context reported") {
		t.Fatalf("Pi context harness did not finish: %s", output)
	}
}

func TestPiActivityFetchFailureIsHandled(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}

	// The application loads this TypeScript directly through Pi, but Dire Mux
	// supports Node 20, before Node's built-in type stripping. Remove the small,
	// known set of type-only fragments so the lifecycle test can run everywhere
	// the project's JavaScript hook tests run.
	extensionSource := string(piThreadActivityExtension)
	for _, replacement := range [][2]string{
		{`import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";`, ""},
		{`import threadUsageExtension from "./dire-mux-thread-usage.ts";`, `const threadUsageExtension = () => {};`},
		{`import browserExtension from "./dire-mux-browser.ts";`, `const browserExtension = () => {};`},
		{`import skillForksExtension from "./dire-mux-skill-forks.ts";`, `const skillForksExtension = () => {};`},
		{`type ActivityState = "working" | "finished" | "idle";`, ""},
		{`export default function (pi: ExtensionAPI) {`, `export default function (pi) {`},
		{`let activePromptStartedAt: string | undefined;`, `let activePromptStartedAt;`},
		{`let heartbeat: ReturnType<typeof setInterval> | undefined;`, `let heartbeat;`},
		{`async function sendActivity(state: ActivityState, promptStartedAt?: string): Promise<void> {`, `async function sendActivity(state, promptStartedAt) {`},
		{`function queueActivity(state: ActivityState, promptStartedAt?: string): Promise<void> {`, `function queueActivity(state, promptStartedAt) {`},
		{`function stopHeartbeat(): void {`, `function stopHeartbeat() {`},
	} {
		if count := strings.Count(extensionSource, replacement[0]); count != 1 {
			t.Fatalf("Pi activity source contains %d copies of %q, want 1", count, replacement[0])
		}
		extensionSource = strings.Replace(extensionSource, replacement[0], replacement[1], 1)
	}
	extensionPath := filepath.Join(t.TempDir(), "dire-mux-thread-activity.mjs")
	if err := os.WriteFile(extensionPath, []byte(extensionSource), 0o600); err != nil {
		t.Fatal(err)
	}

	harnessPath := filepath.Join(t.TempDir(), "pi-activity-failure.mjs")
	harness := `
import { pathToFileURL } from "node:url";

const activities = [];
let inFlight = 0;
let maxInFlight = 0;
globalThis.fetch = async (_url, init) => {
	activities.push(JSON.parse(init.body));
	inFlight += 1;
	maxInFlight = Math.max(maxInFlight, inFlight);
	await new Promise((resolve) => setImmediate(resolve));
	inFlight -= 1;
	throw new Error("dire-mux is unreachable");
};

const handlers = new Map();
const pi = {
	on(event, handler) {
		handlers.set(event, handler);
	},
};

const extension = await import(pathToFileURL(process.env.PI_ACTIVITY_EXTENSION).href);
extension.default(pi);

for (const event of ["agent_start", "agent_settled", "session_shutdown"]) {
	if (!handlers.has(event)) throw new Error("missing " + event + " handler");
}

handlers.get("agent_start")();
handlers.get("agent_settled")();
for (let index = 0; index < 4; index += 1) {
	await new Promise((resolve) => setImmediate(resolve));
}
handlers.get("agent_start")();
await handlers.get("session_shutdown")();
await new Promise((resolve) => setImmediate(resolve));

if (maxInFlight !== 1) throw new Error("activity requests were not serialized");
const states = activities.map(({ state }) => state);
if (states.join(",") !== "working,finished,working,idle") {
	throw new Error("unexpected activity states: " + states.join(","));
}
if (typeof activities[0].promptStartedAt !== "string" || typeof activities[2].promptStartedAt !== "string") {
	throw new Error("working transitions did not include prompt start times");
}
if (activities[1].promptStartedAt || activities[3].promptStartedAt) {
	throw new Error("settled activity included a prompt start time");
}
process.stdout.write("activity failures handled\n");
`
	if err := os.WriteFile(harnessPath, []byte(harness), 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(nodePath, "--unhandled-rejections=strict", harnessPath)
	command.Env = append(os.Environ(),
		"DIRE_MUX_THREAD_ENDPOINT=http://127.0.0.1:1/api/projects/project/threads/thread",
		"PI_ACTIVITY_EXTENSION="+extensionPath,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run Pi activity extension with failing fetch: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "activity failures handled") {
		t.Fatalf("Pi activity harness did not finish: %s", output)
	}
}
