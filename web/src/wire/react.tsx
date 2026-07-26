import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useSyncExternalStore,
  type ReactNode,
} from 'react'
import {
  StateSocketClient,
  type StateConnectionSnapshot,
  type SubscriptionState,
} from './client'
import type { TopicDefinition } from './topics'

const StateSocketContext = createContext<StateSocketClient | null>(null)
const disabledState = { state: 'loading' } as const
const subscribeDisabled = () => () => {}
const getDisabledState = () => disabledState

export function StateSocketProvider({
  children,
  client: suppliedClient,
}: {
  children: ReactNode
  client?: StateSocketClient
}) {
  const client = useMemo(() => suppliedClient ?? new StateSocketClient(), [suppliedClient])
  useEffect(() => {
    client.start()
    return () => {
      if (!suppliedClient) client.stop()
    }
  }, [client, suppliedClient])
  return <StateSocketContext.Provider value={client}>{children}</StateSocketContext.Provider>
}

export function useStateSocketClient() {
  const client = useContext(StateSocketContext)
  if (!client) throw new Error('State socket hooks must be used within StateSocketProvider.')
  return client
}

export type UseSubscriptionOptions = {
  readonly enabled?: boolean
}

export type SubscriptionResult<Snapshot> = SubscriptionState<Snapshot> & {
  readonly retry: () => void
}

export function useSubscription<Tag extends string, Params, Snapshot>(
  topic: TopicDefinition<Tag, Params, Snapshot>,
  params: Params,
  options: UseSubscriptionOptions = {},
): SubscriptionResult<Snapshot> {
  const client = useStateSocketClient()
  const enabled = options.enabled !== false
  const semanticKey = topic.key(params)
  const observer = useMemo(
    () => client.observe(topic, params),
    // The semantic key is the topic's identity contract; params object identity is irrelevant.
    [client, semanticKey, topic],
  )
  const state = useSyncExternalStore(
    enabled ? observer.subscribe : subscribeDisabled,
    enabled ? observer.getSnapshot : getDisabledState,
    enabled ? observer.getSnapshot : getDisabledState,
  )
  return useMemo(() => ({ ...state, retry: observer.retry }), [observer.retry, state])
}

export function useConnectionStatus(): StateConnectionSnapshot {
  const client = useStateSocketClient()
  return useSyncExternalStore(
    client.subscribeConnection,
    client.getConnectionSnapshot,
    client.getConnectionSnapshot,
  )
}

export function useApplicationInstance(
  onChange: (current: string, previous?: string) => void,
) {
  const client = useStateSocketClient()
  useEffect(() => client.subscribeInstance(onChange), [client, onChange])
}
