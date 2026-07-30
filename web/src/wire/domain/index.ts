import { Schema } from 'effect'

const MutableArray = <A, I, R>(item: Schema.Schema<A, I, R>) =>
  Schema.mutable(Schema.Array(item))

const StringArray = MutableArray(Schema.String)

export const ProfileSchema = Schema.Struct({
  id: Schema.String,
  name: Schema.String,
})
export type Profile = Schema.Schema.Type<typeof ProfileSchema>

export const PlatformScriptsSchema = Schema.Struct({
  default: Schema.String,
  macos: Schema.String,
  linux: Schema.String,
  windows: Schema.String,
})
export type PlatformScripts = Schema.Schema.Type<typeof PlatformScriptsSchema>

export const EnvironmentVariableSchema = Schema.Struct({
  name: Schema.String,
  value: Schema.String,
})
export type EnvironmentVariable = Schema.Schema.Type<typeof EnvironmentVariableSchema>

export const EnvironmentActionSchema = Schema.Struct({
  id: Schema.String,
  name: Schema.String,
  scripts: PlatformScriptsSchema,
})
export type EnvironmentAction = Schema.Schema.Type<typeof EnvironmentActionSchema>

export const LocalEnvironmentSchema = Schema.Struct({
  name: Schema.String,
  setupScripts: PlatformScriptsSchema,
  cleanupScripts: PlatformScriptsSchema,
  variables: MutableArray(EnvironmentVariableSchema),
  actions: MutableArray(EnvironmentActionSchema),
})
export type LocalEnvironment = Schema.Schema.Type<typeof LocalEnvironmentSchema>

export const SandboxFileAccessSchema = Schema.Struct({
  read: StringArray,
  write: StringArray,
})
export type SandboxFileAccess = Schema.Schema.Type<typeof SandboxFileAccessSchema>

export const SandboxCommandRuleSchema = Schema.Struct({
  patterns: StringArray,
  files: Schema.optional(SandboxFileAccessSchema),
  network: Schema.optional(Schema.Boolean),
})
export type SandboxCommandRule = Schema.Schema.Type<typeof SandboxCommandRuleSchema>

export const SandboxConfigSchema = Schema.mutable(Schema.Struct({
  defaults: Schema.optional(SandboxFileAccessSchema),
  commands: Schema.optional(MutableArray(SandboxCommandRuleSchema)),
  network: Schema.optional(Schema.Boolean),
  pty: Schema.optional(Schema.Boolean),
  shell: Schema.optional(Schema.String),
  relatedProjects: Schema.optional(StringArray),
}))
export type SandboxConfig = Schema.Schema.Type<typeof SandboxConfigSchema>

export const EffectiveSandboxConfigSchema = Schema.Struct({
  defaults: SandboxFileAccessSchema,
  commands: MutableArray(SandboxCommandRuleSchema),
  network: Schema.Boolean,
  pty: Schema.Boolean,
  shell: Schema.String,
  relatedProjects: StringArray,
})
export type EffectiveSandboxConfig = Schema.Schema.Type<typeof EffectiveSandboxConfigSchema>

export const SandboxConfigScopeSchema = Schema.Literal('global', 'thread')
export type SandboxConfigScope = Schema.Schema.Type<typeof SandboxConfigScopeSchema>

export const SandboxConfigStateSchema = Schema.Struct({
  scope: SandboxConfigScopeSchema,
  path: Schema.String,
  exists: Schema.Boolean,
  parseError: Schema.optional(Schema.String),
  globalParseError: Schema.optional(Schema.String),
  config: SandboxConfigSchema,
  inherited: EffectiveSandboxConfigSchema,
  effective: EffectiveSandboxConfigSchema,
})
export type SandboxConfigState = Schema.Schema.Type<typeof SandboxConfigStateSchema>

export const ThreadSchema = Schema.Struct({
  id: Schema.String,
  title: Schema.String,
  cwd: Schema.String,
  createdAt: Schema.String,
  environmentSetupStatus: Schema.optional(Schema.Literal('pending', 'running', 'succeeded', 'failed')),
  lastPromptAt: Schema.optional(Schema.String),
  worktree: Schema.optional(Schema.Boolean),
  branch: Schema.optional(Schema.String),
  worktreePath: Schema.optional(Schema.String),
  autoNamed: Schema.optional(Schema.Boolean),
  titleLocked: Schema.optional(Schema.Boolean),
  archivedAt: Schema.optional(Schema.String),
  tokenLimit: Schema.optional(Schema.Number),
  costLimitUsd: Schema.optional(Schema.Number),
  rollbackPending: Schema.optional(Schema.Boolean),
  rollbackCleanupReady: Schema.optional(Schema.Boolean),
})
export type Thread = Schema.Schema.Type<typeof ThreadSchema>

export const ProjectSchema = Schema.Struct({
  id: Schema.String,
  name: Schema.String,
  path: Schema.String,
  profileId: Schema.String,
  host: Schema.String,
  isGitRepo: Schema.Boolean,
  createdAt: Schema.String,
  threads: MutableArray(ThreadSchema),
  worktreeBranchPrefix: Schema.String,
  environment: LocalEnvironmentSchema,
  figmaMCPEnabled: Schema.Boolean,
})
export type Project = Schema.Schema.Type<typeof ProjectSchema>

export const ThreadUsageTotalsSchema = Schema.Struct({
  inputTokens: Schema.Number,
  outputTokens: Schema.Number,
  cacheReadTokens: Schema.Number,
  cacheWriteTokens: Schema.Number,
  totalTokens: Schema.Number,
  costUsd: Schema.Number,
})
export type ThreadUsageTotals = Schema.Schema.Type<typeof ThreadUsageTotalsSchema>

export const ThreadUsageSnapshotSchema = Schema.Struct({
  projectId: Schema.String,
  threadId: Schema.String,
  own: ThreadUsageTotalsSchema,
  total: ThreadUsageTotalsSchema,
  tokenLimit: Schema.optional(Schema.Number),
  costLimitUsd: Schema.optional(Schema.Number),
  limitReached: Schema.Boolean,
  limitThreadId: Schema.optional(Schema.String),
  updatedAt: Schema.optional(Schema.String),
})
export type ThreadUsageSnapshot = Schema.Schema.Type<typeof ThreadUsageSnapshotSchema>

export const ThemeColorsSchema = Schema.Struct({
  canvas: Schema.String,
  sidebar: Schema.String,
  background: Schema.String,
  panel: Schema.String,
  raised: Schema.String,
  selected: Schema.String,
  border: Schema.String,
  foreground: Schema.String,
  muted: Schema.String,
  dim: Schema.String,
  cursor: Schema.String,
  selectionBackground: Schema.String,
  selectionForeground: Schema.String,
  black: Schema.String,
  red: Schema.String,
  green: Schema.String,
  yellow: Schema.String,
  blue: Schema.String,
  magenta: Schema.String,
  cyan: Schema.String,
  white: Schema.String,
  brightBlack: Schema.String,
  brightRed: Schema.String,
  brightGreen: Schema.String,
  brightYellow: Schema.String,
  brightBlue: Schema.String,
  brightMagenta: Schema.String,
  brightCyan: Schema.String,
  brightWhite: Schema.String,
})
export type ThemeColors = Schema.Schema.Type<typeof ThemeColorsSchema>

export const ThemeSettingsSchema = Schema.Struct({
  fontFamily: Schema.String,
  fontSize: Schema.Number,
  colors: ThemeColorsSchema,
})
export type ThemeSettings = Schema.Schema.Type<typeof ThemeSettingsSchema>

export const CodingAgentSettingSchema = Schema.Struct({
  id: Schema.String,
  name: Schema.String,
  kind: Schema.Literal('pi', 'pi-native', 'codex', 'claude', 'claude-gpt'),
  configDirectory: Schema.optional(Schema.String),
  isDefault: Schema.Boolean,
})
export type CodingAgentSetting = Schema.Schema.Type<typeof CodingAgentSettingSchema>

export const AppSettingsSchema = Schema.Struct({
  worktreeBasePath: Schema.String,
  defaultWorktreeBasePath: Schema.String,
  usingDefault: Schema.Boolean,
  archivedThreadRetentionDays: Schema.Number,
  orphanedWorktreeRetentionDays: Schema.Number,
  codingAgents: MutableArray(CodingAgentSettingSchema),
  theme: ThemeSettingsSchema,
  defaultTheme: ThemeSettingsSchema,
  usingDefaultTheme: Schema.Boolean,
})
export type AppSettings = Schema.Schema.Type<typeof AppSettingsSchema>

export const ThreadCleanupOverviewSchema = Schema.Struct({
  projectId: Schema.String,
  projectName: Schema.String,
  threadId: Schema.String,
  threadTitle: Schema.String,
  archivedAt: Schema.String,
  scheduledDeletionAt: Schema.NullOr(Schema.String),
})
export type ThreadCleanupOverview = Schema.Schema.Type<typeof ThreadCleanupOverviewSchema>

export const WorktreeCleanupOverviewSchema = Schema.Struct({
  projectId: Schema.String,
  projectName: Schema.optional(Schema.String),
  threadId: Schema.String,
  threadTitle: Schema.optional(Schema.String),
  worktreePath: Schema.String,
  branch: Schema.optional(Schema.String),
  detachedAt: Schema.String,
  scheduledDeletionAt: Schema.NullOr(Schema.String),
  hasUncommittedChanges: Schema.Boolean,
  inspectionError: Schema.optional(Schema.String),
})
export type WorktreeCleanupOverview = Schema.Schema.Type<typeof WorktreeCleanupOverviewSchema>

export const CleanupOverviewSchema = Schema.Struct({
  generatedAt: Schema.String,
  archivedThreadRetentionDays: Schema.Number,
  orphanedWorktreeRetentionDays: Schema.Number,
  threads: MutableArray(ThreadCleanupOverviewSchema),
  worktrees: MutableArray(WorktreeCleanupOverviewSchema),
})
export type CleanupOverview = Schema.Schema.Type<typeof CleanupOverviewSchema>

export const SessionClosureEventSchema = Schema.Struct({
  id: Schema.String,
  projectId: Schema.String,
  projectName: Schema.String,
  threadId: Schema.String,
  threadTitle: Schema.String,
  sessionNames: StringArray,
  lastActivityAt: Schema.String,
  closedAt: Schema.String,
  reason: Schema.Literal('inactivity'),
})
export type SessionClosureEvent = Schema.Schema.Type<typeof SessionClosureEventSchema>

export const SessionClosureOverviewSchema = Schema.Struct({
  generatedAt: Schema.String,
  inactivityHours: Schema.Number,
  events: MutableArray(SessionClosureEventSchema),
})
export type SessionClosureOverview = Schema.Schema.Type<typeof SessionClosureOverviewSchema>

export const AgentSkillItemStatusSchema = Schema.Struct({
  name: Schema.String,
  path: Schema.String,
  installed: Schema.Boolean,
  upToDate: Schema.Boolean,
})
export type AgentSkillItemStatus = Schema.Schema.Type<typeof AgentSkillItemStatusSchema>

export const AgentSkillStatusSchema = Schema.Struct({
  name: Schema.String,
  path: Schema.String,
  installed: Schema.Boolean,
  upToDate: Schema.Boolean,
  skills: Schema.optional(MutableArray(AgentSkillItemStatusSchema)),
})
export type AgentSkillStatus = Schema.Schema.Type<typeof AgentSkillStatusSchema>

export const GitBranchSchema = Schema.Struct({
  name: Schema.String,
  current: Schema.Boolean,
})
export type GitBranch = Schema.Schema.Type<typeof GitBranchSchema>

export const GitBranchStateSchema = Schema.Struct({
  isRepository: Schema.Boolean,
  current: Schema.String,
  detached: Schema.Boolean,
  branches: MutableArray(GitBranchSchema),
})
export type GitBranchState = Schema.Schema.Type<typeof GitBranchStateSchema>

export const BrowserPageSchema = Schema.Struct({
  id: Schema.String,
  title: Schema.String,
  url: Schema.String,
})
export type BrowserPage = Schema.Schema.Type<typeof BrowserPageSchema>

export const BrowserCurrentPageSchema = Schema.Struct({
  id: Schema.String,
  title: Schema.String,
  url: Schema.String,
  canGoBack: Schema.optional(Schema.Boolean),
  canGoForward: Schema.optional(Schema.Boolean),
  loading: Schema.optional(Schema.Boolean),
})
export type BrowserCurrentPage = Schema.Schema.Type<typeof BrowserCurrentPageSchema>

export const BrowserCapabilitiesSchema = Schema.Struct({
  nativeView: Schema.optional(Schema.Boolean),
  interactiveStream: Schema.optional(Schema.Boolean),
  preview: Schema.optional(Schema.Boolean),
})
export type BrowserCapabilities = Schema.Schema.Type<typeof BrowserCapabilitiesSchema>

export const BrowserStatusResultSchema = Schema.Struct({
  backend: Schema.String,
  presentation: Schema.String,
  capabilities: BrowserCapabilitiesSchema,
  reachable: Schema.optional(Schema.Boolean),
  running: Schema.optional(Schema.Boolean),
  pages: MutableArray(BrowserPageSchema),
  currentTargetId: Schema.NullOr(Schema.String),
  current: Schema.optional(BrowserCurrentPageSchema),
  error: Schema.optional(Schema.String),
})
export type BrowserStatusResult = Schema.Schema.Type<typeof BrowserStatusResultSchema>

export const BuiltInCodingAgentSchema = Schema.Literal('pi', 'codex', 'claude', 'claude-gpt')
export type BuiltInCodingAgent = Schema.Schema.Type<typeof BuiltInCodingAgentSchema>

export const ConfiguredClaudeAgentSchema = Schema.Union(
  Schema.TemplateLiteral('claude-profile-', Schema.String),
  Schema.TemplateLiteral('claude-gpt-profile-', Schema.String),
)
export type ConfiguredClaudeAgent = Schema.Schema.Type<typeof ConfiguredClaudeAgentSchema>

export const CodingAgentSchema = Schema.Union(BuiltInCodingAgentSchema, ConfiguredClaudeAgentSchema)
export type CodingAgent = Schema.Schema.Type<typeof CodingAgentSchema>

export const AgentContextStatusSourceSchema = Schema.Literal('pi-terminal', 'pi-native', 'claude-native')
export type AgentContextStatusSource = Schema.Schema.Type<typeof AgentContextStatusSourceSchema>

export const AgentContextStatusSchema = Schema.Struct({
  source: AgentContextStatusSourceSchema,
  tokens: Schema.NullOr(Schema.Number),
  contextWindow: Schema.Number,
  percent: Schema.NullOr(Schema.Number),
  model: Schema.optional(Schema.String),
  updatedAt: Schema.String,
})
export type AgentContextStatus = Schema.Schema.Type<typeof AgentContextStatusSchema>

export const CodingAgentChoiceSchema = Schema.Struct({
  id: Schema.String,
  label: Schema.String,
})
export type CodingAgentChoice = Schema.Schema.Type<typeof CodingAgentChoiceSchema>

export const CodingAgentConfigSchema = Schema.Struct({
  id: CodingAgentSchema,
  label: Schema.String,
  models: MutableArray(CodingAgentChoiceSchema),
  thinkingLevels: MutableArray(CodingAgentChoiceSchema),
})
export type CodingAgentConfig = Schema.Schema.Type<typeof CodingAgentConfigSchema>

export const PiActivityStateSchema = Schema.Literal('working', 'finished')
export type PiActivityState = Schema.Schema.Type<typeof PiActivityStateSchema>

export const PiThreadActivitySchema = Schema.Struct({
  projectId: Schema.String,
  threadId: Schema.String,
  state: PiActivityStateSchema,
  updatedAt: Schema.String,
})
export type PiThreadActivity = Schema.Schema.Type<typeof PiThreadActivitySchema>

export const TmuxWindowSchema = Schema.Struct({
  index: Schema.Number,
  name: Schema.String,
  active: Schema.Boolean,
})
export type TmuxWindow = Schema.Schema.Type<typeof TmuxWindowSchema>

export const TmuxBrowserWindowSchema = Schema.Struct({
  id: Schema.String,
  index: Schema.Number,
  name: Schema.String,
  active: Schema.Boolean,
  paneCount: Schema.Number,
  currentCommand: Schema.String,
})
export type TmuxBrowserWindow = Schema.Schema.Type<typeof TmuxBrowserWindowSchema>

export const TmuxBrowserSessionSchema = Schema.Struct({
  name: Schema.String,
  attached: Schema.Boolean,
  kind: Schema.optional(Schema.Literal('shell', 'tools')),
  projectId: Schema.optional(Schema.String),
  projectName: Schema.optional(Schema.String),
  threadId: Schema.optional(Schema.String),
  threadTitle: Schema.optional(Schema.String),
  windows: MutableArray(TmuxBrowserWindowSchema),
})
export type TmuxBrowserSession = Schema.Schema.Type<typeof TmuxBrowserSessionSchema>

export const ProcessWindowSchema = Schema.Struct({
  id: Schema.String,
  index: Schema.Number,
  name: Schema.String,
  currentCommand: Schema.String,
})
export type ProcessWindow = Schema.Schema.Type<typeof ProcessWindowSchema>

export const ThreadStatusErrorsSchema = Schema.Struct({
  gitBranches: Schema.optional(Schema.String),
  processes: Schema.optional(Schema.String),
  shellWindows: Schema.optional(Schema.String),
})
export type ThreadStatusErrors = Schema.Schema.Type<typeof ThreadStatusErrorsSchema>

export const ThreadStatusSnapshotSchema = Schema.Struct({
  gitBranches: Schema.NullOr(GitBranchStateSchema),
  contextStatuses: Schema.Struct({
    'pi-terminal': Schema.optional(AgentContextStatusSchema),
    'pi-native': Schema.optional(AgentContextStatusSchema),
    'claude-native': Schema.optional(AgentContextStatusSchema),
  }),
  processes: MutableArray(ProcessWindowSchema),
  shellWindows: MutableArray(TmuxWindowSchema),
  errors: ThreadStatusErrorsSchema,
})
export type ThreadStatusSnapshot = Schema.Schema.Type<typeof ThreadStatusSnapshotSchema>
