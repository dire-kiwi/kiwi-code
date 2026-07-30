import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { shallowEqual } from 'react-redux'
import { Link, useLocation, useMatch, useNavigate } from 'react-router-dom'
import {
  Activity,
  Bot,
  Braces,
  GitBranch,
  Globe2,
  LoaderCircle,
  Play,
  SquareTerminal,
} from 'lucide-react'
import { runEnvironmentAction, touchThreadTmuxActivity } from '@/api'
import {
  codingAgentSelectionForTarget,
  codingAgentTargetForSelection,
  configuredCodingAgentChoices,
} from '@/codingAgents'
import { WORKSPACE_ROUTE, workspacePath, workspaceToolFromRoute } from '@/app/routes'
import { newThreadStartFromState } from './newThreadStart'
import type {
  CodingAgentSelection,
  ConnectionStatus,
  PiPresentation,
  Project,
  Thread,
  WorkspaceTool,
  ThreadStatusSnapshot,
} from '@/types'
import { useAppDispatch, useAppSelector } from '@/store/hooks'
import {
  resolveThreadWorkspace,
  threadClaudePresentationChanged,
  threadCodingAgentChanged,
  threadPiPresentationChanged,
  threadWorkspaceKey,
  threadWorkspaceMounted,
  type ThreadWorkspaceRouting,
} from '@/store/slices/threadWorkspace'
import { selectSettings } from '@/store/slices/settings'
import { threadUpdated } from '@/store/slices/projects'
import {
  detailsSidebarExpandedChanged,
  selectDetailsSidebarExpanded,
  selectSidebarOpen,
  sidebarOpened,
} from '@/store/slices/ui'
import { useThreadUsage } from '@/wire/serverData'
import { threadActivityAcknowledged } from '@/store/thunks/agentActivity'
import {
  claudeContextStatusReported,
  claudePresentationStatusReported,
  piContextStatusReported,
  piPresentationStatusReported,
  processStarted,
  selectBranchOverlayOpen,
  selectClaudeContextStatus,
  selectClaudePresentationStatuses,
  selectPiContextStatus,
  selectPiPresentationStatuses,
  selectProcessWindows,
  selectRuntimeThreadKey,
  selectSelectedProcessId,
  selectToolStatuses,
  threadRuntimeKey,
  threadStatusSnapshotReceived,
  toolStatusReported,
  workspaceEntered,
} from '@/store/slices/threadWorkspaceRuntime'
import { Select } from '@/ui/inputs'
import { OpenSidebarButton } from '@/ui/buttons'
import { GitBranchBar } from './GitBranchBar'
import { ClaudeNativePane } from '@/features/workspace/panes/agent/ClaudeNativePane'
import { PiNativePane } from '@/features/workspace/panes/agent/PiNativePane'
import { ProcessWindowTabs } from './ProcessWindowTabs'
import { BrowserPane } from '@/features/workspace/panes/browser/BrowserPane'
import { TerminalSession } from '@/features/workspace/panes/TerminalSession'
import { ThreadProjectSidebar } from '@/features/workspace/details/ThreadProjectSidebar'
import { TmuxWindowTabs } from './TmuxWindowTabs'
import { useLastReadySubscriptionData, useSubscription } from '@/wire/react'
import { ThreadStatusTopic } from '@/wire/topics'

// project and thread stay props on purpose. App has to resolve them anyway --
// for the route guard that decides whether to render this at all, and for the
// key that remounts it on a thread change -- so passing them keeps the non-null
// contract explicit instead of re-deriving and re-asserting it here.
type TerminalWorkspaceProps = {
  project: Project
  thread: Thread
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
]

const statusCopy: Record<ConnectionStatus, string> = {
  connecting: 'Starting',
  open: 'Live',
  closed: 'Ended',
  error: 'Offline',
}

// A different thread always opens on this tool, matching what App did before.

const fallbackWorkspaceCodingAgents: Array<{ id: CodingAgentSelection; label: string }> = [
  { id: 'pi', label: 'Pi' },
  { id: 'pi-native', label: 'Pi Native' },
  { id: 'codex', label: 'Codex CLI' },
]

export function TerminalWorkspace({
  project,
  thread,
}: TerminalWorkspaceProps) {
  const navigate = useNavigate()
  const routeLocation = useLocation()
  const dispatch = useAppDispatch()
  const activeTool = workspaceToolFromRoute(useMatch(WORKSPACE_ROUTE)?.params.tool) ?? 'pi'
  const detailsExpanded = useAppSelector(selectDetailsSidebarExpanded)
  // The sidebar floats over the pane area, so the embedded browser surface
  // has to stand down while it is open.
  const nativeViewSuppressed = useAppSelector(selectSidebarOpen)
  const usageSnapshots = useThreadUsage()
  const usage = usageSnapshots.find((snapshot) =>
    snapshot.projectId === project.id && snapshot.threadId === thread.id)

  // The new-thread hand-off arrives in location state; unpacking it here rather
  // than in App is what removes seven props.
  const pendingStart = newThreadStartFromState(routeLocation.state)
  const threadStart = pendingStart
    && pendingStart.projectId === project.id
    && pendingStart.threadId === thread.id
    ? pendingStart
    : null
  const initialCodingAgent = threadStart?.agent
  const initialPresentation = threadStart?.presentation
  const initialModel = threadStart?.model
  const initialThinkingLevel = threadStart?.thinkingLevel
  const initialPrompt = threadStart?.prompt
  const initialImagePaths = threadStart?.imagePaths
  // Clearing the state stops the prompt being re-sent if the user navigates back.
  const onInitialPromptSent = threadStart
    ? () => {
        navigate({
          pathname: routeLocation.pathname,
          search: routeLocation.search,
          hash: routeLocation.hash,
        }, { replace: true, state: null })
      }
    : undefined
  const onThreadInteraction = () => {
    void dispatch(threadActivityAcknowledged({ projectId: project.id, threadId: thread.id }))
  }
  const onThreadUpdated = (updated: Thread) => {
    dispatch(threadUpdated({ projectId: project.id, thread: updated }))
  }
  const onOpenSidebar = () => dispatch(sidebarOpened())
  const onDetailsExpandedChange = (expanded: boolean) =>
    dispatch(detailsSidebarExpandedChanged(expanded))
  const statusSubscription = useSubscription(ThreadStatusTopic, {
    projectId: project.id,
    threadId: thread.id,
  })
  const settings = useAppSelector(selectSettings)
  const statusSnapshot = useLastReadySubscriptionData(statusSubscription) as ThreadStatusSnapshot | null
  const initialCodingAgentChoices = useMemo(() => {
    if (!initialCodingAgent || fallbackWorkspaceCodingAgents.some((agent) => agent.id === initialCodingAgent)) {
      return fallbackWorkspaceCodingAgents
    }
    return [
      { id: initialCodingAgent, label: initialCodingAgent === 'codex' ? 'Codex CLI' : 'Claude Code' },
      ...fallbackWorkspaceCodingAgents,
    ]
  }, [initialCodingAgent])
  const codingAgentChoices = useMemo(
    () => settings
      ? configuredCodingAgentChoices(settings.codingAgents)
      : initialCodingAgentChoices,
    [initialCodingAgentChoices, settings],
  )
  const workspaceKey = threadWorkspaceKey(project.id, thread.id)
  const routing = useMemo<ThreadWorkspaceRouting>(
    () => ({ initialCodingAgent, initialPresentation }),
    [initialCodingAgent, initialPresentation],
  )
  // Selecting through the same resolver the reducer uses keeps the first render
  // correct, so the panes below never seed themselves from the wrong presentation.
  const { codingAgent, piPresentation, claudePresentation } = useAppSelector(
    (state) => resolveThreadWorkspace(state.threadWorkspace.byThread[workspaceKey], routing),
    shallowEqual,
  )
  useEffect(() => {
    dispatch(threadWorkspaceMounted({ key: workspaceKey, routing }))
  }, [dispatch, routing, workspaceKey])
  const [piNativeOpened, setPiNativeOpened] = useState(() => piPresentation === 'native')
  const [piTerminalOpened, setPiTerminalOpened] = useState(() => piPresentation === 'terminal')
  const [claudeNativeOpened, setClaudeNativeOpened] = useState(() => claudePresentation === 'native')
  const [claudeTerminalOpened, setClaudeTerminalOpened] = useState(() => claudePresentation === 'terminal')
  const initialPiPresentationRef = useRef(piPresentation)
  const initialClaudePresentationRef = useRef(claudePresentation)
  const [openedTools, setOpenedTools] = useState<WorkspaceTool[]>(() => [activeTool])

  // Everything the tab bar, the branch bar and the details sidebar need to read
  // lives in the runtime slice, so none of it has to travel back up through here
  // on an onXChange prop. The reset on mount is what keeps a single un-keyed
  // slice correct; see the SINGLE MOUNT note in the slice.
  const threadKey = threadRuntimeKey(project.id, thread.id)
  const entryTool = useRef(activeTool)
  useEffect(() => {
    // Seeds the entry tool's status. Not re-run per tab change -- switching tabs
    // must not wipe the statuses the other panes have already reported.
    dispatch(workspaceEntered({ threadKey, activeTool: entryTool.current }))
  }, [dispatch, threadKey])
  const runtimeThreadKey = useAppSelector(selectRuntimeThreadKey)

  const statuses = useAppSelector(selectToolStatuses)
  const piPresentationStatuses = useAppSelector(selectPiPresentationStatuses)
  const claudePresentationStatuses = useAppSelector(selectClaudePresentationStatuses)
  const nativeContextStatus = useAppSelector(selectPiContextStatus)
  const claudeNativeContextStatus = useAppSelector(selectClaudeContextStatus)
  const processWindows = useAppSelector(selectProcessWindows)
  const selectedProcessId = useAppSelector(selectSelectedProcessId)
  const branchOverlayOpen = useAppSelector(selectBranchOverlayOpen)

  const [runningEnvironmentAction, setRunningEnvironmentAction] = useState<string | null>(null)
  const [environmentActionError, setEnvironmentActionError] = useState('')
  const [environmentSetupStatus, setEnvironmentSetupStatus] = useState(
    thread.environmentSetupStatus ?? 'succeeded',
  )
  const toolTabsRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    setEnvironmentSetupStatus(thread.environmentSetupStatus ?? 'succeeded')
  }, [thread.environmentSetupStatus])

  useEffect(() => {
    const controller = new AbortController()
    const touch = () => {
      void touchThreadTmuxActivity(project.id, thread.id, controller.signal).catch(() => {})
    }
    touch()
    const interval = window.setInterval(touch, 5 * 60 * 1000)
    return () => {
      window.clearInterval(interval)
      controller.abort()
    }
  }, [project.id, thread.id])

  useEffect(() => {
    if (codingAgentChoices.some((choice) => choice.id === codingAgent)) return
    dispatch(threadCodingAgentChanged({ key: workspaceKey, codingAgent: 'pi' }))
  }, [codingAgent, codingAgentChoices, dispatch, workspaceKey])

  const reportToolStatus = useCallback((tool: WorkspaceTool, status: ConnectionStatus) => {
    dispatch(toolStatusReported({ threadKey, tool, status }))
  }, [dispatch, threadKey])
  const reportPiPresentationStatus = useCallback(
    (presentation: PiPresentation, status: ConnectionStatus) => {
      dispatch(piPresentationStatusReported({ threadKey, presentation, status }))
    },
    [dispatch, threadKey],
  )
  const reportClaudePresentationStatus = useCallback(
    (presentation: PiPresentation, status: ConnectionStatus) => {
      dispatch(claudePresentationStatusReported({ threadKey, presentation, status }))
    },
    [dispatch, threadKey],
  )

  const markToolOpened = useCallback((tool: WorkspaceTool) => {
    setOpenedTools((current) => (current.includes(tool) ? current : [...current, tool]))
  }, [])

  const activateTool = useCallback((tool: WorkspaceTool) => {
    markToolOpened(tool)
    navigate(workspacePath(project.id, thread.id, tool))
  }, [dispatch, markToolOpened, navigate, project.id, thread.id])

  async function handleEnvironmentAction(actionId: string) {
    if (runningEnvironmentAction) return
    setRunningEnvironmentAction(actionId)
    setEnvironmentActionError('')
    try {
      const started = await runEnvironmentAction(project.id, thread.id, actionId)
      dispatch(processStarted({ threadKey, process: started }))
      activateTool('process')
    } catch (reason) {
      setEnvironmentActionError(reason instanceof Error ? reason.message : 'Could not run the environment action.')
    } finally {
      setRunningEnvironmentAction(null)
    }
  }

  function selectCodingAgent(selection: CodingAgentSelection) {
    const { agent, presentation } = codingAgentTargetForSelection(selection)
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
      dispatch(threadPiPresentationChanged({ key: workspaceKey, presentation }))
    }
    if (agent === 'claude') {
      if (presentation === 'native') setClaudeNativeOpened(true)
      if (presentation === 'terminal') setClaudeTerminalOpened(true)
      dispatch(threadClaudePresentationChanged({ key: workspaceKey, presentation }))
    }
    dispatch(threadCodingAgentChanged({ key: workspaceKey, codingAgent: agent }))
    reportToolStatus('pi', 'connecting')
    activateTool('pi')
  }

  useEffect(() => {
    markToolOpened(activeTool)
  }, [activeTool, dispatch, markToolOpened])

  useEffect(() => {
    if (!statusSnapshot) return
    dispatch(threadStatusSnapshotReceived({ threadKey, snapshot: statusSnapshot }))
  }, [dispatch, statusSnapshot, threadKey])

  const statusError = statusSubscription.state === 'error'
    ? statusSubscription.error.message
    : ''
  const statusLoading = statusSnapshot === null && statusSubscription.state === 'loading'
  const processesLoading = statusLoading
  const branchesLoading = statusLoading
  const shellWindowsLoading = statusLoading
  const processesError = statusError || statusSnapshot?.errors.processes || ''
  const branchesError = statusError || statusSnapshot?.errors.gitBranches || ''
  const shellWindowsError = statusError || statusSnapshot?.errors.shellWindows || ''
  const contextStatuses = statusSnapshot?.contextStatuses ?? {}

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
      : statusCopy[activeStatus]
  const sessionTools = openedTools.includes(activeTool)
    ? openedTools
    : [...openedTools, activeTool]
  const availableCodingAgents = codingAgentChoices
  const codingAgentSelection = codingAgentSelectionForTarget(
    codingAgent,
    codingAgent === 'claude'
      ? claudePresentation
      : codingAgent === 'pi'
        ? piPresentation
        : 'terminal',
  )
  const selectedCodingAgent = availableCodingAgents.find((agent) => agent.id === codingAgentSelection) ?? availableCodingAgents[0]
  const contextStatus = codingAgent === 'claude'
    ? claudePresentation === 'native' ? claudeNativeContextStatus : null
    : codingAgent !== 'pi'
      ? null
      : piPresentation === 'native'
        ? nativeContextStatus
        : contextStatuses['pi-terminal'] ?? null
  const hasSecondaryTabs = activeTool === 'terminal' || activeTool === 'process'

  // The runtime slice still holds the previous thread for the one render before
  // workspaceEntered commits. Rendering then would flash that thread's branch
  // name, process list and tab dots, so hold off for a frame instead.
  if (runtimeThreadKey !== threadKey) return null

  return (
    <div
      className={`relative flex h-full min-w-0 bg-ghost-black ${
        detailsExpanded ? 'thread-details-expanded' : ''
      }`}
      onKeyDownCapture={onThreadInteraction}
      onPointerDownCapture={onThreadInteraction}
      onWheelCapture={onThreadInteraction}
    >
      <div className="flex min-w-0 flex-1 flex-col">
        <header className={`shrink-0 bg-ghost-panel/95 ${hasSecondaryTabs ? 'border-b border-ghost-border/70' : ''}`}>
        <div className="desktop-titlebar-drag-region desktop-titlebar-workspace-right-safe relative flex min-h-[41px] items-center gap-3 pl-3 pr-12 md:px-3 lg:px-5">
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
                        onChange={(agent) => selectCodingAgent(agent as CodingAgentSelection)}
                        aria-label="Coding agent"
                        title="Choose coding agent"
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

          {project.environment.actions.length > 0 && (
            <div className="hidden max-w-[34%] shrink-0 items-center gap-1 overflow-x-auto md:flex" aria-label="Environment actions">
              {project.environment.actions.map((action) => (
                <button
                  key={action.id}
                  type="button"
                  disabled={runningEnvironmentAction !== null}
                  onClick={() => void handleEnvironmentAction(action.id)}
                  className="flex h-7 shrink-0 items-center gap-1.5 rounded-lg border border-ghost-border/70 bg-ghost-background/70 px-2.5 text-[9px] font-medium text-ghost-muted transition hover:border-ghost-green/40 hover:text-ghost-bright-white disabled:cursor-wait disabled:opacity-55"
                  title={`Run ${action.name} in a Process shell`}
                >
                  {runningEnvironmentAction === action.id
                    ? <LoaderCircle size={11} className="animate-spin text-ghost-green" />
                    : <Play size={10} className="text-ghost-green" />}
                  {action.name}
                </button>
              ))}
            </div>
          )}

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
        {project.environment.actions.length > 0 && (
          <div className="flex h-9 items-center gap-1 overflow-x-auto border-t border-ghost-border/55 bg-ghost-background px-3 md:hidden" aria-label="Environment actions">
            {project.environment.actions.map((action) => (
              <button
                key={action.id}
                type="button"
                disabled={runningEnvironmentAction !== null}
                onClick={() => void handleEnvironmentAction(action.id)}
                className="flex h-7 shrink-0 items-center gap-1.5 rounded-lg border border-ghost-border/70 bg-ghost-panel px-2.5 text-[9px] font-medium text-ghost-muted disabled:opacity-55"
              >
                {runningEnvironmentAction === action.id
                  ? <LoaderCircle size={11} className="animate-spin text-ghost-green" />
                  : <Play size={10} className="text-ghost-green" />}
                {action.name}
              </button>
            ))}
          </div>
        )}
        {environmentActionError && (
          <div className="border-t border-ghost-bright-red/20 bg-ghost-bright-red/10 px-4 py-1.5 text-center text-[9px] text-ghost-bright-red" role="alert">
            {environmentActionError}
          </div>
        )}
        {activeTool === 'terminal' && (
          <TmuxWindowTabs
            projectId={project.id}
            threadId={thread.id}
            loading={shellWindowsLoading}
            error={shellWindowsError}
            onRetry={statusSubscription.retry}
          />
        )}
        {activeTool === 'process' && (
          <ProcessWindowTabs
            loading={processesLoading}
            error={processesError}
            onRetry={statusSubscription.retry}
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
                    suppressed={nativeViewSuppressed || branchOverlayOpen}
                    onWorkspaceShortcut={(index) => {
                      const tool = tools[index - 1]
                      if (tool) activateTool(tool.id)
                    }}
                    onStatusChange={(status) => reportToolStatus('browser', status)}
                  />
                )
              }
              const processId = tool === 'process' ? selectedProcessId ?? undefined : undefined
              if (tool === 'process' && !processId) return null
              if (tool === 'pi' && environmentSetupStatus !== 'succeeded') {
                return (
                  <TerminalSession
                    key="pi:environment-setup"
                    projectId={project.id}
                    threadId={thread.id}
                    threadTitle={thread.title}
                    tool="pi"
                    codingAgent={codingAgent}
                    terminalLabel="environment setup"
                    environmentSetup
                    onEnvironmentSetupFinished={setEnvironmentSetupStatus}
                    active={activeTool === 'pi'}
                    onStatusChange={(status) => {
                      if (codingAgent === 'claude') reportClaudePresentationStatus(claudePresentation, status)
                      else if (codingAgent === 'pi') reportPiPresentationStatus(piPresentation, status)
                      else reportToolStatus('pi', status)
                    }}
                  />
                )
              }
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
                        onStatusChange={(status) => reportClaudePresentationStatus('native', status)}
                        onContextStatusChange={(status) =>
                          dispatch(claudeContextStatusReported({ threadKey, status }))
                        }
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
                        onStatusChange={(status) => reportClaudePresentationStatus('terminal', status)}
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
                        active={activeTool === 'pi' && piPresentation === 'native'}
                        onStatusChange={(status) => reportPiPresentationStatus('native', status)}
                        onContextStatusChange={(status) =>
                          dispatch(piContextStatusReported({ threadKey, status }))
                        }
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
                        onStatusChange={(status) => reportPiPresentationStatus('terminal', status)}
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
                  onStatusChange={(status) => reportToolStatus(tool, status)}
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
          </div>
        </div>
      </main>

        <GitBranchBar
          projectId={project.id}
          threadId={thread.id}
          worktree={thread.worktree}
          contextStatus={contextStatus}
          loading={branchesLoading}
          loadError={branchesError}
          onRetry={statusSubscription.retry}
        />
      </div>

      <ThreadProjectSidebar
        project={project}
        thread={thread}
        usage={usage}
        expanded={detailsExpanded}
        onExpandedChange={onDetailsExpandedChange}
        onThreadUpdated={onThreadUpdated}
      />
    </div>
  )
}
