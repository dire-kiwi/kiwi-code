import { Schema } from 'effect'
import { describe, expect, it } from 'vitest'
import fixture from '@/wire/__fixtures__/protocol.json'
import { decodeStrict } from './client'
import { protocolVersion, ServerMessageSchema } from './protocol'
import {
  allTopics,
  SandboxConfigParamsSchema,
} from './topics'

const expectedServerMessageTypes = [
  'event',
  'pong',
  'ready',
  'snap',
  'subend',
  'suberr',
]

describe('Go/TypeScript protocol fixtures', () => {
  it('uses the same protocol version', () => {
    expect(fixture.protocolVersion).toBe(protocolVersion)
  })

  it('strictly decodes every server envelope and covers the full v1 union', () => {
    for (const message of fixture.serverMessages) {
      expect(() => decodeStrict(ServerMessageSchema, message, `${message.t} fixture`)).not.toThrow()
    }
    expect(fixture.serverMessages.map(({ t }) => t).sort()).toEqual(expectedServerMessageTypes)
  })

  it('strictly decodes every topic snapshot with completeness in both directions', () => {
    const fixtureTopics: Record<string, unknown> = fixture.topics
    const fixtureTags = Object.keys(fixtureTopics).sort()
    const schemaTags = allTopics.map(({ tag }) => tag).sort()
    expect(fixtureTags).toEqual(schemaTags)

    for (const topic of allTopics) {
      expect(() =>
        decodeStrict(
          topic.snapshot as Schema.Schema<unknown>,
          fixtureTopics[topic.tag],
          `${topic.tag} fixture`,
        )
      ).not.toThrow()
    }
  })

  it('keeps every protocol-v1 topic snapshot-only', () => {
    for (const topic of allTopics) {
      expect(() => decodeStrict(topic.event, {}, `${topic.tag} event`)).toThrow()
    }
  })

  it('rejects invalid cross-scope sandbox parameters', () => {
    expect(() =>
      decodeStrict(
        SandboxConfigParamsSchema,
        { scope: 'global', projectId: 'project-1' },
        'global sandbox params',
      )
    ).toThrow()
    expect(() =>
      decodeStrict(
        SandboxConfigParamsSchema,
        { scope: 'thread', projectId: 'project-1' },
        'thread sandbox params',
      )
    ).toThrow()
  })
})
