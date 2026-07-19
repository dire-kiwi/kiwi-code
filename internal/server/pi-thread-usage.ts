import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

type Totals = {
	inputTokens: number;
	outputTokens: number;
	cacheReadTokens: number;
	cacheWriteTokens: number;
	totalTokens: number;
	costUsd: number;
};

const requestTimeoutMs = 4_000;

function usageNumber(value: unknown): number {
	return typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : 0;
}

function sessionTotals(ctx: ExtensionContext): Totals {
	const totals: Totals = { inputTokens: 0, outputTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0, totalTokens: 0, costUsd: 0 };
	const messages: unknown[] = [];
	for (const entry of ctx.sessionManager.getEntries()) {
		if (entry.type === "message") messages.push(entry.message);
	}
	for (const candidate of messages) {
		if (!candidate || typeof candidate !== "object") continue;
		const message = candidate as { role?: string; usage?: { input?: number; output?: number; cacheRead?: number; cacheWrite?: number; cost?: { total?: number } } };
		if (message.role !== "assistant" || !message.usage) continue;
		totals.inputTokens += usageNumber(message.usage.input);
		totals.outputTokens += usageNumber(message.usage.output);
		totals.cacheReadTokens += usageNumber(message.usage.cacheRead);
		totals.cacheWriteTokens += usageNumber(message.usage.cacheWrite);
		totals.costUsd += usageNumber(message.usage.cost?.total);
	}
	totals.totalTokens = totals.inputTokens + totals.outputTokens + totals.cacheReadTokens + totals.cacheWriteTokens;
	return totals;
}

export default function (pi: ExtensionAPI) {
	const threadEndpoint = process.env.DIRE_MUX_THREAD_ENDPOINT;
	const agentToken = process.env.DIRE_MUX_AGENT_TOKEN;
	if (!threadEndpoint || !agentToken) return;
	let requests = Promise.resolve();

	async function request(path: string, init?: RequestInit): Promise<Response> {
		const controller = new AbortController();
		const timeout = setTimeout(() => controller.abort(), requestTimeoutMs);
		try {
			return await fetch(`${threadEndpoint}${path}`, {
				...init,
				headers: { "Content-Type": "application/json", "X-Dire-Mux-Agent-Token": agentToken, ...init?.headers },
				signal: controller.signal,
			});
		} finally {
			clearTimeout(timeout);
		}
	}

	function report(ctx: ExtensionContext): Promise<void> {
		const sessionId = ctx.sessionManager.getSessionId();
		if (!sessionId) return Promise.resolve();
		const body = JSON.stringify({ sessionId, ...sessionTotals(ctx) });
		const scheduled = requests.then(async () => {
			const response = await request("/usage", { method: "PUT", body });
			if (!response.ok) throw new Error(`dire/mux returned ${response.status}`);
		});
		requests = scheduled.catch(() => {});
		return requests;
	}

	pi.on("session_start", (_event, ctx) => {
		void report(ctx);
	});

	pi.on("agent_settled", (_event, ctx) => {
		void report(ctx);
	});

	pi.on("input", async (_event, ctx) => {
		try {
			const response = await request("/budget");
			if (!response.ok) return { action: "continue" as const };
			const budget = await response.json() as { limitReached?: boolean };
			if (!budget.limitReached) return { action: "continue" as const };
			ctx.ui.notify("Thread token or cost limit reached. Increase or remove the limit in Thread details to continue.", "error");
			return { action: "handled" as const };
		} catch {
			// A transient status failure must not strand a local interactive session.
			return { action: "continue" as const };
		}
	});

	pi.on("session_shutdown", async () => {
		await requests.catch(() => {});
	});
}
