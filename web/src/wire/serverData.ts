import type { ThreadUsageSnapshot } from '@/types'
import { useLastReadySubscriptionData, useSubscription } from './react'
import { ThreadUsageTopic } from './topics'

// This topic is read in several places and written in none. Copying it
// into the store would duplicate socket data for nothing, so it stays a
// subscription -- just one behind a name instead of the same four lines of
// unwrapping repeated at each call site.
//
// Calling these from more than one component is free: the client keys channels
// by topic tag and params (wire/client.ts, observe()), so every caller shares
// one channel.

export function useThreadUsage(): ThreadUsageSnapshot[] {
  const subscription = useSubscription(ThreadUsageTopic, undefined)
  return (useLastReadySubscriptionData(subscription) as ThreadUsageSnapshot[] | null) ?? []
}
