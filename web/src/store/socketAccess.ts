import type { StateSocketClient } from '@/wire/client'
import type { TopicDefinition } from '@/wire/topics'

// main.tsx records why the store cannot simply be handed the socket client:
//
//   "The store outranks the socket provider: it has no dependency on the socket,
//    while socket-driven middleware would need the store to already exist."
//
// That ordering stays true here. This module has no import-time dependency on a
// live client; StateSocketProvider registers one once it has built it, and code
// that runs later -- a retry button, a thunk rolling a failed mutation back --
// asks for it then. Everything through here must tolerate a null client, which
// is the honest state before the provider mounts and after it tears down.
let client: StateSocketClient | null = null

export function registerStateSocketClient(next: StateSocketClient | null) {
  client = next
}

export function stateSocketClient(): StateSocketClient | null {
  return client
}

/** Forces a fresh snapshot of one topic. A no-op when the socket is not up yet. */
export function retryTopic<Tag extends string, Params, Snapshot>(
  topic: TopicDefinition<Tag, Params, Snapshot>,
  params: Params,
) {
  client?.observe(topic, params).retry()
}
