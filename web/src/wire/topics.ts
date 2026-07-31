import { Schema } from 'effect'
import {
  AgentSkillStatusSchema,
  AppSettingsSchema,
  BrowserRecordingSchema,
  BrowserStatusResultSchema,
  CleanupOverviewSchema,
  CodingAgentConfigSchema,
  GitBranchStateSchema,
  PiThreadActivitySchema,
  ProcessWebServerSchema,
  ProfileSchema,
  ProjectSchema,
  SessionClosureOverviewSchema,
  ThreadStatusSnapshotSchema,
  ThreadUsageSnapshotSchema,
  TmuxBrowserSessionSchema,
} from './domain'

const MutableArray = <A, I, R>(item: Schema.Schema<A, I, R>) =>
  Schema.mutable(Schema.Array(item))

export interface TopicDefinition<Tag extends string, Params, Snapshot> {
  readonly tag: Tag
  readonly params: Schema.Schema<Params>
  readonly snapshot: Schema.Schema<Snapshot>
  readonly event: Schema.Schema<never>
  readonly key: (params: Params) => string
  readonly topic: (params: Params) => Record<string, unknown> & { tag: Tag }
}

function globalTopic<Tag extends string, Snapshot>(
  tag: Tag,
  snapshot: Schema.Schema<Snapshot>,
): TopicDefinition<Tag, undefined, Snapshot> {
  return {
    tag,
    params: Schema.Undefined,
    snapshot,
    event: Schema.Never,
    key: () => '',
    topic: () => ({ tag }),
  }
}

function parameterizedTopic<Tag extends string, Params, Snapshot>(
  tag: Tag,
  params: Schema.Schema<Params>,
  snapshot: Schema.Schema<Snapshot>,
  key: (params: Params) => string,
): TopicDefinition<Tag, Params, Snapshot> {
  return {
    tag,
    params,
    snapshot,
    event: Schema.Never,
    key,
    topic: (value) => ({ tag, ...(value as Record<string, unknown>) }),
  }
}

export const ProjectsTopic = globalTopic('projects', MutableArray(ProjectSchema))
export const ProfilesTopic = globalTopic('profiles', MutableArray(ProfileSchema))
export const AgentActivityTopic = globalTopic('agentActivity', MutableArray(PiThreadActivitySchema))
export const ThreadUsageTopic = globalTopic('threadUsage', MutableArray(ThreadUsageSnapshotSchema))
export const ProcessWebServersTopic = globalTopic('processWebServers', MutableArray(ProcessWebServerSchema))
export const SettingsTopic = globalTopic('settings', AppSettingsSchema)
export const CleanupTopic = globalTopic('cleanup', CleanupOverviewSchema)
export const SessionClosuresTopic = globalTopic('sessionClosures', SessionClosureOverviewSchema)
export const TmuxSessionsTopic = globalTopic('tmuxSessions', MutableArray(TmuxBrowserSessionSchema))
export const AgentSkillsTopic = globalTopic('agentSkills', AgentSkillStatusSchema)

export const ThreadStatusParamsSchema = Schema.Struct({
  projectId: Schema.String,
  threadId: Schema.String,
})
export const ThreadStatusTopic = parameterizedTopic(
  'thread.status',
  ThreadStatusParamsSchema,
  ThreadStatusSnapshotSchema,
  ({ projectId, threadId }) => JSON.stringify([projectId, threadId]),
)

export const CodingAgentsParamsSchema = Schema.Struct({
  projectId: Schema.optional(Schema.String),
})
export const CodingAgentsTopic = parameterizedTopic(
  'codingAgents',
  CodingAgentsParamsSchema,
  MutableArray(CodingAgentConfigSchema),
  ({ projectId }) => projectId ?? '',
)

export const GitBranchesParamsSchema = Schema.Struct({ projectId: Schema.String })
export const GitBranchesTopic = parameterizedTopic(
  'git.branches',
  GitBranchesParamsSchema,
  GitBranchStateSchema,
  ({ projectId }) => projectId,
)

export const BrowserParamsSchema = Schema.Struct({
  projectId: Schema.String,
  threadId: Schema.String,
})
export const BrowserStatusTopic = parameterizedTopic(
  'browser.status',
  BrowserParamsSchema,
  BrowserStatusResultSchema,
  ({ projectId, threadId }) => JSON.stringify([projectId, threadId]),
)
/** @deprecated Recordings are included in BrowserStatusTopic. */
export const BrowserRecordingsTopic = parameterizedTopic(
  'browser.recordings',
  BrowserParamsSchema,
  MutableArray(BrowserRecordingSchema),
  ({ projectId, threadId }) => JSON.stringify([projectId, threadId]),
)

export const allTopics = [
  ProjectsTopic,
  ProfilesTopic,
  AgentActivityTopic,
  ThreadUsageTopic,
  ProcessWebServersTopic,
  ThreadStatusTopic,
  SettingsTopic,
  CodingAgentsTopic,
  CleanupTopic,
  SessionClosuresTopic,
  GitBranchesTopic,
  BrowserStatusTopic,
  BrowserRecordingsTopic,
  TmuxSessionsTopic,
  AgentSkillsTopic,
] as const

export type AnyTopic = (typeof allTopics)[number]
