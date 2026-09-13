import { describe, expect, it } from 'vitest'
import { newThreadStartFromState } from './newThreadStart'

const start = {
  kind: 'new-thread-start', projectId: 'project', threadId: 'thread',
  agent: 'codex', presentation: 'native', model: '', thinkingLevel: '',
  prompt: 'Build the feature', imagePaths: ['/tmp/kiwi-code-pi-clipboard-example.png'],
}
describe('native new-thread handoff', () => {
  it('preserves the Codex native presentation, prompt and images', () => {
    expect(newThreadStartFromState(start)).toEqual(start)
    expect(newThreadStartFromState({ ...start, imagePaths: undefined })).toMatchObject({
      agent: 'codex', presentation: 'native', prompt: 'Build the feature',
    })
  })
  it('still rejects images for terminal agents and unsupported native agents', () => {
    expect(newThreadStartFromState({ ...start, presentation: 'terminal' })).toBeNull()
    expect(newThreadStartFromState({ ...start, agent: 'grok', imagePaths: undefined })).toBeNull()
  })
})
