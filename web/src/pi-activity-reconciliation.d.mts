import type { PiThreadActivity } from './types'

export type PiActivityAcknowledgement = {
  activity: PiThreadActivity
  index: number
}

export function piActivityKey(projectId: string, threadId: string): string
export function piActivityVersion(activity: PiThreadActivity): string
export function samePiActivities(current: PiThreadActivity[], next: PiThreadActivity[]): boolean
export function reconcilePiActivities(
  nextActivities: PiThreadActivity[],
  acknowledgements: Map<string, PiActivityAcknowledgement>,
): PiThreadActivity[]
export function reconcileFailedPiAcknowledgements(
  nextActivities: PiThreadActivity[],
  failedAcknowledgements: Map<string, string>,
): void
export function restoreAcknowledgedPiActivity(
  current: PiThreadActivity[],
  acknowledgement: PiActivityAcknowledgement,
): PiThreadActivity[]
