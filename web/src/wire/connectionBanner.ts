import type { StateConnectionSnapshot } from './client'

export type StateConnectionBanner = {
  readonly message: string
  readonly canRetryTopics: boolean
}

export function stateConnectionBanner(
  connection: StateConnectionSnapshot,
  topicError: string,
): StateConnectionBanner | null {
  if (connection.state === 'incompatible') {
    return {
      message: 'UI update required — reload Kiwi Code',
      canRetryTopics: false,
    }
  }
  if (topicError) {
    return {
      message: `UI state error: ${topicError}`,
      canRetryTopics: true,
    }
  }
  switch (connection.state) {
    case 'open':
      return null
    case 'error':
      return {
        message: 'UI state connection interrupted…',
        canRetryTopics: false,
      }
    case 'reconnecting':
      return {
        message: 'Reconnecting UI state…',
        canRetryTopics: false,
      }
    case 'connecting':
      return {
        message: 'Connecting UI state…',
        canRetryTopics: false,
      }
  }
}
