import { useCallback, useRef, useState } from 'react'

export type NativeActivityRecord = {
  id: number
  at: number
  event: string
  summary: string
  repeats: number
}

export function useNativeActivityLog(limit = 24) {
  const [activityLog, setActivityLog] = useState<NativeActivityRecord[]>([])
  const sequenceRef = useRef(0)
  const appendActivity = useCallback((event: string, summary: string, at = Date.now()) => {
    setActivityLog((current) => {
      const latest = current[0]
      if (latest && latest.event === event && latest.summary === summary && at - latest.at < 1_500) {
        return [{ ...latest, at, repeats: latest.repeats + 1 }, ...current.slice(1)]
      }
      return [{
        id: sequenceRef.current += 1,
        at,
        event,
        summary,
        repeats: 1,
      }, ...current].slice(0, limit)
    })
  }, [limit])

  return { activityLog, appendActivity }
}
