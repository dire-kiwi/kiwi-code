#!/usr/bin/env node

import { readFile } from "node:fs/promises";
import vm from "node:vm";

const MANIFEST_VERSION = 1;
const MAX_AGENTS = 1_000;
const MAX_BATCH_ITEMS = 4_096;
const MAX_CONTROL_EVENTS = 2_000;
const MAX_RESULT_BYTES = 512 * 1024;
const POLL_INTERVAL_MS = 750;
const MAX_EVENT_RETRY_MS = 5 * 60 * 1_000;
const META_PREFIX = /^\s*export\s+const\s+meta\s*=\s*/;

function errorMessage(error) {
  return error instanceof Error ? error.message : String(error);
}

function sleep(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}

function isRecord(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function safeJSON(value, label, maximumBytes = MAX_RESULT_BYTES) {
  let encoded;
  try {
    encoded = JSON.stringify(value);
  } catch (error) {
    throw new Error(`${label} is not JSON-serializable: ${errorMessage(error)}`);
  }
  if (encoded === undefined) encoded = "null";
  if (Buffer.byteLength(encoded, "utf8") > maximumBytes) {
    throw new Error(`${label} exceeds ${maximumBytes} bytes.`);
  }
  return encoded;
}

function extractMetadata(source) {
  const match = META_PREFIX.exec(source);
  if (!match) {
    throw new Error("Workflow scripts must begin with export const meta = { name, description }.");
  }
  let index = match[0].length;
  while (/\s/.test(source[index] ?? "")) index += 1;
  if (source[index] !== "{") {
    throw new Error("Workflow meta must be an object literal.");
  }

  const start = index;
  let depth = 0;
  let quote = "";
  let escaped = false;
  let lineComment = false;
  let blockComment = false;
  for (; index < source.length; index += 1) {
    const character = source[index];
    const next = source[index + 1];
    if (lineComment) {
      if (character === "\n") lineComment = false;
      continue;
    }
    if (blockComment) {
      if (character === "*" && next === "/") {
        blockComment = false;
        index += 1;
      }
      continue;
    }
    if (quote) {
      if (escaped) {
        escaped = false;
        continue;
      }
      if (character === "\\") {
        escaped = true;
        continue;
      }
      if (quote === "`" && character === "$" && next === "{") {
        throw new Error("Workflow meta must be a pure literal without template interpolation.");
      }
      if (character === quote) quote = "";
      continue;
    }
    if (character === "/" && next === "/") {
      lineComment = true;
      index += 1;
      continue;
    }
    if (character === "/" && next === "*") {
      blockComment = true;
      index += 1;
      continue;
    }
    if (character === "'" || character === '"' || character === "`") {
      quote = character;
      continue;
    }
    if (character === "{") depth += 1;
    if (character === "}") {
      depth -= 1;
      if (depth === 0) break;
    }
  }
  if (depth !== 0 || index >= source.length) throw new Error("Workflow meta object is not closed.");

  const literal = source.slice(start, index + 1);
  const context = vm.createContext({}, { codeGeneration: { strings: false, wasm: false } });
  new vm.Script("globalThis.constructor = undefined").runInContext(context, { timeout: 100 });
  let metadata;
  try {
    const raw = new vm.Script(`JSON.stringify(${literal})`).runInContext(context, { timeout: 250 });
    metadata = JSON.parse(raw);
  } catch (error) {
    throw new Error(`Workflow meta is invalid: ${errorMessage(error)}`);
  }
  if (!isRecord(metadata) || typeof metadata.name !== "string" || !metadata.name.trim() ||
      typeof metadata.description !== "string" || !metadata.description.trim()) {
    throw new Error("Workflow meta requires non-empty string name and description fields.");
  }
  if (metadata.phases !== undefined && (!Array.isArray(metadata.phases) || metadata.phases.some((entry) =>
    !isRecord(entry) || typeof entry.title !== "string" || !entry.title.trim()))) {
    throw new Error("Workflow meta phases must be objects with non-empty title fields.");
  }
  metadata.name = metadata.name.trim().slice(0, 120);
  metadata.description = metadata.description.trim().slice(0, 2_000);
  return { metadata, declarationEnd: index + 1, prefixMatch: match };
}

function jsonEqual(left, right) {
  if (left === right) return true;
  if (Array.isArray(left) || Array.isArray(right)) {
    return Array.isArray(left) && Array.isArray(right) && left.length === right.length &&
      left.every((entry, index) => jsonEqual(entry, right[index]));
  }
  if (!isRecord(left) || !isRecord(right)) return false;
  const leftKeys = Object.keys(left).sort();
  const rightKeys = Object.keys(right).sort();
  return leftKeys.length === rightKeys.length && leftKeys.every((key, index) =>
    key === rightKeys[index] && jsonEqual(left[key], right[key]));
}

function schemaMatches(value, schema, path) {
  try {
    validateSchema(value, schema, path);
    return true;
  } catch {
    return false;
  }
}

function validateSchema(value, schema, path = "result") {
  if (schema === true || schema === undefined) return;
  if (schema === false) throw new Error(`${path} is not allowed by the schema.`);
  if (!isRecord(schema)) throw new Error(`${path} has an invalid schema.`);
  if (Object.prototype.hasOwnProperty.call(schema, "const") && !jsonEqual(schema.const, value)) {
    throw new Error(`${path} does not equal the schema's constant value.`);
  }
  if (Array.isArray(schema.enum) && !schema.enum.some((candidate) => jsonEqual(candidate, value))) {
    throw new Error(`${path} is not one of the schema's allowed values.`);
  }
  if (Array.isArray(schema.allOf)) {
    for (const child of schema.allOf) validateSchema(value, child, path);
  }
  if (Array.isArray(schema.anyOf) && !schema.anyOf.some((child) => schemaMatches(value, child, path))) {
    throw new Error(`${path} does not match any allowed schema.`);
  }
  if (Array.isArray(schema.oneOf) && schema.oneOf.filter((child) => schemaMatches(value, child, path)).length !== 1) {
    throw new Error(`${path} must match exactly one allowed schema.`);
  }
  if (schema.not !== undefined && schemaMatches(value, schema.not, path)) {
    throw new Error(`${path} matches a forbidden schema.`);
  }
  if (Array.isArray(schema.type)) {
    if (!schema.type.some((type) => schemaMatches(value, { ...schema, type }, path))) {
      throw new Error(`${path} does not have an allowed type.`);
    }
    return;
  }
  switch (schema.type) {
    case "object": {
      if (!isRecord(value)) throw new Error(`${path} must be an object.`);
      const keys = Object.keys(value);
      if (Number.isInteger(schema.minProperties) && keys.length < schema.minProperties) throw new Error(`${path} has too few properties.`);
      if (Number.isInteger(schema.maxProperties) && keys.length > schema.maxProperties) throw new Error(`${path} has too many properties.`);
      for (const required of Array.isArray(schema.required) ? schema.required : []) {
        if (!Object.prototype.hasOwnProperty.call(value, required)) {
          throw new Error(`${path}.${required} is required.`);
        }
      }
      const properties = isRecord(schema.properties) ? schema.properties : {};
      for (const [name, childSchema] of Object.entries(properties)) {
        if (Object.prototype.hasOwnProperty.call(value, name)) validateSchema(value[name], childSchema, `${path}.${name}`);
      }
      for (const name of keys) {
        if (Object.prototype.hasOwnProperty.call(properties, name)) continue;
        if (schema.additionalProperties === false) throw new Error(`${path}.${name} is not allowed by the schema.`);
        if (isRecord(schema.additionalProperties) || typeof schema.additionalProperties === "boolean") {
          validateSchema(value[name], schema.additionalProperties, `${path}.${name}`);
        }
      }
      break;
    }
    case "array":
      if (!Array.isArray(value)) throw new Error(`${path} must be an array.`);
      if (Number.isInteger(schema.minItems) && value.length < schema.minItems) throw new Error(`${path} has too few items.`);
      if (Number.isInteger(schema.maxItems) && value.length > schema.maxItems) throw new Error(`${path} has too many items.`);
      for (let index = 0; index < value.length; index += 1) {
        validateSchema(value[index], schema.items, `${path}[${index}]`);
      }
      break;
    case "string":
      if (typeof value !== "string") throw new Error(`${path} must be a string.`);
      if (Number.isInteger(schema.minLength) && [...value].length < schema.minLength) throw new Error(`${path} is too short.`);
      if (Number.isInteger(schema.maxLength) && [...value].length > schema.maxLength) throw new Error(`${path} is too long.`);
      if (typeof schema.pattern === "string") {
        let matches = false;
        try { matches = new RegExp(schema.pattern, "u").test(value); } catch { throw new Error(`${path} has an invalid pattern schema.`); }
        if (!matches) throw new Error(`${path} does not match the required pattern.`);
      }
      break;
    case "number":
    case "integer":
      if (typeof value !== "number" || !Number.isFinite(value) || (schema.type === "integer" && !Number.isInteger(value))) {
        throw new Error(`${path} must be ${schema.type === "integer" ? "an integer" : "a number"}.`);
      }
      if (typeof schema.minimum === "number" && value < schema.minimum) throw new Error(`${path} is below its minimum.`);
      if (typeof schema.maximum === "number" && value > schema.maximum) throw new Error(`${path} is above its maximum.`);
      if (typeof schema.exclusiveMinimum === "number" && value <= schema.exclusiveMinimum) throw new Error(`${path} is below its exclusive minimum.`);
      if (typeof schema.exclusiveMaximum === "number" && value >= schema.exclusiveMaximum) throw new Error(`${path} is above its exclusive maximum.`);
      break;
    case "boolean":
      if (typeof value !== "boolean") throw new Error(`${path} must be a boolean.`);
      break;
    case "null":
      if (value !== null) throw new Error(`${path} must be null.`);
      break;
    default:
      break;
  }
}

function parseStructuredOutput(output, schema) {
  const trimmed = String(output ?? "").trim();
  const candidates = [trimmed];
  const fence = /```(?:json)?\s*([\s\S]*?)```/gi;
  for (const match of trimmed.matchAll(fence)) candidates.push(match[1].trim());
  const firstObject = trimmed.indexOf("{");
  const lastObject = trimmed.lastIndexOf("}");
  if (firstObject >= 0 && lastObject > firstObject) candidates.push(trimmed.slice(firstObject, lastObject + 1));
  const firstArray = trimmed.indexOf("[");
  const lastArray = trimmed.lastIndexOf("]");
  if (firstArray >= 0 && lastArray > firstArray) candidates.push(trimmed.slice(firstArray, lastArray + 1));

  for (const candidate of candidates) {
    try {
      const value = JSON.parse(candidate);
      validateSchema(value, schema);
      return value;
    } catch {
      // Try the next bounded representation.
    }
  }
  throw new Error("Agent did not return valid JSON matching the requested schema.");
}

function createLimiter(limit) {
  let active = 0;
  const queued = [];
  const acquire = () => new Promise((resolve) => {
    if (active < limit) {
      active += 1;
      resolve();
    } else {
      queued.push(resolve);
    }
  });
  const release = () => {
    const next = queued.shift();
    if (next) next();
    else active -= 1;
  };
  return async (operation) => {
    await acquire();
    try {
      return await operation();
    } finally {
      release();
    }
  };
}

async function main() {
  const manifestPath = process.argv[2];
  if (!manifestPath) throw new Error("Workflow runner manifest path is required.");
  const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
  if (!isRecord(manifest) || manifest.version !== MANIFEST_VERSION ||
      typeof manifest.runId !== "string" || typeof manifest.endpoint !== "string" ||
      typeof manifest.token !== "string" || typeof manifest.scriptPath !== "string") {
    throw new Error("Workflow runner manifest is invalid.");
  }

  const endpoint = manifest.endpoint.replace(/\/+$/, "");
  const runPath = `/workflows/${encodeURIComponent(manifest.runId)}`;
  const headers = {
    "Content-Type": "application/json",
    "X-Kiwi-Code-Workflow-Token": manifest.token,
  };

  async function api(path, init = {}, retryMilliseconds = 0) {
    const started = Date.now();
    let delay = 150;
    for (;;) {
      try {
        const response = await fetch(`${endpoint}${path}`, {
          ...init,
          headers: { ...headers, ...(init.headers ?? {}) },
        });
        const text = await response.text();
        let payload;
        if (text) {
          try { payload = JSON.parse(text); } catch { payload = undefined; }
        }
        if (!response.ok) {
          const message = isRecord(payload) && typeof payload.error === "string"
            ? payload.error
            : `Kiwi Code returned HTTP ${response.status}.`;
          const error = new Error(message);
          error.status = response.status;
          throw error;
        }
        return payload;
      } catch (error) {
        const retryable = !Number.isInteger(error?.status) || error.status >= 500;
        if (!retryable || retryMilliseconds <= 0 || Date.now() - started >= retryMilliseconds) throw error;
        await sleep(delay);
        delay = Math.min(2_000, Math.round(delay * 1.6));
      }
    }
  }

  let eventSequence = 0;
  const attempt = Math.max(1, Number(manifest.attempt) || 1);
  async function event(type, fields = {}) {
    eventSequence += 1;
    const payload = { eventId: `attempt-${attempt}-event-${String(eventSequence).padStart(6, "0")}`, type, ...fields };
    const response = await api(`${runPath}/events`, {
      method: "POST",
      body: safeJSON(payload, "Workflow event", 512 * 1024),
    }, MAX_EVENT_RETRY_MS);
    if (type === "log" && typeof fields.message === "string") process.stdout.write(`${fields.message}\n`);
    return response;
  }

  const source = await readFile(manifest.scriptPath, "utf8");
  const { metadata } = extractMetadata(source);
  await event("started", { meta: metadata });

  const concurrency = Math.max(1, Math.min(16, Number(manifest.maxConcurrency) || 16));
  const limited = createLimiter(concurrency);
  let agentSequence = 0;
  let currentPhase = "";

  async function runAgent(serialized) {
    const values = JSON.parse(serialized);
    const prompt = values[0];
    const options = values[1] ?? {};
    if (typeof prompt !== "string" || !prompt.trim()) throw new Error("agent() requires a non-empty prompt string.");
    if (!isRecord(options)) throw new Error("agent() options must be an object.");
    agentSequence += 1;
    if (agentSequence > MAX_AGENTS) throw new Error(`Workflow agent() call cap reached (${MAX_AGENTS}).`);

    const agentId = `agent-${String(agentSequence).padStart(4, "0")}`;
    const rawLabel = typeof options.label === "string" && options.label.trim()
      ? options.label.trim()
      : `Workflow agent ${agentSequence}`;
    const label = [...rawLabel].slice(0, 120).join("");
    const phaseName = typeof options.phase === "string" && options.phase.trim()
      ? options.phase.trim().slice(0, 120)
      : currentPhase;
    const registration = await event("agent_started", { agentId, label, phase: phaseName });
    if (registration?.cached === true) return registration.value;

    return limited(async () => {
      let childPrompt = prompt;
      const schema = isRecord(options.schema) ? options.schema : undefined;
      if (schema) {
        childPrompt += `\n\n[Kiwi Code workflow return contract]\nYour final response is a machine-consumed value. Return only JSON, without a Markdown fence or commentary, matching this JSON Schema:\n${safeJSON(schema, "Agent schema", 128 * 1024)}`;
      }
      const request = {
        title: label,
        prompt: childPrompt,
        agent: "pi",
      };
      const model = typeof options.model === "string" && options.model.trim()
        ? options.model.trim()
        : typeof manifest.defaultModel === "string" ? manifest.defaultModel.trim() : "";
      const thinkingLevel = typeof options.effort === "string" && options.effort.trim()
        ? options.effort.trim()
        : typeof options.thinkingLevel === "string" && options.thinkingLevel.trim()
          ? options.thinkingLevel.trim()
          : typeof manifest.defaultThinkingLevel === "string" ? manifest.defaultThinkingLevel.trim() : "";
      if (model) request.model = model;
      if (thinkingLevel) request.thinkingLevel = thinkingLevel;
      if (typeof options.worktree === "boolean") request.worktree = options.worktree;
      else if (options.isolation === "worktree") request.worktree = true;
      else if (options.isolation === "shared") request.worktree = false;
      if (typeof options.baseBranch === "string" && options.baseBranch.trim()) request.baseBranch = options.baseBranch.trim();
      if (Number.isInteger(options.nestedDepth)) request.nestedDepth = options.nestedDepth;

      let created;
      try {
        created = await api(`${runPath}/agents/${encodeURIComponent(agentId)}`, {
          method: "POST",
          body: safeJSON(request, "Agent request", 512 * 1024),
        }, MAX_EVENT_RETRY_MS);
      } catch (error) {
        await event("agent_failed", { agentId, error: errorMessage(error) });
        return null;
      }
      await event("agent_working", {
        agentId,
        threadId: created?.thread?.id,
        childRunId: created?.run?.id,
      });

      let run = created?.run;
      let value;
      let failure = "";
      try {
        while (run?.state === "starting" || run?.state === "working") {
          await sleep(POLL_INTERVAL_MS);
          run = await api(`${runPath}/agents/${encodeURIComponent(agentId)}`, {}, MAX_EVENT_RETRY_MS);
        }
        if (!run || run.state !== "finished") {
          failure = run?.error || `Agent ended in state ${run?.state ?? "unknown"}.`;
        } else {
          value = run.output ?? "";
          if (schema) value = parseStructuredOutput(value, schema);
        }
      } catch (error) {
        failure = errorMessage(error);
      }

      const closeOnComplete = typeof options.closeOnComplete === "boolean"
        ? options.closeOnComplete
        : manifest.closeOnComplete !== false;
      let closeError = "";
      if (closeOnComplete) {
        try {
          await api(`${runPath}/agents/${encodeURIComponent(agentId)}/close`, {
            method: "POST",
            body: "{}",
          }, 30_000);
        } catch (error) {
          closeError = errorMessage(error);
        }
      }
      const output = String(run?.output ?? "").slice(0, 200 * 1024);
      if (failure) {
        await event("agent_failed", {
          agentId,
          error: closeError ? `${failure} The child thread also could not be closed: ${closeError}` : failure,
          output,
        });
        return null;
      }
      await event("agent_finished", {
        agentId,
        output,
        value,
        ...(closeError ? { error: `Agent completed, but its thread could not be closed: ${closeError}` } : {}),
      });
      return value;
    });
  }

  const backgroundEvents = [];
  const backgroundAgents = [];
  let logEvents = 0;
  let phaseEvents = 0;
  const bridgeAgent = (serialized) => {
    const operation = runAgent(serialized).then((value) => safeJSON(value, "Agent result"));
    backgroundAgents.push(operation);
    return operation;
  };
  const bridgeLog = (message) => {
    logEvents += 1;
    if (logEvents > MAX_CONTROL_EVENTS) return;
    const text = String(message).slice(0, 4_000);
    const pending = event("log", { message: text }).catch((error) => process.stderr.write(`workflow log failed: ${errorMessage(error)}\n`));
    backgroundEvents.push(pending);
  };
  const bridgePhase = (title) => {
    phaseEvents += 1;
    if (phaseEvents > MAX_CONTROL_EVENTS) return;
    currentPhase = String(title).trim().slice(0, 120);
    const pending = event("phase", { phase: currentPhase }).catch((error) => process.stderr.write(`workflow phase failed: ${errorMessage(error)}\n`));
    backgroundEvents.push(pending);
  };
  for (const bridge of [bridgeAgent, bridgeLog, bridgePhase]) {
    Object.setPrototypeOf(bridge, null);
    Object.freeze(bridge);
  }

  const sandbox = vm.createContext({
    __kiwiCodeAgent: bridgeAgent,
    __kiwiCodeLog: bridgeLog,
    __kiwiCodePhase: bridgePhase,
    __kiwiCodeArgsJSON: manifest.hasArgs ? safeJSON(manifest.args, "Workflow args", 1024 * 1024) : "",
  }, {
    name: `kiwi-code-workflow-${manifest.runId}`,
    codeGeneration: { strings: false, wasm: false },
  });

  const bootstrap = `
const __agentBridge = globalThis.__kiwiCodeAgent;
const __logBridge = globalThis.__kiwiCodeLog;
const __phaseBridge = globalThis.__kiwiCodePhase;
const __argsJSON = globalThis.__kiwiCodeArgsJSON;
delete globalThis.__kiwiCodeAgent;
delete globalThis.__kiwiCodeLog;
delete globalThis.__kiwiCodePhase;
delete globalThis.__kiwiCodeArgsJSON;
globalThis.constructor = undefined;
const __bridgePromise = (operation) => new Promise((resolve, reject) => {
  try {
    operation.then(resolve, reason => reject(new Error(String(reason && reason.message ? reason.message : reason))));
  } catch (error) {
    reject(new Error(String(error && error.message ? error.message : error)));
  }
});
const agent = (prompt, options = {}) => __bridgePromise(
  __agentBridge(JSON.stringify([prompt, options]))
).then(serialized => JSON.parse(serialized));
const parallel = async thunks => {
  if (!Array.isArray(thunks)) throw new TypeError('parallel() expects an array of functions');
  if (thunks.length > ${MAX_BATCH_ITEMS}) throw new RangeError('parallel() accepts at most ${MAX_BATCH_ITEMS} items');
  for (const thunk of thunks) if (typeof thunk !== 'function') {
    throw new TypeError('parallel() expects functions, not promises. Wrap each call: () => agent(...)');
  }
  const settled = await Promise.allSettled(thunks.map(thunk => Promise.resolve().then(thunk)));
  return settled.map(entry => entry.status === 'fulfilled' ? entry.value : null);
};
const pipeline = async (items, ...stages) => {
  if (!Array.isArray(items)) throw new TypeError('pipeline() expects an array as the first argument');
  if (items.length > ${MAX_BATCH_ITEMS}) throw new RangeError('pipeline() accepts at most ${MAX_BATCH_ITEMS} items');
  for (const stage of stages) if (typeof stage !== 'function') throw new TypeError('pipeline() stages must be functions');
  const settled = await Promise.allSettled(items.map(async (original, index) => {
    let value = original;
    for (const stage of stages) {
      if (value === null) break;
      value = await stage(value, original, index);
    }
    return value;
  }));
  return settled.map(entry => entry.status === 'fulfilled' ? entry.value : null);
};
const log = message => __logBridge(String(message));
const phase = title => __phaseBridge(String(title));
const args = __argsJSON ? JSON.parse(__argsJSON) : undefined;
Object.defineProperty(Math, 'random', { value: () => { throw new Error('Math.random() is unavailable in workflow scripts'); } });
Object.defineProperty(Date, 'now', { value: () => { throw new Error('Date.now() is unavailable in workflow scripts'); } });
Object.freeze(agent); Object.freeze(parallel); Object.freeze(pipeline); Object.freeze(log); Object.freeze(phase);
`;
  new vm.Script(bootstrap, { filename: "kiwi-code-workflow-bootstrap.js" }).runInContext(sandbox, { timeout: 1_000 });

  const transformed = source.replace(META_PREFIX, "const meta = ");
  const execution = new vm.Script(`(async () => {\n${transformed}\n})()`, {
    filename: manifest.scriptPath,
  }).runInContext(sandbox, { timeout: 1_000 });
  let result;
  let executionError;
  try {
    result = await execution;
  } catch (error) {
    executionError = error;
  }
  await Promise.allSettled(backgroundAgents);
  await Promise.allSettled(backgroundEvents);
  if (executionError) throw executionError;
  const normalizedResult = result === undefined ? null : result;
  safeJSON(normalizedResult, "Workflow result");
  await event("finished", { result: normalizedResult });
  process.stdout.write(`Workflow ${metadata.name} finished.\n`);
}

main().catch(async (error) => {
  const message = errorMessage(error);
  process.stderr.write(`Workflow failed: ${message}\n`);
  try {
    const manifestPath = process.argv[2];
    if (manifestPath) {
      const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
      const endpoint = String(manifest.endpoint || "").replace(/\/+$/, "");
      if (endpoint && manifest.runId && manifest.token) {
        await fetch(`${endpoint}/workflows/${encodeURIComponent(manifest.runId)}/events`, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            "X-Kiwi-Code-Workflow-Token": manifest.token,
          },
          body: JSON.stringify({
            eventId: `attempt-${Math.max(1, Number(manifest.attempt) || 1)}-terminal-failure`,
            type: "failed",
            error: message.slice(0, 16 * 1024),
          }),
        });
      }
    }
  } catch {
    // The process log retains the original error when the backend is unavailable.
  }
  process.exitCode = 1;
});
