import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

type ContextStatusUpdate = {
	source: "pi-terminal";
	tokens: number | null;
	contextWindow: number;
	percent: number | null;
	model: string;
};

const requestTimeoutMs = 4_000;
const reportIntervalMs = 30_000;

function contextStatus(ctx: ExtensionContext): ContextStatusUpdate | undefined {
	// Pi Native receives the same canonical contextUsage through its RPC
	// get_session_stats response. Only the terminal presentation needs this
	// extension-to-server bridge.
	if (ctx.mode !== "tui") return undefined;

	const usage = typeof ctx.getContextUsage === "function" ? ctx.getContextUsage() : undefined;
	const contextWindow = usage?.contextWindow ?? ctx.model?.contextWindow;
	if (typeof contextWindow !== "number" || !Number.isFinite(contextWindow) || contextWindow <= 0) {
		return undefined;
	}
	const tokens = usage?.tokens ?? null;
	const percent = usage?.percent ?? null;
	const hasKnownUsage = typeof tokens === "number" && Number.isFinite(tokens)
		&& typeof percent === "number" && Number.isFinite(percent);
	return {
		source: "pi-terminal",
		tokens: hasKnownUsage ? tokens : null,
		contextWindow,
		percent: hasKnownUsage ? percent : null,
		model: ctx.model ? `${ctx.model.provider}/${ctx.model.id}` : "",
	};
}

export default function (pi: ExtensionAPI) {
	const threadEndpoint = process.env.KIWI_CODE_THREAD_ENDPOINT;
	if (!threadEndpoint) return;

	let requests = Promise.resolve();
	let reportInterval: ReturnType<typeof setInterval> | undefined;

	async function sendContext(status: ContextStatusUpdate): Promise<void> {
		const controller = new AbortController();
		const timeout = setTimeout(() => controller.abort(), requestTimeoutMs);
		try {
			const response = await fetch(`${threadEndpoint}/context/status`, {
				method: "PUT",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify(status),
				signal: controller.signal,
			});
			if (!response.ok) throw new Error(`Kiwi Code returned ${response.status}`);
		} finally {
			clearTimeout(timeout);
		}
	}

	function queueContext(ctx: ExtensionContext): Promise<void> {
		const status = contextStatus(ctx);
		if (!status) return requests;
		const scheduled = requests.then(() => sendContext(status));
		// Context reporting must never interrupt a Pi session. Serialize updates
		// and retain a handled promise so failures can be retried by the next event.
		requests = scheduled.catch(() => {});
		return requests;
	}

	pi.on("session_start", (_event, ctx) => {
		if (reportInterval) clearInterval(reportInterval);
		reportInterval = undefined;
		if (ctx.mode !== "tui") return;
		void queueContext(ctx);
		reportInterval = setInterval(() => void queueContext(ctx), reportIntervalMs);
	});
	pi.on("model_select", (_event, ctx) => {
		void queueContext(ctx);
	});
	pi.on("turn_end", (_event, ctx) => {
		void queueContext(ctx);
	});
	pi.on("session_compact", (_event, ctx) => {
		void queueContext(ctx);
	});
	pi.on("session_tree", (_event, ctx) => {
		void queueContext(ctx);
	});
	pi.on("agent_settled", (_event, ctx) => {
		void queueContext(ctx);
	});
	pi.on("session_shutdown", async () => {
		if (reportInterval) clearInterval(reportInterval);
		reportInterval = undefined;
		await requests.catch(() => {});
	});
}
