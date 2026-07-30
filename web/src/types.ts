import type { CodingAgent } from '@/wire/domain'

// Reactive domain types are derived from the Effect schemas that validate
// state-socket snapshots. Keep this module as the compatibility import surface
// while the rest of the UI migrates to `wire/domain` directly.
export type {
  AgentContextStatus,
  AgentContextStatusSource,
  AgentSkillItemStatus,
  AgentSkillStatus,
  AppSettings,
  BrowserCapabilities,
  BrowserCurrentPage,
  BrowserPage,
  BrowserRecording,
  BrowserStatusResult,
  BuiltInCodingAgent,
  CleanupOverview,
  CodingAgent,
  CodingAgentChoice,
  CodingAgentConfig,
  CodingAgentSetting,
  ConfiguredClaudeAgent,
  EffectiveSandboxConfig,
  EnvironmentAction,
  EnvironmentVariable,
  GitBranch,
  GitBranchState,
  LocalEnvironment,
  PiActivityState,
  PiThreadActivity,
  PlatformScripts,
  ProcessWebServer,
  ProcessWindow,
  Profile,
  Project,
  SandboxCommandRule,
  SandboxConfig,
  SandboxConfigScope,
  SandboxConfigState,
  SandboxFileAccess,
  SavedWorkflow,
  SessionClosureEvent,
  SessionClosureOverview,
  ThemeColors,
  ThemeSettings,
  Thread,
  ThreadCleanupOverview,
  ThreadPlan,
  ThreadStatusErrors,
  ThreadStatusSnapshot,
  ThreadUsageSnapshot,
  ThreadUsageTotals,
  TmuxBrowserSession,
  TmuxBrowserWindow,
  TmuxWindow,
  WorkflowAgent,
  WorkflowLogEntry,
  WorkflowPhase,
  WorkflowRun,
  WorktreeCleanupOverview,
} from '@/wire/domain'

export type DirectorySuggestion = {
  name: string
  path: string
}

export type TerminalTool = 'terminal' | 'nvim' | 'lazygit' | 'pi' | 'process'

export type WorkspaceTool = TerminalTool | 'browser' | 'code'

export type BrowserActionOperation =
  | 'session.start'
  | 'session.disconnect'
  | 'session.stop'
  | 'tabs.new'
  | 'tabs.select'
  | 'tabs.close'
  | 'navigate.goto'
  | 'navigate.back'
  | 'navigate.forward'
  | 'navigate.reload'
  | 'recording.start'
  | 'recording.stop'
  | 'recording.status'
  | 'recording.delete'
  | 'evaluate'

export type BrowserActionParams = {
  url?: string
  targetId?: string
  recordingId?: string
  title?: string
  idleTimeoutMs?: number
  expression?: string
}

export type BrowserActionRequest = {
  operation: BrowserActionOperation
  params: BrowserActionParams
}

export type BrowserActionResponse<Result = unknown> = {
  result: Result
}

export type BrowserViewBounds = {
  x: number
  y: number
  width: number
  height: number
}

export type PiPresentation = 'native' | 'terminal'

export type CodingAgentSelection = CodingAgent | 'pi-native' | 'claude-native'

export type CodingAgentStart = {
  agent: CodingAgent
  presentation?: PiPresentation
  model: string
  thinkingLevel: string
  prompt: string
  imagePaths?: string[]
}

export type ConnectionStatus = 'connecting' | 'open' | 'closed' | 'error'
