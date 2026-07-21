import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import threadUsageExtension from "./kiwi-code-thread-usage.ts";
import browserExtension from "./kiwi-code-browser.ts";
import skillForksExtension from "./kiwi-code-skill-forks.ts";

type ActivityState = "working" | "finished" | "idle";

const heartbeatIntervalMs = 5_000;
const requestTimeoutMs = 4_000;

export default function (pi: ExtensionAPI) {
	threadUsageExtension(pi);
	browserExtension(pi);
	skillForksExtension(pi);
	const threadEndpoint = process.env.KIWI_CODE_THREAD_ENDPOINT;
	if (!threadEndpoint) return;

	let working = false;
	let activePromptStartedAt: string | undefined;
	let heartbeat: ReturnType<typeof setInterval> | undefined;
	let requests = Promise.resolve();

	async function sendActivity(state: ActivityState, promptStartedAt?: string): Promise<void> {
		const controller = new AbortController();
		const timeout = setTimeout(() => controller.abort(), requestTimeoutMs);
		try {
			const response = await fetch(`${threadEndpoint}/pi/activity`, {
				method: "PUT",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({ state, ...(promptStartedAt ? { promptStartedAt } : {}) }),
				signal: controller.signal,
			});
			if (!response.ok) {
				throw new Error(`Kiwi Code returned ${response.status}`);
			}
		} finally {
			clearTimeout(timeout);
		}
	}

	function queueActivity(state: ActivityState, promptStartedAt?: string): Promise<void> {
		const scheduled = requests.then(() => sendActivity(state, promptStartedAt));
		// Event callbacks intentionally do not await these updates. Store and
		// return a handled promise immediately so a network failure cannot become
		// an unhandled rejection, while the next update still waits for this one.
		requests = scheduled.catch(() => {});
		return requests;
	}

	function stopHeartbeat(): void {
		if (heartbeat) clearInterval(heartbeat);
		heartbeat = undefined;
	}

	pi.on("agent_start", () => {
		working = true;
		activePromptStartedAt = new Date().toISOString();
		stopHeartbeat();
		void queueActivity("working", activePromptStartedAt);
		heartbeat = setInterval(() => void queueActivity("working", activePromptStartedAt), heartbeatIntervalMs);
	});

	pi.on("agent_settled", () => {
		if (!working) return;
		working = false;
		activePromptStartedAt = undefined;
		stopHeartbeat();
		void queueActivity("finished");
	});

	pi.on("session_shutdown", async () => {
		stopHeartbeat();
		activePromptStartedAt = undefined;
		if (working) {
			working = false;
			await queueActivity("idle").catch(() => {});
			return;
		}
		await requests.catch(() => {});
	});
}
