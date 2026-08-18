package server

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializePiExtensionsRemovesOrchestrationArtifacts(t *testing.T) {
	directory := t.TempDir()
	obsolete := []string{
		filepath.Join(directory, "extensions", "kiwi-code-child-threads.ts"),
		filepath.Join(directory, "extensions", "kiwi-code-workflows.ts"),
		filepath.Join(directory, "extensions", "kiwi-code-skill-forks.ts"),
		filepath.Join(directory, "skills", "kiwi-code-planner", "SKILL.md"),
	}
	for _, path := range obsolete {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("obsolete"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	paths, err := materializePiExtensions(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 {
		t.Fatalf("materialized extension paths = %v, want title, activity, and context", paths)
	}
	for _, path := range obsolete {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("obsolete artifact still exists at %q: %v", path, err)
		}
	}
	browserSkill, err := os.ReadFile(filepath.Join(directory, "skills", "kiwi-code-in-app-browser", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(browserSkill, []byte("context: fork")) {
		t.Fatal("browser skill still requests a forked context")
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
	extensionPath := filepath.Join(t.TempDir(), "kiwi-code-thread-context.mjs")
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
		"KIWI_CODE_THREAD_ENDPOINT=http://127.0.0.1:4001/api/projects/project/threads/thread",
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

func TestPiTitleExtensionCompletesThroughModelRegistry(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}

	extensionSource := string(piThreadTitleExtension)
	for _, replacement := range [][2]string{
		{`import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";`, ""},
		{`function titleModelSelection(): { provider: string; modelId: string } {`, `function titleModelSelection() {`},
		{`type TitleReasoningEffort = "minimal" | "low" | "medium" | "high" | "xhigh" | "max";`, ""},
		{`function titleReasoningEffort(model: { reasoning?: boolean }): TitleReasoningEffort | undefined {`, `function titleReasoningEffort(model) {`},
		{`return level === "off" ? undefined : (level as TitleReasoningEffort);`, `return level === "off" ? undefined : level;`},
		{`type UpdatedThread = {
	title?: unknown;
};`, ""},
		{`function hasUserMessage(ctx: ExtensionContext): boolean {`, `function hasUserMessage(ctx) {`},
		{`function cleanTitle(value: string): string {`, `function cleanTitle(value) {`},
		{`function titlePrompt(firstMessage: string): string {`, `function titlePrompt(firstMessage) {`},
		{`export default function (pi: ExtensionAPI) {`, `export default function (pi) {`},
		{`let controller: AbortController | undefined;`, `let controller;`},
		{`.filter((part): part is { type: "text"; text: string } => part.type === "text")`, `.filter((part) => part.type === "text")`},
		{`const updated = await updateResponse.json() as UpdatedThread;`, `const updated = await updateResponse.json();`},
	} {
		if count := strings.Count(extensionSource, replacement[0]); count != 1 {
			t.Fatalf("Pi title source contains %d copies of %q, want 1", count, replacement[0])
		}
		extensionSource = strings.Replace(extensionSource, replacement[0], replacement[1], 1)
	}
	extensionPath := filepath.Join(t.TempDir(), "kiwi-code-thread-title.mjs")
	if err := os.WriteFile(extensionPath, []byte(extensionSource), 0o600); err != nil {
		t.Fatal(err)
	}

	harnessPath := filepath.Join(t.TempDir(), "pi-title.mjs")
	harness := `
import { pathToFileURL } from "node:url";

const model = {
	provider: "xai-auth",
	id: "grok-composer-2.5-fast",
	api: "xai-responses",
	reasoning: false,
};
const completions = [];
const patches = [];
const notifications = [];
let sessionName = "";
const entries = [];

globalThis.fetch = async (url, init) => {
	if (String(url) !== process.env.KIWI_CODE_THREAD_ENDPOINT) throw new Error("unexpected URL: " + url);
	if (init?.method !== "PATCH") throw new Error("unexpected method: " + init?.method);
	patches.push(JSON.parse(init.body));
	return {
		ok: true,
		status: 200,
		async json() { return { title: patches[0].title }; },
		async text() { return ""; },
	};
};

const handlers = new Map();
const pi = {
	on(event, handler) { handlers.set(event, handler); },
	setSessionName(name) { sessionName = name; },
	appendEntry(type, data) { entries.push({ type, data }); },
};
const extension = await import(pathToFileURL(process.env.PI_TITLE_EXTENSION).href);
extension.default(pi);

const context = {
	hasUI: true,
	ui: { notify(message, level) { notifications.push({ message, level }); } },
	sessionManager: { getBranch() { return []; } },
	modelRegistry: {
		find(provider, modelId) {
			if (provider !== model.provider || modelId !== model.id) return undefined;
			return model;
		},
		hasConfiguredAuth(found) { return found === model; },
		async complete(found, request, options) {
			completions.push({ found, request, options });
			return { content: [{ type: "text", text: "Name Custom Provider Thread" }] };
		},
	},
};

handlers.get("session_start")({}, context);
handlers.get("before_agent_start")({ prompt: "Please rename this thread." }, context);
for (let attempt = 0; attempt < 20 && entries.length === 0 && notifications.length === 0; attempt += 1) {
	await new Promise((resolve) => setImmediate(resolve));
}

if (notifications.length !== 0) throw new Error("unexpected notifications: " + JSON.stringify(notifications));
if (completions.length !== 1) throw new Error("expected one registry completion, got " + completions.length);
if (completions[0].found.api !== "xai-responses") throw new Error("completed unexpected api: " + completions[0].found.api);
if (completions[0].options?.apiKey) throw new Error("registry complete should resolve auth itself");
if (patches.length !== 1 || patches[0].title !== "Name Custom Provider Thread" || patches[0].autoGenerated !== true) {
	throw new Error("unexpected patch: " + JSON.stringify(patches));
}
if (sessionName !== "Name Custom Provider Thread") throw new Error("session name = " + sessionName);
if (entries.length !== 1 || entries[0].type !== "kiwi-code-thread-title") {
	throw new Error("unexpected entries: " + JSON.stringify(entries));
}
process.stdout.write("title named\n");
`
	if err := os.WriteFile(harnessPath, []byte(harness), 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(nodePath, "--unhandled-rejections=strict", harnessPath)
	command.Env = append(os.Environ(),
		"KIWI_CODE_THREAD_ENDPOINT=http://127.0.0.1:4001/api/projects/project/threads/thread",
		"KIWI_CODE_TITLE_MODEL=xai-auth/grok-composer-2.5-fast",
		"KIWI_CODE_TITLE_THINKING=off",
		"PI_TITLE_EXTENSION="+extensionPath,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run Pi title extension: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "title named") {
		t.Fatalf("Pi title harness did not finish: %s", output)
	}
}
