export type Profile = {
  id: string
  name: string
}

export type DirectorySuggestion = {
  name: string
  path: string
}

export type Project = {
  id: string
  name: string
  path: string
  profileId: string
  host: string
  isGitRepo: boolean
  createdAt: string
  threads: Thread[]
  subAgentNestingDepthOverride?: number | null
  worktreeBranchPrefix: string
}

export type Thread = {
  id: string
  title: string
  cwd: string
  createdAt: string
  lastPromptAt?: string
  parentThreadId?: string
  agentModel?: string
  agentThinkingLevel?: string
  workflowRunId?: string
  workflowAgentId?: string
  worktree?: boolean
  branch?: string
  worktreePath?: string
  autoNamed?: boolean
  closedAt?: string
  archivedAt?: string
  bookmarked?: boolean
  tokenLimit?: number
  costLimitUsd?: number
  nestedDepth?: number
  rollbackPending?: boolean
}

export type ThreadUsageTotals = {
  inputTokens: number
  outputTokens: number
  cacheReadTokens: number
  cacheWriteTokens: number
  totalTokens: number
  costUsd: number
}

export type ThreadUsageSnapshot = {
  projectId: string
  threadId: string
  own: ThreadUsageTotals
  children: ThreadUsageTotals
  total: ThreadUsageTotals
  tokenLimit?: number
  costLimitUsd?: number
  limitReached: boolean
  limitThreadId?: string
  updatedAt?: string
}

export type ThemeColors = {
  canvas: string
  sidebar: string
  background: string
  panel: string
  raised: string
  selected: string
  border: string
  foreground: string
  muted: string
  dim: string
  cursor: string
  selectionBackground: string
  selectionForeground: string
  black: string
  red: string
  green: string
  yellow: string
  blue: string
  magenta: string
  cyan: string
  white: string
  brightBlack: string
  brightRed: string
  brightGreen: string
  brightYellow: string
  brightBlue: string
  brightMagenta: string
  brightCyan: string
  brightWhite: string
}

export type ThemeSettings = {
  fontFamily: string
  fontSize: number
  colors: ThemeColors
}

export type ClaudeCodeProfile = {
  id: string
  name: string
  configDirectory: string
}

export type AppSettings = {
  worktreeBasePath: string
  defaultWorktreeBasePath: string
  usingDefault: boolean
  archivedThreadRetentionDays: number
  orphanedWorktreeRetentionDays: number
  subAgentNestingDepth: number
  maxSubAgentNestingDepth: number
  disableWorkflows: boolean
  workflowKeywordTriggerEnabled: boolean
  workflowSizeGuideline: 'unrestricted' | 'small' | 'medium' | 'large'
  claudeCodeProfiles: ClaudeCodeProfile[]
  theme: ThemeSettings
  defaultTheme: ThemeSettings
  usingDefaultTheme: boolean
}

export type ThreadCleanupOverview = {
  projectId: string
  projectName: string
  threadId: string
  threadTitle: string
  archivedAt: string
  scheduledDeletionAt: string | null
}

export type WorktreeCleanupOverview = {
  projectId: string
  projectName?: string
  threadId: string
  threadTitle?: string
  worktreePath: string
  branch?: string
  detachedAt: string
  scheduledDeletionAt: string | null
  hasUncommittedChanges: boolean
  inspectionError?: string
}

export type CleanupOverview = {
  generatedAt: string
  archivedThreadRetentionDays: number
  orphanedWorktreeRetentionDays: number
  threads: ThreadCleanupOverview[]
  worktrees: WorktreeCleanupOverview[]
}

export type AgentSkillItemStatus = {
  name: string
  path: string
  installed: boolean
  upToDate: boolean
}

export type AgentSkillStatus = AgentSkillItemStatus & {
  skills?: AgentSkillItemStatus[]
}

export type GitBranch = {
  name: string
  current: boolean
}

export type GitBranchState = {
  isRepository: boolean
  current: string
  detached: boolean
  branches: GitBranch[]
}

export type TerminalTool = 'terminal' | 'nvim' | 'lazygit' | 'pi' | 'process'

export type WorkspaceTool = TerminalTool | 'browser' | 'code'

export type BrowserPage = {
  id: string
  title?: string
  url?: string
}

export type BrowserCurrentPage = BrowserPage & {
  canGoBack?: boolean
  canGoForward?: boolean
  loading?: boolean
}

export type BrowserCapabilities = {
  nativeView?: boolean
  interactiveStream?: boolean
  preview?: boolean
  recording?: boolean
}

export type BrowserRecording = {
  id: string
  state: 'starting' | 'recording' | 'finalizing' | 'completed'
  targetId: string
  title: string
  startedAt: string
  finishedAt?: string
  durationMs?: number
  bytes?: number
  mimeType?: string
  filename?: string
  idleTimeoutMs?: number
  idleDeadlineAt?: string
}

export type BrowserStatusResult = {
  backend?: string
  presentation?: 'native' | 'stream' | string
  capabilities?: BrowserCapabilities
  reachable?: boolean
  running?: boolean
  pages?: BrowserPage[]
  currentTargetId?: string
  current?: BrowserCurrentPage
  recording?: BrowserRecording | null
  recordings?: BrowserRecording[]
  error?: string
}

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

export type BuiltInCodingAgent = 'pi' | 'claude' | 'claude-gpt'

export type ClaudeCodeProfileAgent = `claude-profile-${string}`

export type CodingAgent = BuiltInCodingAgent | ClaudeCodeProfileAgent

export type PiPresentation = 'native' | 'terminal'

export type AgentContextStatusSource = 'pi-terminal' | 'pi-native' | 'claude-native'

export type AgentContextStatus = {
  source: AgentContextStatusSource
  tokens: number | null
  contextWindow: number
  percent: number | null
  model?: string
  updatedAt: string
}

export type CodingAgentSelection = CodingAgent | 'pi-native' | 'claude-native'

export type CodingAgentChoice = {
  id: string
  label: string
}

export type CodingAgentConfig = {
  id: CodingAgent
  label: string
  models: CodingAgentChoice[]
  thinkingLevels: CodingAgentChoice[]
}

export type CodingAgentStart = {
  agent: CodingAgent
  presentation?: PiPresentation
  model: string
  thinkingLevel: string
  prompt: string
  imagePaths?: string[]
}

export type ConnectionStatus = 'connecting' | 'open' | 'closed' | 'error'

export type PiActivityState = 'working' | 'finished'

export type PiThreadActivity = {
  projectId: string
  threadId: string
  state: PiActivityState
  updatedAt: string
}

export type TmuxWindow = {
  index: number
  name: string
  active: boolean
}

export type TmuxBrowserWindow = {
  id: string
  index: number
  name: string
  active: boolean
  paneCount: number
  currentCommand: string
}

export type TmuxBrowserSession = {
  name: string
  attached: boolean
  kind?: 'shell' | 'tools'
  projectId?: string
  projectName?: string
  threadId?: string
  threadTitle?: string
  windows: TmuxBrowserWindow[]
}

export type ProcessWindow = {
  id: string
  index: number
  name: string
  currentCommand: string
  webServers: string[]
}

export type ProcessWebServer = {
  projectId: string
  projectName: string
  threadId: string
  threadTitle: string
  processId: string
  processName: string
  url: string
}

export type WorkflowPhase = {
  title: string
  detail?: string
  model?: string
}

export type WorkflowAgent = {
  id: string
  label: string
  phase?: string
  state: 'starting' | 'working' | 'paused' | 'finished' | 'failed'
  threadId?: string
  childRunId?: number
  startedAt: string
  finishedAt?: string
  error?: string
  value?: unknown
  valueOmitted?: boolean
}

export type ThreadPlan = {
  id: string
  projectId: string
  threadId: string
  sourceThreadId: string
  title: string
  createdAt: string
  sizeBytes: number
}

export type WorkflowRun = {
  id: string
  projectId: string
  threadId: string
  state: 'queued' | 'running' | 'paused' | 'finished' | 'failed' | 'stopped'
  attempt?: number
  name: string
  description?: string
  whenToUse?: string
  phases?: WorkflowPhase[]
  currentPhase?: string
  scriptPath: string
  processId?: string
  createdAt: string
  startedAt?: string
  finishedAt?: string
  updatedAt: string
  error?: string
  result?: unknown
  agents: WorkflowAgent[]
}

export type SavedWorkflow = {
  name: string
  scope: 'project' | 'personal'
  path: string
}

export type ThreadStatusErrors = {
  gitBranches?: string
  processes?: string
  shellWindows?: string
  workflows?: string
  plans?: string
}

export type ThreadStatusSnapshot = {
  gitBranches: GitBranchState | null
  contextStatuses: Partial<Record<AgentContextStatusSource, AgentContextStatus>>
  processes: ProcessWindow[]
  shellWindows: TmuxWindow[]
  workflows: WorkflowRun[]
  plans: ThreadPlan[]
  errors: ThreadStatusErrors
}
