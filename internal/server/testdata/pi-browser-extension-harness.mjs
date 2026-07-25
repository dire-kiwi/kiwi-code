import assert from "node:assert/strict";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { pathToFileURL } from "node:url";

const implementationToolNames = [
  "browser_session",
  "browser_recording",
  "browser_tabs",
  "browser_navigate",
  "browser_snapshot",
  "browser_click",
  "browser_fill",
  "browser_key",
  "browser_wait",
  "browser_evaluate",
  "browser_screenshot",
  "browser_cdp",
];

const { createJiti } = await import(pathToFileURL(process.env.PI_JITI_PATH).href);
const jiti = createJiti(import.meta.url, { interopDefault: false });

const childExtensionModule = await jiti.import(process.env.PI_CHILD_THREADS_EXTENSION);
const childTools = [];
const childHandlers = new Map();
childExtensionModule.default({
  on(name, handler) {
    childHandlers.set(name, [...(childHandlers.get(name) ?? []), handler]);
  },
  registerTool(tool) {
    childTools.push(tool);
  },
});
assert.deepEqual(childTools, []);
assert.deepEqual([...childHandlers.keys()], ["session_start", "session_shutdown"]);

const workflowExtensionModule = await jiti.import(process.env.PI_WORKFLOWS_EXTENSION);
const workflowTools = [];
const workflowHandlers = new Map();
const workflowCommands = new Map();
const workflowShortcuts = new Map();
workflowExtensionModule.default({
  on(name, handler) {
    workflowHandlers.set(name, [...(workflowHandlers.get(name) ?? []), handler]);
  },
  registerCommand(name, command) {
    workflowCommands.set(name, command);
  },
  registerShortcut(name, shortcut) {
    workflowShortcuts.set(name, shortcut);
  },
  registerTool(tool) {
    workflowTools.push(tool);
  },
});
assert.deepEqual(
  workflowTools.map((tool) => tool.name),
  [
    "run_workflow",
    "run_saved_workflow",
    "list_saved_workflows",
    "save_workflow",
    "list_workflows",
    "wait_for_workflow",
    "pause_workflow",
    "resume_workflow",
    "stop_workflow",
  ],
);
assert.deepEqual([...workflowHandlers.keys()], ["session_start", "input", "before_agent_start", "tool_call"]);
assert.deepEqual([...workflowCommands.keys()], ["effort", "workflows"]);
assert.deepEqual([...workflowShortcuts.keys()], ["alt+w"]);
assert.match(workflowTools[0].description, /current human-authored prompt/);

const skillRoot = await mkdtemp(join(tmpdir(), "kiwi-code-skill-forks-"));
const forkedSkillDirectory = join(skillRoot, "deep-research");
const manualSkillDirectory = join(skillRoot, "manual-fork");
const regularSkillDirectory = join(skillRoot, "regular-skill");
await Promise.all([
  mkdir(forkedSkillDirectory),
  mkdir(manualSkillDirectory),
  mkdir(regularSkillDirectory),
]);
const forkedSkillPath = join(forkedSkillDirectory, "SKILL.md");
const manualSkillPath = join(manualSkillDirectory, "SKILL.md");
const regularSkillPath = join(regularSkillDirectory, "SKILL.md");
await Promise.all([
  writeFile(forkedSkillPath, `---
name: deep-research
description: Research a topic in an isolated child
context: fork
agent: Explore
arguments: [topic, scope]
---
Research $topic in $scope. Full request: $ARGUMENTS. First argument: $0. Indexed scope: $ARGUMENTS[1].
`),
  writeFile(manualSkillPath, `---
name: manual-fork
description: Run only when explicitly invoked
context: fork
disable-model-invocation: true
---
Handle the request.
`),
  writeFile(regularSkillPath, `---
name: regular-skill
description: Stay in the parent
---
Handle this normally.
`),
]);

const browserSkillFile = join(process.env.PI_BROWSER_SKILL, "SKILL.md");
const plannerSkillFile = join(process.env.PI_PLANNER_SKILL, "SKILL.md");
const skillCommands = [
  {
    name: "skill:kiwi-code-in-app-browser",
    description: "Control the in-app browser from a forked agent context",
    source: "skill",
    sourceInfo: { path: browserSkillFile, baseDir: process.env.PI_BROWSER_SKILL },
  },
  {
    name: "skill:kiwi-code-planner",
    description: "Plan work and publish it to the parent thread",
    source: "skill",
    sourceInfo: { path: plannerSkillFile, baseDir: process.env.PI_PLANNER_SKILL },
  },
  {
    name: "skill:deep-research",
    description: "Research a topic in an isolated child",
    source: "skill",
    sourceInfo: { path: forkedSkillPath, baseDir: forkedSkillDirectory },
  },
  {
    name: "skill:manual-fork",
    description: "Run only when explicitly invoked",
    source: "skill",
    sourceInfo: { path: manualSkillPath, baseDir: manualSkillDirectory },
  },
  {
    name: "skill:regular-skill",
    description: "Stay in the parent",
    source: "skill",
    sourceInfo: { path: regularSkillPath, baseDir: regularSkillDirectory },
  },
];
const skillForkModule = await jiti.import(process.env.PI_SKILL_FORKS_EXTENSION);
const skillForkTools = [];
const skillForkHandlers = new Map();
skillForkModule.default({
  getCommands() { return skillCommands; },
  getThinkingLevel() { return "high"; },
  on(name, handler) {
    skillForkHandlers.set(name, [...(skillForkHandlers.get(name) ?? []), handler]);
  },
  registerTool(tool) { skillForkTools.push(tool); },
});
assert.deepEqual(
  skillForkTools.map((tool) => tool.name),
  ["run_forked_skill", "publish_thread_plan", "list_thread_plans", "download_thread_plan"],
);
assert.match(skillForkTools[0].description, /does not start or require activation of a Kiwi Code workflow/);
assert.match(skillForkTools[3].description, /then carry out the returned plan in the parent thread/);
assert.deepEqual([...skillForkHandlers.keys()], ["input", "before_agent_start", "resources_discover"]);
assert.deepEqual(skillForkHandlers.get("resources_discover")[0](), { skillPaths: [process.env.PI_PLANNER_SKILL] });

const beforeAgentStart = skillForkHandlers.get("before_agent_start")[0];
const piSkillBlock = `Base prompt
  <skill>
    <name>deep-research</name>
    <description>Research a topic in an isolated child</description>
    <location>${forkedSkillPath}</location>
  </skill>`;
const guidance = beforeAgentStart({ prompt: "Research this", systemPrompt: piSkillBlock }, {});
assert.match(guidance.systemPrompt, /context: fork/);
assert.match(guidance.systemPrompt, /does not start or require activation of a Kiwi Code workflow/);
assert.doesNotMatch(guidance.systemPrompt, new RegExp(`<location>${forkedSkillPath.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}`));
assert.match(guidance.systemPrompt, /<name>deep-research<\/name>/);
assert.match(guidance.systemPrompt, /<name>kiwi-code-in-app-browser<\/name>/);
assert.match(guidance.systemPrompt, /<name>kiwi-code-planner<\/name>/);
assert.doesNotMatch(guidance.systemPrompt, /<name>manual-fork<\/name>/);
assert.doesNotMatch(guidance.systemPrompt, /<name>regular-skill<\/name>/);
const childGuidance = beforeAgentStart({
  prompt: `<kiwi_code_forked_skill name="deep-research">\nTask\n</kiwi_code_forked_skill>`,
  systemPrompt: "Child base prompt",
}, {});
assert.match(childGuidance.systemPrompt, /already executing/);
assert.doesNotMatch(childGuidance.systemPrompt, /<name>deep-research<\/name>/);

const inputHandler = skillForkHandlers.get("input")[0];
const transformed = inputHandler({ text: `/skill:deep-research "alpha beta" "src files"` }, {});
assert.equal(transformed.action, "transform");
assert.match(transformed.text, /run_forked_skill/);
assert.match(transformed.text, /alpha beta/);
const browserSkillInvocation = inputHandler({ text: "/skill:kiwi-code-in-app-browser inspect example.com" }, {});
assert.equal(browserSkillInvocation.action, "transform");
assert.match(browserSkillInvocation.text, /run_forked_skill/);
assert.deepEqual(inputHandler({ text: "/skill:regular-skill request" }, {}), { action: "continue" });
assert.equal(inputHandler({ text: "/skill:manual-fork request" }, {}).action, "transform");

const skillForkRequests = [];
const planRequests = [];
let skillForkResponseResult = "Child research result";
let skillForkCreateState = "starting";
globalThis.fetch = async (url, init = {}) => {
  const request = { url: String(url), init };
  if (request.url.endsWith("/plans/plan-1")) {
    planRequests.push(request);
    return new Response("# Saved plan\n\n1. Implement it.\n", {
      status: 200,
      headers: { "Content-Type": "text/markdown; charset=utf-8" },
    });
  }
  if (request.url.endsWith("/plans")) {
    planRequests.push(request);
    if (request.init.method === "POST") {
      const input = JSON.parse(request.init.body);
      return Response.json({
        id: "plan-1",
        projectId: "project",
        threadId: "thread",
        sourceThreadId: "skill-child",
        title: input.title,
        createdAt: "2026-01-01T00:00:00Z",
        sizeBytes: Buffer.byteLength(input.content),
      }, { status: 201 });
    }
    return Response.json([{
      id: "plan-1",
      projectId: "project",
      threadId: "thread",
      sourceThreadId: "skill-child",
      title: "Saved plan",
      createdAt: "2026-01-01T00:00:00Z",
      sizeBytes: 31,
    }]);
  }
  skillForkRequests.push(request);
  if (request.url.endsWith("/skill-forks")) {
    return Response.json({
      thread: { id: "skill-child", title: "deep-research · Explore" },
      run: {
        id: 7,
        state: skillForkCreateState,
        ...(skillForkCreateState === "finished" ? { output: skillForkResponseResult } : {}),
        startedAt: "2026-01-01T00:00:00Z",
      },
      agent: "pi",
    }, { status: 201 });
  }
  if (request.url.endsWith("/children/skill-child/runs/7")) {
    return Response.json({
      id: 7,
      state: "finished",
      output: skillForkResponseResult,
      startedAt: "2026-01-01T00:00:00Z",
      finishedAt: "2026-01-01T00:00:01Z",
    });
  }
  if (request.url.endsWith("/children/skill-child/close")) {
    return Response.json({ id: "skill-child", title: "deep-research · Explore" });
  }
  if (request.url.endsWith("/skill-forks/skill-child/stop")) {
    return Response.json({ id: "skill-child", title: "deep-research · Explore" });
  }
  throw new Error(`unexpected skill fork request: ${request.url}`);
};

const publishResult = await skillForkTools[1].execute(
  "publish-plan",
  { title: "Implementation plan", content: "# Plan\n\n1. Make the change.\n" },
  undefined,
);
assert.match(publishResult.content[0].text, /plan-1/);
assert.equal(publishResult.details.plan.threadId, "thread");
assert.equal(planRequests[0].url, `${process.env.KIWI_CODE_THREAD_ENDPOINT}/plans`);
assert.equal(planRequests[0].init.method, "POST");
assert.equal(JSON.parse(planRequests[0].init.body).title, "Implementation plan");
assert.equal(new Headers(planRequests[0].init.headers).get("x-kiwi-code-agent-token"), process.env.KIWI_CODE_AGENT_TOKEN);

const listedPlans = await skillForkTools[2].execute("list-plans", {}, undefined);
assert.match(listedPlans.content[0].text, /Saved plan — plan-1/);
assert.equal(listedPlans.details.plans.length, 1);

const downloadedPlan = await skillForkTools[3].execute("download-plan", {}, undefined);
assert.equal(downloadedPlan.content[0].text, "# Saved plan\n\n1. Implement it.\n");
assert.equal(downloadedPlan.details.plan.id, "plan-1");
assert.equal(planRequests.at(-1).url, `${process.env.KIWI_CODE_THREAD_ENDPOINT}/plans/plan-1`);

const skillUpdates = [];
const skillResult = await skillForkTools[0].execute(
  "fork-skill",
  { skill: "deep-research", arguments: `"alpha beta" "src files"` },
  undefined,
  (update) => skillUpdates.push(update),
  { model: { provider: "openai-codex", id: "gpt-test" } },
);
assert.equal(skillResult.content[0].text, "Child research result");
assert.equal(skillResult.details.skill, "deep-research");
assert.equal(skillResult.details.agent, "Explore");
assert.equal(skillResult.details.thread.id, "skill-child");
assert.equal(skillResult.details.closed, true);
assert.equal(skillUpdates.at(-1).details.run.state, "finished");
assert.equal(skillForkRequests.length, 3);
assert.equal(skillForkRequests[0].url, `${process.env.KIWI_CODE_THREAD_ENDPOINT}/skill-forks`);
assert.equal(skillForkRequests[0].init.method, "POST");
const skillHeaders = new Headers(skillForkRequests[0].init.headers);
assert.equal(skillHeaders.get("x-kiwi-code-agent-token"), process.env.KIWI_CODE_AGENT_TOKEN);
const skillRequest = JSON.parse(skillForkRequests[0].init.body);
assert.equal(skillRequest.model, "openai-codex/gpt-test");
assert.equal(skillRequest.thinkingLevel, "high");
assert.equal(skillRequest.agent, "pi");
assert.equal(skillRequest.worktree, false);
assert.equal(skillRequest.title, "deep-research · Explore");
assert.match(skillRequest.prompt, /Research alpha beta in src files\./);
assert.match(skillRequest.prompt, /Full request: "alpha beta" "src files"\./);
assert.match(skillRequest.prompt, /First argument: alpha beta\./);
assert.match(skillRequest.prompt, /Indexed scope: src files\./);
assert.match(skillRequest.prompt, /Explore agent profile/);
assert.match(skillRequest.prompt, new RegExp(forkedSkillDirectory.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));
assert.equal(skillForkRequests[1].url, `${process.env.KIWI_CODE_THREAD_ENDPOINT}/children/skill-child/runs/7`);
assert.equal(skillForkRequests[2].url, `${process.env.KIWI_CODE_THREAD_ENDPOINT}/children/skill-child/close`);

skillForkRequests.length = 0;
skillForkCreateState = "finished";
skillForkResponseResult = "large result ".repeat(6000);
const manualResult = await skillForkTools[0].execute(
  "manual-fork",
  { skill: "manual-fork", arguments: "append-this" },
  undefined,
  undefined,
  {},
);
assert.equal(skillForkRequests.length, 2);
assert.match(JSON.parse(skillForkRequests[0].init.body).prompt, /ARGUMENTS: append-this/);
assert.match(manualResult.content[0].text, /Forked skill result truncated by/);
assert.ok(Buffer.byteLength(manualResult.content[0].text, "utf8") < 52 * 1024);

skillForkRequests.length = 0;
skillForkCreateState = "starting";
skillForkResponseResult = "should be cancelled";
const skillAbort = new AbortController();
await assert.rejects(
  skillForkTools[0].execute(
    "abort-fork",
    { skill: "deep-research", arguments: "cancel me" },
    skillAbort.signal,
    () => skillAbort.abort(),
    { model: { provider: "openai-codex", id: "gpt-test" } },
  ),
  /cancelled/,
);
assert.equal(skillForkRequests.length, 2);
assert.equal(skillForkRequests[1].url, `${process.env.KIWI_CODE_THREAD_ENDPOINT}/skill-forks/skill-child/stop`);

await assert.rejects(
  skillForkTools[0].execute("missing-skill", { skill: "missing" }, undefined, undefined, {}),
  /Unknown context: fork skill/,
);
await rm(skillRoot, { recursive: true, force: true });

const extensionModule = await jiti.import(process.env.PI_BROWSER_EXTENSION);

const tools = [];
const commands = new Map();
const handlers = new Map();
let activeTools = ["read", ...implementationToolNames, "browser_tool_search"];
let fetchCalls = 0;
const api = {
  getActiveTools: () => [...activeTools],
  getAllTools: () =>
    tools.map((tool) => ({
      description: tool.description,
      name: tool.name,
      parameters: tool.parameters,
      promptGuidelines: tool.promptGuidelines,
      sourceInfo: {
        origin: "top-level",
        path: process.env.PI_BROWSER_EXTENSION,
        scope: "temporary",
        source: "test",
      },
    })),
  on(name, handler) {
    handlers.set(name, [...(handlers.get(name) ?? []), handler]);
  },
  registerCommand(name, command) {
    commands.set(name, command);
  },
  registerTool(tool) {
    tools.push(tool);
  },
  setActiveTools(names) {
    activeTools = [...names];
  },
};

globalThis.fetch = async () => {
  fetchCalls += 1;
  throw new Error("fetch must not run while the extension or session starts");
};
extensionModule.default(api);

assert.deepEqual([...handlers.keys()], ["session_start", "session_tree", "resources_discover"]);
assert.deepEqual(tools, []);
assert.deepEqual([...commands.keys()], []);
const sessionStart = handlers.get("session_start")[0];
sessionStart(
  { reason: "startup" },
  { sessionManager: { getBranch: () => [] }, ui: { notify() {} } },
);
assert.deepEqual(
  tools.map((tool) => tool.name),
  [...implementationToolNames, "browser_tool_search"],
);
assert.ok(tools.every((tool) => tool.executionMode === "sequential"));
assert.ok(tools.every((tool) => tool.promptSnippet === undefined));
assert.ok(tools.every((tool) => tool.promptGuidelines === undefined));
assert.deepEqual([...commands.keys()], ["browser"]);
assert.deepEqual(activeTools, ["read", "browser_tool_search"]);
assert.equal(fetchCalls, 0);
handlers.get("session_tree")[0](
  { reason: "tree" },
  { sessionManager: { getBranch: () => [] } },
);
assert.deepEqual(activeTools, ["read", "browser_tool_search"]);

const resources = await handlers.get("resources_discover")[0](
  { reason: "startup", cwd: process.cwd() },
  {},
);
assert.deepEqual(resources, { skillPaths: [process.env.PI_BROWSER_SKILL] });

const loader = tools.find((tool) => tool.name === "browser_tool_search");
const loadResult = await loader.execute(
  "load-one",
  { query: "click a page control", toolNames: ["browser_click"] },
  undefined,
  undefined,
  {},
);
assert.deepEqual(activeTools, [
  "read",
  "browser_tool_search",
  "browser_snapshot",
  "browser_click",
]);
assert.match(loadResult.content[0].text, /Loaded browser tools: browser_snapshot, browser_click/);

activeTools = ["read", ...implementationToolNames, "browser_tool_search"];
sessionStart(
  { reason: "resume" },
  {
    sessionManager: {
      getBranch: () => [
        {
          type: "message",
          message: {
            role: "toolResult",
            toolName: "browser_tool_search",
            addedToolNames: ["browser_snapshot", "browser_click"],
          },
        },
      ],
    },
  },
);
assert.deepEqual(activeTools, [
  "read",
  "browser_tool_search",
  "browser_snapshot",
  "browser_click",
]);
assert.equal(fetchCalls, 0);

const requests = [];
const responses = [];
globalThis.fetch = async (url, init) => {
  requests.push({ url: String(url), init });
  const response = responses.shift();
  if (!response) throw new Error("missing mocked response");
  return response;
};

responses.push(Response.json({ activated: false, reason: "dismissed" }));
const transformedWorkflowInput = await workflowHandlers.get("input")[0](
  { text: "\u2063kiwi-code-no-ultracode\u2063ultracode audit", source: "rpc", images: [] },
  { mode: "rpc" },
);
assert.deepEqual(transformedWorkflowInput, { action: "transform", text: "ultracode audit", images: [] });
assert.equal(requests[0].url, `${process.env.KIWI_CODE_THREAD_ENDPOINT}/workflows/activation`);
assert.equal(JSON.parse(requests[0].init.body).keywordDismissed, true);
requests.length = 0;

function tool(name) {
  const found = tools.find((candidate) => candidate.name === name);
  assert.ok(found, `missing ${name}`);
  return found;
}

responses.push(Response.json({ result: { recording: null, recordings: [] } }));
const recordingStatus = await tool("browser_recording").execute(
  "recording-status", { action: "status" }, undefined, undefined, {},
);
assert.match(recordingStatus.content[0].text, /No browser recording is active/);
assert.deepEqual(JSON.parse(requests.at(-1).init.body), { operation: "recording.status", params: {} });

const recordingId = `rec-${"a".repeat(32)}`;
responses.push(Response.json({ result: {
  id: recordingId, state: "recording", targetId: "page-one", title: "Demonstrate example navigation",
  startedAt: "2026-07-21T12:00:00.000Z", mimeType: "video/webm;codecs=vp9",
  idleTimeoutMs: 300_000, idleDeadlineAt: "2026-07-21T12:05:00.000Z",
} }));
const recordingStart = await tool("browser_recording").execute(
  "recording-start", { action: "start", title: "Demonstrate example navigation" }, undefined, undefined, {},
);
assert.match(recordingStart.content[0].text, /Idle timeout: 300 seconds/);
assert.deepEqual(JSON.parse(requests.at(-1).init.body), {
  operation: "recording.start",
  params: { title: "Demonstrate example navigation", idleTimeoutMs: 300_000 },
});
await assert.rejects(
  tool("browser_recording").execute("recording-stop-missing", { action: "stop" }, undefined, undefined, {}),
  /recordingId is required/,
);
await assert.rejects(
  tool("browser_recording").execute("recording-start-title", { action: "start", title: "Demo" }, undefined, undefined, {}),
  /2–12 word title/,
);
responses.push(Response.json({ result: {
  id: recordingId, state: "completed", targetId: "page-one", title: "Demonstrate example navigation",
  startedAt: "2026-07-21T12:00:00.000Z", finishedAt: "2026-07-21T12:00:05.000Z",
  durationMs: 5_000, bytes: 1_024, mimeType: "video/webm;codecs=vp9", filename: `${recordingId}.webm`,
} }));
await tool("browser_recording").execute(
  "recording-stop", { action: "stop", recordingId }, undefined, undefined, {},
);
assert.deepEqual(JSON.parse(requests.at(-1).init.body), {
  operation: "recording.stop", params: { recordingId },
});

const controller = new AbortController();
responses.push(
  Response.json({
    result: {
      action: "goto",
      targetId: "page-one",
      title: "Example",
      url: "https://example.com/",
    },
  }),
);
const navigation = await tool("browser_navigate").execute(
  "navigate-one",
  { action: "goto", url: "https://example.com", timeoutMs: 1234 },
  controller.signal,
  undefined,
  {},
);
const navigationRequest = requests.at(-1);
assert.equal(navigationRequest.url, `${process.env.KIWI_CODE_BROWSER_THREAD_ENDPOINT}/browser/actions`);
assert.equal(navigationRequest.init.method, "POST");
assert.equal(navigationRequest.init.signal, controller.signal);
const requestHeaders = new Headers(navigationRequest.init.headers);
assert.equal(requestHeaders.get("x-kiwi-code-agent-token"), process.env.KIWI_CODE_AGENT_TOKEN);
assert.equal(requestHeaders.get("content-type"), "application/json");
assert.deepEqual(JSON.parse(navigationRequest.init.body), {
  operation: "navigate.goto",
  params: { url: "https://example.com", timeoutMs: 1234 },
});
assert.equal(
  navigation.content[0].text,
  "goto complete.\nPage: Example\nURL: https://example.com/\nTarget: page-one",
);
assert.equal(navigation.details.targetId, "page-one");

await assert.rejects(
  tool("browser_navigate").execute("navigate-missing-url", { action: "goto" }, undefined, undefined, {}),
  /url is required/,
);
responses.push(
  Response.json({
    result: {
      message: "Released the browser control connection; the session remains running.",
      status: {
        endpoint: "kiwi-code://electron",
        owned: true,
        pages: 1,
        reachable: true,
      },
    },
  }),
);
await tool("browser_session").execute(
  "session-disconnect",
  { action: "disconnect" },
  undefined,
  undefined,
  {},
);
assert.equal(JSON.parse(requests.at(-1).init.body).operation, "session.disconnect");

responses.push(
  Response.json({
    result: {
      method: "Runtime.enable",
      result: {},
      target: "page",
      targetId: "page-one",
    },
  }),
);
await tool("browser_cdp").execute(
  "cdp-defaults",
  { method: "Runtime.enable" },
  undefined,
  undefined,
  {},
);
assert.deepEqual(JSON.parse(requests.at(-1).init.body), {
  operation: "cdp",
  params: { method: "Runtime.enable", params: {}, target: "page" },
});

const imageData = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=";
responses.push(
  Response.json({
    result: {
      data: imageData,
      mimeType: "image/png",
      targetId: "page-one",
      title: "Example",
      url: "https://example.com/",
    },
  }),
);
const screenshot = await tool("browser_screenshot").execute(
  "screenshot-one",
  { format: "png", fullPage: true },
  undefined,
  undefined,
  {},
);
assert.deepEqual(JSON.parse(requests.at(-1).init.body), {
  operation: "screenshot",
  params: { format: "png", fullPage: true },
});
assert.equal(screenshot.content[1].type, "image");
assert.ok(["image/png", "image/jpeg"].includes(screenshot.content[1].mimeType));
assert.ok(Buffer.from(screenshot.content[1].data, "base64").byteLength > 0);
assert.equal(screenshot.details.bytes, Buffer.from(screenshot.content[1].data, "base64").byteLength);

const largeValue = "browser output ".repeat(5000);
responses.push(
  Response.json({
    result: {
      result: largeValue,
      targetId: "page-one",
      title: "Example",
      url: "https://example.com/",
    },
  }),
);
const evaluation = await tool("browser_evaluate").execute(
  "evaluate-one",
  { expression: "largeValue" },
  undefined,
  undefined,
  {},
);
assert.equal(evaluation.details.truncated, true);
assert.match(evaluation.content[0].text, /\[Output truncated:/);
assert.equal(
  await readFile(evaluation.details.fullOutputPath, "utf8"),
  JSON.stringify(largeValue, null, 2),
);
await rm(dirname(evaluation.details.fullOutputPath), { recursive: true, force: true });

responses.push(Response.json({ error: "Browser page not found." }, { status: 404 }));
await assert.rejects(
  tool("browser_tabs").execute("tabs-404", { action: "list" }, undefined, undefined, {}),
  /tabs\.list failed \(HTTP 404\): Browser page not found/,
);
responses.push(new Response("not found", { status: 404 }));
await assert.rejects(
  tool("browser_tabs").execute("endpoint-404", { action: "list" }, undefined, undefined, {}),
  /actions endpoint is unavailable \(HTTP 404\)/,
);
responses.push(Response.json({ error: "desktop disconnected" }, { status: 503 }));
await assert.rejects(
  tool("browser_tabs").execute("tabs-503", { action: "list" }, undefined, undefined, {}),
  /in-app browser provider is unavailable \(HTTP 503\)/,
);
responses.push(Response.json({ error: "provider exploded" }, { status: 500 }));
await assert.rejects(
  tool("browser_tabs").execute("tabs-500", { action: "list" }, undefined, undefined, {}),
  /tabs\.list failed \(HTTP 500\): provider exploded/,
);
responses.push(Response.json({ value: "wrong envelope" }));
await assert.rejects(
  tool("browser_tabs").execute("tabs-invalid", { action: "list" }, undefined, undefined, {}),
  /invalid response; expected \{result: \.\.\.\}/,
);
responses.push(new Response("not JSON", { status: 200 }));
await assert.rejects(
  tool("browser_tabs").execute("tabs-invalid-json", { action: "list" }, undefined, undefined, {}),
  /invalid response; expected \{result: \.\.\.\}/,
);
responses.push(Response.json({ result: null }));
await assert.rejects(
  tool("browser_tabs").execute("tabs-null", { action: "list" }, undefined, undefined, {}),
  /invalid response; expected \{result: \.\.\.\}/,
);
responses.push(Response.json({ result: { message: "missing pages" } }));
await assert.rejects(
  tool("browser_tabs").execute("tabs-invalid-result", { action: "list" }, undefined, undefined, {}),
  /invalid response; expected \{result: \.\.\.\}/,
);

const requestCountBeforeMissingToken = requests.length;
const token = process.env.KIWI_CODE_AGENT_TOKEN;
delete process.env.KIWI_CODE_AGENT_TOKEN;
await assert.rejects(
  tool("browser_tabs").execute("tabs-no-token", { action: "list" }, undefined, undefined, {}),
  /KIWI_CODE_AGENT_TOKEN is not set/,
);
process.env.KIWI_CODE_AGENT_TOKEN = token;
assert.equal(requests.length, requestCountBeforeMissingToken);
assert.equal(responses.length, 0);

const conflictHandlers = new Map();
const conflictTools = [];
const warnings = [];
const originalConsoleError = console.error;
console.error = (message) => warnings.push(String(message));
extensionModule.default({
  getActiveTools: () => ["browser_tool_search"],
  getAllTools: () => [{ name: "browser_tool_search", sourceInfo: { path: "/legacy/chrome-devtools.ts" } }],
  on(name, handler) { conflictHandlers.set(name, handler); },
  registerCommand() { throw new Error("conflicting browser command must not register"); },
  registerTool(toolDefinition) { conflictTools.push(toolDefinition); },
  setActiveTools() {},
});
conflictHandlers.get("session_start")(
  { reason: "startup" },
  { sessionManager: { getBranch: () => [] }, ui: { notify(message) { warnings.push(message); } } },
);
console.error = originalConsoleError;
assert.deepEqual(conflictTools, []);
assert.match(warnings.join("\n"), /first-party in-app browser tools are disabled/);
assert.deepEqual(conflictHandlers.get("resources_discover")(), { skillPaths: [] });

process.stdout.write("Pi browser extension harness passed\n");
