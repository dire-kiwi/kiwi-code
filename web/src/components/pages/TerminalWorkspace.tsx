import { Fragment, useCallback, useEffect, useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import {
  Activity,
  Bot,
  Braces,
  Code2,
  GitBranch,
  Globe2,
  SquareTerminal,
} from 'lucide-react'
import { getSettings, threadEventsPath } from '../../api'
import { claudeCodeProfileChoices, isCodingAgent } from '../../codingAgents'
import { workspacePath } from '../../routes'
import type {
  AgentContextStatus,
  AgentContextStatusSource,
  CodingAgent,
  CodingAgentSelection,
  ConnectionStatus,
  GitBranchState,
  PiPresentation,
  ProcessWindow,
  Profile,
  Project,
  Thread,
  ThreadPlan,
  WorkspaceTool,
  ThreadStatusSnapshot,
  ThreadUsageSnapshot,
  TmuxWindow,
  WorkflowRun,
} from '../../types'
import { Select } from '../atoms/Select'
import { OpenSidebarButton } from '../molecules/OpenSidebarButton'
import { GitBranchBar } from '../organisms/GitBranchBar'
import { ClaudeNativePane } from '../organisms/ClaudeNativePane'
import { PiNativePane } from '../organisms/PiNativePane'
import { ProcessWindowTabs } from '../organisms/ProcessWindowTabs'
import { BrowserPane } from '../organisms/BrowserPane'
import { CodeServerPane } from '../organisms/CodeServerPane'
import { TerminalSession } from '../organisms/TerminalSession'
import { ThreadPlanViewer } from '../organisms/ThreadPlanViewer'
import { ThreadProjectSidebar } from '../organisms/ThreadProjectSidebar'
import { TmuxWindowTabs } from '../organisms/TmuxWindowTabs'

type TerminalWorkspaceProps = {
  profiles: Profile[]
  project: Project
  thread: Thread
  usage?: ThreadUsageSnapshot
  activeTool: WorkspaceTool
  detailsExpanded: boolean
  nativeViewSuppressed?: boolean
  onDetailsExpandedChange: (expanded: boolean) => void
  onOpenSidebar: () => void
  onThreadInteraction: () => void
  onProjectUpdated: (project: Project) => void
  onThreadUpdated: (thread: Thread) => void
  onSelectThread: (thread: Thread) => void
  initialCodingAgent?: CodingAgent
  initialPresentation?: PiPresentation
  initialModel?: string
  initialThinkingLevel?: string
  initialPrompt?: string
  initialImagePaths?: string[]
  onInitialPromptSent?: () => void
}

const tools: Array<{
  id: WorkspaceTool
  label: string
  shortcut: string
  icon: typeof SquareTerminal
}> = [
  { id: 'pi', label: 'Pi', shortcut: '⌘1', icon: Bot },
  { id: 'terminal', label: 'Shell', shortcut: '⌘2', icon: SquareTerminal },
  { id: 'nvim', label: 'Neovim', shortcut: '⌘3', icon: Braces },
  { id: 'lazygit', label: 'Lazygit', shortcut: '⌘4', icon: GitBranch },
  { id: 'process', label: 'Process', shortcut: '⌘5', icon: Activity },
  { id: 'browser', label: 'Browser', shortcut: '⌘6', icon: Globe2 },
  { id: 'code', label: 'Code', shortcut: '⌘7', icon: Code2 },
]

const statusCopy: Record<ConnectionStatus, string> = {
  connecting: 'Starting',
  open: 'Live',
  closed: 'Ended',
  error: 'Offline',
}

const fallbackWorkspaceCodingAgents: Array<{ id: CodingAgentSelection; label: string }> = [
  { id: 'pi', label: 'Pi' },
  { id: 'claude', label: 'Claude Code' },
  { id: 'claude-gpt', label: 'Claude Code (with gpt)' },
  { id: 'pi-native', label: 'Pi Native' },
  { id: 'claude-native', label: 'Claude Native' },
]

function codingAgentStorageKey(projectId: string, threadId: string) {
  return `kiwi-code:coding-agent:${projectId}:${threadId}`
}

function rememberedCodingAgent(projectId: string, threadId: string): CodingAgent {
  try {
    const value = window.localStorage.getItem(codingAgentStorageKey(projectId, threadId))
    if (isCodingAgent(value)) return value
  } catch {
    // Storage can be unavailable under restrictive browser policies.
  }
  return 'pi'
}

function piPresentationStorageKey(projectId: string, threadId: string) {
  return `kiwi-code:pi-presentation:${projectId}:${threadId}`
}

function claudePresentationStorageKey(projectId: string, threadId: string) {
  return `kiwi-code:claude-presentation:${projectId}:${threadId}`
}

function rememberedPresentation(storageKey: string, fallback: PiPresentation): PiPresentation {
  try {
    const value = window.localStorage.getItem(storageKey)
    if (value === 'native' || value === 'terminal') return value
  } catch {
    // Storage can be unavailable under restrictive browser policies.
  }
  return fallback
}

export function TerminalWorkspace({
  profiles,
  project,
  thread,
  usage,
  activeTool,
  detailsExpanded,
  nativeViewSuppressed = false,
  onDetailsExpandedChange,
  onOpenSidebar,
  onThreadInteraction,
  onProjectUpdated,
  onThreadUpdated,
  onSelectThread,
  initialCodingAgent,
  initialPresentation,
  initialModel,
  initialThinkingLevel,
  initialPrompt,
  initialImagePaths,
  onInitialPromptSent,
}: TerminalWorkspaceProps) {
  const navigate = useNavigate()
  const readOnlySubagent = Boolean(thread.parentThreadId)
  const [codingAgentChoices, setCodingAgentChoices] = useState(() => {
    if (!initialCodingAgent || fallbackWorkspaceCodingAgents.some((agent) => agent.id === initialCodingAgent)) {
      return fallbackWorkspaceCodingAgents
    }
    return [
      ...fallbackWorkspaceCodingAgents.slice(0, 3),
      { id: initialCodingAgent, label: 'Claude Code' },
      ...fallbackWorkspaceCodingAgents.slice(3),
    ]
  })
  const [codingAgent, setCodingAgent] = useState<CodingAgent>(() =>
    readOnlySubagent ? 'pi' : initialCodingAgent ?? rememberedCodingAgent(project.id, thread.id),
  )
  const [piPresentation, setPiPresentation] = useState<PiPresentation>(() =>
    readOnlySubagent
      ? 'native'
      : initialCodingAgent === 'pi' && initialPresentation
        ? initialPresentation
        : rememberedPresentation(piPresentationStorageKey(project.id, thread.id), 'native'),
  )
  const [claudePresentation, setClaudePresentation] = useState<PiPresentation>(() =>
    initialCodingAgent === 'claude' && initialPresentation
      ? initialPresentation
      : rememberedPresentation(claudePresentationStorageKey(project.id, thread.id), 'terminal'),
  )
  const [piNativeOpened, setPiNativeOpened] = useState(() => piPresentation === 'native')
  const [piTerminalOpened, setPiTerminalOpened] = useState(() => piPresentation === 'terminal')
  const [claudeNativeOpened, setClaudeNativeOpened] = useState(() => claudePresentation === 'native')
  const [claudeTerminalOpened, setClaudeTerminalOpened] = useState(() => claudePresentation === 'terminal')
  const [piPresentationStatuses, setPiPresentationStatuses] = useState<Record<PiPresentation, ConnectionStatus>>({
    native: 'connecting',
    terminal: 'connecting',
  })
  const [claudePresentationStatuses, setClaudePresentationStatuses] = useState<Record<PiPresentation, ConnectionStatus>>({
    native: 'connecting',
    terminal: 'connecting',
  })
  const initialPiPresentationRef = useRef(piPresentation)
  const initialClaudePresentationRef = useRef(claudePresentation)
  const [openedTools, setOpenedTools] = useState<WorkspaceTool[]>(() => [activeTool])
  const [statuses, setStatuses] = useState<Partial<Record<WorkspaceTool, ConnectionStatus>>>(() => ({
    [activeTool]: 'connecting',
  }))
  const [processWindows, setProcessWindows] = useState<ProcessWindow[]>([])
  const [selectedProcessId, setSelectedProcessId] = useState<string | null>(null)
  const [processesLoading, setProcessesLoading] = useState(true)
  const [processesError, setProcessesError] = useState('')
  const [branchState, setBranchState] = useState<GitBranchState | null>(null)
  const [contextStatuses, setContextStatuses] = useState<Partial<Record<AgentContextStatusSource, AgentContextStatus>>>({})
  const [nativeContextStatus, setNativeContextStatus] = useState<AgentContextStatus | null>(null)
  const [claudeNativeContextStatus, setClaudeNativeContextStatus] = useState<AgentContextStatus | null>(null)
  const [branchesLoading, setBranchesLoading] = useState(true)
  const [branchesError, setBranchesError] = useState('')
  const [shellWindows, setShellWindows] = useState<TmuxWindow[]>([])
  const [shellWindowsLoading, setShellWindowsLoading] = useState(true)
  const [shellWindowsError, setShellWindowsError] = useState('')
  const [workflowRuns, setWorkflowRuns] = useState<WorkflowRun[]>([])
  const [workflowsError, setWorkflowsError] = useState('')
  const [threadPlans, setThreadPlans] = useState<ThreadPlan[]>([])
  const [plansError, setPlansError] = useState('')
  const [selectedPlan, setSelectedPlan] = useState<ThreadPlan | null>(null)
  const [statusReloadKey, setStatusReloadKey] = useState(0)
  const [branchOverlayOpen, setBranchOverlayOpen] = useState(false)
  const toolTabsRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const controller = new AbortController()
    getSettings(controller.signal)
      .then((settings) => {
        if (controller.signal.aborted) return
        const choices = [
          ...fallbackWorkspaceCodingAgents.slice(0, 3),
          ...claudeCodeProfileChoices(settings.claudeCodeProfiles),
          ...fallbackWorkspaceCodingAgents.slice(3),
        ]
        setCodingAgentChoices(choices)
        setCodingAgent((current) => choices.some((choice) => choice.id === current) ? current : 'pi')
      })
      .catch(() => {
        // Keep the built-in choices when settings are temporarily unavailable.
      })
    return () => controller.abort()
  }, [])

  const markToolOpened = useCallback((tool: WorkspaceTool) => {
    setOpenedTools((current) => (current.includes(tool) ? current : [...current, tool]))
  }, [])

  const activateTool = useCallback((tool: WorkspaceTool) => {
    setSelectedPlan(null)
    markToolOpened(tool)
    navigate(workspacePath(project.id, thread.id, tool))
  }, [markToolOpened, navigate, project.id, thread.id])

  function selectCodingAgent(selection: CodingAgentSelection) {
    if (readOnlySubagent) return
    const agent: CodingAgent = selection === 'claude-native'
      ? 'claude'
      : selection === 'pi-native'
        ? 'pi'
        : selection
    const presentation: PiPresentation = selection === 'pi-native' || selection === 'claude-native'
      ? 'native'
      : 'terminal'
    const selectionUnchanged = agent === codingAgent
      && (agent !== 'pi' || presentation === piPresentation)
      && (agent !== 'claude' || presentation === claudePresentation)
    if (selectionUnchanged) {
      activateTool('pi')
      return
    }
    if (agent === 'pi') {
      if (presentation === 'native') setPiNativeOpened(true)
      if (presentation === 'terminal') setPiTerminalOpened(true)
      setPiPresentation(presentation)
    }
    if (agent === 'claude') {
      if (presentation === 'native') setClaudeNativeOpened(true)
      if (presentation === 'terminal') setClaudeTerminalOpened(true)
      setClaudePresentation(presentation)
    }
    setCodingAgent(agent)
    setStatuses((current) => ({ ...current, pi: 'connecting' }))
    activateTool('pi')
  }

  useEffect(() => {
    if (readOnlySubagent) return
    try {
      window.localStorage.setItem(codingAgentStorageKey(project.id, thread.id), codingAgent)
    } catch {
      // The selection still works for this page when storage is unavailable.
    }
  }, [codingAgent, project.id, readOnlySubagent, thread.id])

  useEffect(() => {
    if (readOnlySubagent) return
    try {
      window.localStorage.setItem(piPresentationStorageKey(project.id, thread.id), piPresentation)
    } catch {
      // The selection still works for this page when storage is unavailable.
    }
  }, [piPresentation, project.id, readOnlySubagent, thread.id])

  useEffect(() => {
    if (readOnlySubagent) return
    try {
      window.localStorage.setItem(claudePresentationStorageKey(project.id, thread.id), claudePresentation)
    } catch {
      // The selection still works for this page when storage is unavailable.
    }
  }, [claudePresentation, project.id, readOnlySubagent, thread.id])

  useEffect(() => {
    markToolOpened(activeTool)
    setSelectedPlan(null)
  }, [activeTool, markToolOpened])

  useEffect(() => {
    const events = new EventSource(threadEventsPath(project.id, thread.id))
    setProcessesLoading(true)
    setBranchesLoading(true)
    setShellWindowsLoading(true)
    setThreadPlans([])
    setPlansError('')

    function handleStatus(event: Event) {
      try {
        const value: unknown = JSON.parse((event as MessageEvent<string>).data)
        if (!value || typeof value !== 'object') return
        const snapshot = value as ThreadStatusSnapshot
        if (!Array.isArray(snapshot.processes) || !Array.isArray(snapshot.shellWindows) || !Array.isArray(snapshot.workflows)) return

        setProcessWindows(snapshot.processes)
        setSelectedProcessId((current) =>
          current && snapshot.processes.some((window) => window.id === current)
            ? current
            : snapshot.processes[0]?.id ?? null,
        )
        setBranchState(snapshot.gitBranches)
        setContextStatuses(snapshot.contextStatuses ?? {})
        setShellWindows(snapshot.shellWindows)
        setWorkflowRuns(snapshot.workflows)
        setThreadPlans(Array.isArray(snapshot.plans) ? snapshot.plans : [])
        setProcessesError(snapshot.errors?.processes ?? '')
        setBranchesError(snapshot.errors?.gitBranches ?? '')
        setShellWindowsError(snapshot.errors?.shellWindows ?? '')
        setWorkflowsError(snapshot.errors?.workflows ?? '')
        setPlansError(snapshot.errors?.plans ?? '')
        setProcessesLoading(false)
        setBranchesLoading(false)
        setShellWindowsLoading(false)
      } catch {
        // Ignore a malformed event; the next authoritative snapshot replaces it.
      }
    }

    events.addEventListener('thread-status', handleStatus)
    events.onerror = () => {
      const message = 'Live thread status disconnected; reconnecting…'
      setProcessesError(message)
      setBranchesError(message)
      setShellWindowsError(message)
      setWorkflowsError(message)
      setPlansError(message)
      setProcessesLoading(false)
      setBranchesLoading(false)
      setShellWindowsLoading(false)
    }
    return () => events.close()
  }, [project.id, statusReloadKey, thread.id])

  useEffect(() => {
    setNativeContextStatus(null)
    setClaudeNativeContextStatus(null)
  }, [project.id, thread.id])

  useEffect(() => {
    let frame = 0
    function revealActiveTool() {
      window.cancelAnimationFrame(frame)
      frame = window.requestAnimationFrame(() => {
        const scroller = toolTabsRef.current
        const selected = scroller?.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]')
        if (!scroller || !selected) return
        const scrollerRect = scroller.getBoundingClientRect()
        const selectedRect = selected.getBoundingClientRect()
        if (selectedRect.left < scrollerRect.left) {
          scroller.scrollLeft -= scrollerRect.left - selectedRect.left
        } else if (selectedRect.right > scrollerRect.right) {
          scroller.scrollLeft += selectedRect.right - scrollerRect.right
        }
      })
    }
    revealActiveTool()
    window.addEventListener('resize', revealActiveTool)
    return () => {
      window.cancelAnimationFrame(frame)
      window.removeEventListener('resize', revealActiveTool)
    }
  }, [activeTool])

  useEffect(() => {
    function handleKeyDown(event: KeyboardEvent) {
      if (!event.metaKey && !event.ctrlKey) return
      const index = Number(event.key) - 1
      const tool = tools[index]
      if (!tool) return
      event.preventDefault()
      activateTool(tool.id)
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [activateTool])

  const activeStatus: ConnectionStatus = activeTool === 'pi' && codingAgent === 'pi'
    ? piPresentationStatuses[piPresentation]
    : activeTool === 'pi' && codingAgent === 'claude'
      ? claudePresentationStatuses[claudePresentation]
    : activeTool === 'process' && !selectedProcessId
      ? processesLoading ? 'connecting' : 'closed'
      : statuses[activeTool] ?? 'connecting'
  const activeStatusCopy = activeTool === 'process' && !selectedProcessId && !processesLoading
    ? 'Empty'
    : activeTool === 'browser'
      ? ({ connecting: 'Checking', open: 'Ready', closed: 'Stopped', error: 'Offline' } as const)[activeStatus]
      : activeTool === 'code'
        ? ({ connecting: 'Starting', open: 'Ready', closed: 'Desktop only', error: 'Unavailable' } as const)[activeStatus]
        : statusCopy[activeStatus]
  const sessionTools = openedTools.includes(activeTool)
    ? openedTools
    : [...openedTools, activeTool]
  const availableCodingAgents = readOnlySubagent
    ? codingAgentChoices.filter((agent) => agent.id === 'pi-native')
    : codingAgentChoices
  const codingAgentSelection: CodingAgentSelection = codingAgent === 'claude'
    ? claudePresentation === 'native' ? 'claude-native' : 'claude'
    : codingAgent === 'pi'
      ? piPresentation === 'native' ? 'pi-native' : 'pi'
      : codingAgent
  const selectedCodingAgent = availableCodingAgents.find((agent) => agent.id === codingAgentSelection) ?? availableCodingAgents[0]
  const contextStatus = codingAgent === 'claude'
    ? claudePresentation === 'native' ? claudeNativeContextStatus : null
    : codingAgent !== 'pi'
      ? null
      : piPresentation === 'native'
        ? nativeContextStatus
        : contextStatuses['pi-terminal'] ?? null
  const hasSecondaryTabs = activeTool === 'terminal' || activeTool === 'process'

  return (
    <div
      className="relative flex h-full min-w-0 bg-ghost-black"
      onFocusCapture={onThreadInteraction}
      onKeyDownCapture={onThreadInteraction}
      onPointerDownCapture={onThreadInteraction}
      onWheelCapture={onThreadInteraction}
    >
      <div className="flex min-w-0 flex-1 flex-col">
        <header className={`shrink-0 bg-ghost-panel/95 ${hasSecondaryTabs ? 'border-b border-ghost-border/70' : ''}`}>
        <div className="relative flex min-h-[41px] items-center gap-3 pl-3 pr-12 md:px-3 lg:px-5">
          <span aria-hidden="true" className="pointer-events-none absolute inset-x-0 bottom-0 h-px bg-ghost-border/70" />
          <OpenSidebarButton onClick={onOpenSidebar} shrink />

          <div ref={toolTabsRef} className="workspace-tool-tabs-scroll min-w-0 self-stretch flex-1 overflow-x-auto overscroll-x-contain">
            <div
              className="relative z-[1] flex h-full w-max min-w-full items-end justify-center gap-1 px-3"
              role="tablist"
              aria-label="Workspace tools"
            >
              {tools.map((tool) => {
              const Icon = tool.icon
              const active = activeTool === tool.id
              const toolStatus = tool.id === 'pi' && codingAgent === 'pi'
                ? piPresentationStatuses[piPresentation]
                : tool.id === 'pi' && codingAgent === 'claude'
                  ? claudePresentationStatuses[claudePresentation]
                  : statuses[tool.id]
              if (tool.id === 'pi') {
                return (
                  <div
                    key={tool.id}
                    className={`group relative flex h-9 shrink-0 items-center rounded-lg text-[11px] font-medium transition ${
                      active
                        ? 'workspace-tool-tab-active text-ghost-foreground'
                        : 'text-ghost-dim hover:bg-ghost-raised/70 hover:text-ghost-bright-white'
                    }`}
                  >
                    <Link
                      to={workspacePath(project.id, thread.id, tool.id)}
                      role="tab"
                      aria-label={selectedCodingAgent.label}
                      aria-selected={active}
                      onClick={() => {
                        setSelectedPlan(null)
                        markToolOpened(tool.id)
                      }}
                      className="flex h-full items-center gap-2 pl-2.5 pr-1.5 lg:pl-3.5 lg:pr-2"
                    >
                      <Icon size={14} strokeWidth={1.8} className={active ? 'text-ghost-green' : ''} />
                      <span className="hidden lg:inline">{selectedCodingAgent.label}</span>
                      <span className="hidden font-mono text-[8px] text-ghost-faint 2xl:inline">{tool.shortcut}</span>
                    </Link>
                    <div className="relative h-6 w-7 shrink-0 border-l border-ghost-border/55 focus-within:ring-1 focus-within:ring-inset focus-within:ring-ghost-green/55">
                      <Select
                        variant="icon"
                        value={codingAgentSelection}
                        options={availableCodingAgents.map((agent) => ({
                          value: agent.id,
                          label: agent.label,
                        }))}
                        disabled={readOnlySubagent}
                        onChange={(agent) => selectCodingAgent(agent as CodingAgentSelection)}
                        aria-label="Coding agent"
                        title={readOnlySubagent ? 'Subagents use read-only Pi Native' : 'Choose coding agent'}
                      />
                    </div>
                    {toolStatus === 'open' && !active && (
                      <span className="absolute right-7 top-1.5 size-1 rounded-full bg-ghost-green/80" />
                    )}
                  </div>
                )
              }

              return (
                <Link
                  key={tool.id}
                  to={workspacePath(project.id, thread.id, tool.id)}
                  role="tab"
                  aria-selected={active}
                  onClick={() => {
                    setSelectedPlan(null)
                    markToolOpened(tool.id)
                  }}
                  className={`group relative flex h-9 shrink-0 items-center gap-2 rounded-lg px-2.5 text-[11px] font-medium transition lg:px-3.5 ${
                    active
                      ? 'workspace-tool-tab-active text-ghost-foreground'
                      : 'text-ghost-dim hover:bg-ghost-raised/70 hover:text-ghost-bright-white'
                  }`}
                >
                  <Icon size={14} strokeWidth={1.8} className={active ? 'text-ghost-green' : ''} />
                  <span>{tool.label}</span>
                  {tool.id === 'process' && processWindows.length > 0 && (
                    <span
                      className={`inline-flex h-5 min-w-5 items-center justify-center rounded-full border px-1.5 font-mono text-[10px] font-bold leading-none shadow-[0_0_8px_rgba(181,189,104,0.28)] ${
                        active
                          ? 'border-ghost-green bg-ghost-green text-ghost-black'
                          : 'border-ghost-green/50 bg-ghost-green/15 text-ghost-green'
                      }`}
                      aria-label={`${processWindows.length} active process${processWindows.length === 1 ? '' : 'es'}`}
                      title={`${processWindows.length} active process${processWindows.length === 1 ? '' : 'es'}`}
                    >
                      {processWindows.length}
                    </span>
                  )}
                  <span className="hidden font-mono text-[8px] text-ghost-faint 2xl:inline">{tool.shortcut}</span>
                  {toolStatus === 'open' && !active && (
                    <span className="absolute right-1.5 top-1.5 size-1 rounded-full bg-ghost-green/80" />
                  )}
                </Link>
              )
              })}
            </div>
          </div>

          <div className={`hidden shrink-0 items-center justify-end gap-2 ${detailsExpanded ? '2xl:flex' : 'lg:flex'}`}>
            <div className="flex items-center gap-2 rounded-full border border-ghost-border/70 bg-ghost-background/70 px-2.5 py-1.5">
              <span
                className={`size-1.5 rounded-full ${
                  activeStatus === 'open'
                    ? 'bg-ghost-green shadow-[0_0_8px_rgba(181,189,104,0.55)]'
                    : activeStatus === 'connecting'
                      ? 'animate-pulse bg-ghost-yellow'
                      : activeStatus === 'error'
                        ? 'bg-ghost-bright-red'
                        : 'bg-ghost-faint'
                }`}
              />
              <span className="text-[9px] font-medium uppercase tracking-[0.12em] text-ghost-dim">
                {activeStatusCopy}
              </span>
            </div>
          </div>
        </div>
        {activeTool === 'terminal' && (
          <TmuxWindowTabs
            projectId={project.id}
            threadId={thread.id}
            windows={shellWindows}
            loading={shellWindowsLoading}
            error={shellWindowsError}
            onWindowsChange={setShellWindows}
            onRetry={() => setStatusReloadKey((value) => value + 1)}
          />
        )}
        {activeTool === 'process' && (
          <ProcessWindowTabs
            windows={processWindows}
            selectedId={selectedProcessId}
            loading={processesLoading}
            error={processesError}
            onSelect={setSelectedProcessId}
            onRetry={() => setStatusReloadKey((value) => value + 1)}
          />
        )}
      </header>

      <main className="relative min-h-0 flex-1">
        <div className="absolute inset-0 overflow-hidden bg-ghost-background">
          <div className="relative h-full min-h-0">
            {sessionTools.map((tool) => {
              if (tool === 'browser') {
                return (
                  <BrowserPane
                    key="browser"
                    projectId={project.id}
                    threadId={thread.id}
                    threadTitle={thread.title}
                    active={activeTool === 'browser'}
                    suppressed={nativeViewSuppressed || branchOverlayOpen || selectedPlan !== null}
                    onWorkspaceShortcut={(index) => {
                      const tool = tools[index - 1]
                      if (tool) activateTool(tool.id)
                    }}
                    onStatusChange={(status) =>
                      setStatuses((current) =>
                        current.browser === status ? current : { ...current, browser: status },
                      )
                    }
                  />
                )
              }
              if (tool === 'code') {
                return (
                  <CodeServerPane
                    key="code"
                    projectId={project.id}
                    threadId={thread.id}
                    threadTitle={thread.title}
                    workspacePath={thread.cwd}
                    active={activeTool === 'code'}
                    suppressed={nativeViewSuppressed || branchOverlayOpen || selectedPlan !== null}
                    onWorkspaceShortcut={(index) => {
                      const selectedTool = tools[index - 1]
                      if (selectedTool) activateTool(selectedTool.id)
                    }}
                    onStatusChange={(status) =>
                      setStatuses((current) =>
                        current.code === status ? current : { ...current, code: status },
                      )
                    }
                  />
                )
              }
              const processId = tool === 'process' ? selectedProcessId ?? undefined : undefined
              if (tool === 'process' && !processId) return null
              if (tool === 'pi' && codingAgent === 'claude') {
                const initialPromptTargetsNative = initialClaudePresentationRef.current === 'native'
                return (
                  <Fragment key="pi:claude">
                    {claudeNativeOpened && (
                      <ClaudeNativePane
                        projectId={project.id}
                        threadId={thread.id}
                        threadTitle={thread.title}
                        initialModel={initialCodingAgent === 'claude' ? initialModel : undefined}
                        initialThinkingLevel={initialCodingAgent === 'claude' ? initialThinkingLevel : undefined}
                        initialPrompt={initialCodingAgent === 'claude' && initialPromptTargetsNative ? initialPrompt : undefined}
                        initialImagePaths={initialCodingAgent === 'claude' && initialPromptTargetsNative ? initialImagePaths : undefined}
                        onInitialPromptSent={initialCodingAgent === 'claude' && initialPromptTargetsNative ? onInitialPromptSent : undefined}
                        active={activeTool === 'pi' && claudePresentation === 'native'}
                        onStatusChange={(status) =>
                          setClaudePresentationStatuses((current) =>
                            current.native === status ? current : { ...current, native: status },
                          )
                        }
                        onContextStatusChange={setClaudeNativeContextStatus}
                      />
                    )}
                    {claudeTerminalOpened && (
                      <TerminalSession
                        key="pi:claude:terminal"
                        projectId={project.id}
                        threadId={thread.id}
                        threadTitle={thread.title}
                        tool="pi"
                        codingAgent="claude"
                        initialModel={initialCodingAgent === 'claude' ? initialModel : undefined}
                        initialThinkingLevel={initialCodingAgent === 'claude' ? initialThinkingLevel : undefined}
                        initialPrompt={initialCodingAgent === 'claude' && !initialPromptTargetsNative ? initialPrompt : undefined}
                        onInitialPromptSent={initialCodingAgent === 'claude' && !initialPromptTargetsNative ? onInitialPromptSent : undefined}
                        active={activeTool === 'pi' && claudePresentation === 'terminal'}
                        onStatusChange={(status) =>
                          setClaudePresentationStatuses((current) =>
                            current.terminal === status ? current : { ...current, terminal: status },
                          )
                        }
                      />
                    )}
                  </Fragment>
                )
              }
              if (tool === 'pi' && codingAgent === 'pi') {
                const initialPromptTargetsNative = initialPiPresentationRef.current === 'native'
                return (
                  <Fragment key="pi:pi">
                    {piNativeOpened && (
                      <PiNativePane
                        projectId={project.id}
                        threadId={thread.id}
                        threadTitle={thread.title}
                        initialModel={initialCodingAgent === 'pi' ? initialModel : undefined}
                        initialThinkingLevel={initialCodingAgent === 'pi' ? initialThinkingLevel : undefined}
                        initialPrompt={initialCodingAgent === 'pi' && initialPromptTargetsNative ? initialPrompt : undefined}
                        initialImagePaths={initialCodingAgent === 'pi' && initialPromptTargetsNative ? initialImagePaths : undefined}
                        onInitialPromptSent={initialCodingAgent === 'pi' && initialPromptTargetsNative ? onInitialPromptSent : undefined}
                        readOnly={readOnlySubagent}
                        active={activeTool === 'pi' && piPresentation === 'native'}
                        onStatusChange={(status) =>
                          setPiPresentationStatuses((current) =>
                            current.native === status ? current : { ...current, native: status },
                          )
                        }
                        onContextStatusChange={setNativeContextStatus}
                      />
                    )}
                    {piTerminalOpened && (
                      <TerminalSession
                        key="pi:pi:terminal"
                        projectId={project.id}
                        threadId={thread.id}
                        threadTitle={thread.title}
                        tool="pi"
                        codingAgent="pi"
                        initialModel={initialCodingAgent === 'pi' ? initialModel : undefined}
                        initialThinkingLevel={initialCodingAgent === 'pi' ? initialThinkingLevel : undefined}
                        initialPrompt={initialCodingAgent === 'pi' && !initialPromptTargetsNative ? initialPrompt : undefined}
                        onInitialPromptSent={initialCodingAgent === 'pi' && !initialPromptTargetsNative ? onInitialPromptSent : undefined}
                        active={activeTool === 'pi' && piPresentation === 'terminal'}
                        onStatusChange={(status) =>
                          setPiPresentationStatuses((current) =>
                            current.terminal === status ? current : { ...current, terminal: status },
                          )
                        }
                      />
                    )}
                  </Fragment>
                )
              }
              return (
                <TerminalSession
                  key={tool === 'process' ? `${tool}:${processId}` : tool === 'pi' ? `${tool}:${codingAgent}` : tool}
                  projectId={project.id}
                  threadId={thread.id}
                  threadTitle={thread.title}
                  tool={tool}
                  codingAgent={codingAgent}
                  terminalLabel={tool === 'pi' ? selectedCodingAgent.label : undefined}
                  initialModel={tool === 'pi' && codingAgent === initialCodingAgent ? initialModel : undefined}
                  initialThinkingLevel={tool === 'pi' && codingAgent === initialCodingAgent ? initialThinkingLevel : undefined}
                  initialPrompt={tool === 'pi' && codingAgent === initialCodingAgent ? initialPrompt : undefined}
                  onInitialPromptSent={tool === 'pi' && codingAgent === initialCodingAgent ? onInitialPromptSent : undefined}
                  processId={processId}
                  active={activeTool === tool}
                  onStatusChange={(status) =>
                    setStatuses((current) =>
                      current[tool] === status ? current : { ...current, [tool]: status },
                    )
                  }
                />
              )
            })}
            {activeTool === 'process' && !selectedProcessId && (
              <div className="absolute inset-0 grid place-items-center px-6 text-center">
                <div className="max-w-sm">
                  <span className="mx-auto grid size-12 place-items-center rounded-xl border border-ghost-border/80 bg-ghost-panel text-ghost-dim">
                    <Activity size={20} />
                  </span>
                  <p className="mt-4 text-sm font-medium text-ghost-bright-white">
                    {processesLoading ? 'Loading process shells' : 'No process shells'}
                  </p>
                  <p className="mt-1.5 text-[11px] leading-5 text-ghost-muted">
                    Agents create a shell when they start a long-running command. Install the Kiwi Code skill in Settings; new shells appear here automatically.
                  </p>
                </div>
              </div>
            )}
            {selectedPlan && (
              <ThreadPlanViewer
                projectId={project.id}
                plan={selectedPlan}
                onClose={() => setSelectedPlan(null)}
              />
            )}
          </div>
        </div>
      </main>

        <GitBranchBar
          projectId={project.id}
          threadId={thread.id}
          worktree={thread.worktree}
          branchState={branchState}
          contextStatus={contextStatus}
          loading={branchesLoading}
          loadError={branchesError}
          onBranchStateChange={setBranchState}
          onRetry={() => setStatusReloadKey((value) => value + 1)}
          onOverlayOpenChange={setBranchOverlayOpen}
        />
      </div>

      <ThreadProjectSidebar
        profiles={profiles}
        project={project}
        thread={thread}
        usage={usage}
        workflowRuns={workflowRuns}
        workflowsError={workflowsError}
        plans={threadPlans}
        plansError={plansError}
        onViewPlan={setSelectedPlan}
        onWorkflowUpdated={(updated) => setWorkflowRuns((current) => current.map((run) => run.id === updated.id ? updated : run))}
        expanded={detailsExpanded}
        onExpandedChange={onDetailsExpandedChange}
        onProjectUpdated={onProjectUpdated}
        onThreadUpdated={onThreadUpdated}
        onSelectThread={onSelectThread}
      />
    </div>
  )
}
