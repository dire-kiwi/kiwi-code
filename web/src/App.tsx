import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Navigate, Route, Routes, useLocation, useMatch, useNavigate } from 'react-router-dom'
import {
  acknowledgePiThreadActivity,
  deleteProject,
  deleteThread,
  listProfiles,
  listProjects,
  setThreadArchived,
  setThreadBookmarked,
  updateProjectOrder,
  updateThreadOrder,
} from './api'
import { apiUrl } from './apiUrl'
import { WorkspaceLoadingState } from './components/molecules/WorkspaceLoadingState'
import { ProjectSidebar } from './components/organisms/ProjectSidebar'
import { ProjectThreadFinder } from './components/organisms/ProjectThreadFinder'
import { CleanupScreen } from './components/pages/CleanupScreen'
import { EmptyWorkspace } from './components/pages/EmptyWorkspace'
import { NewThreadScreen } from './components/pages/NewThreadScreen'
import { SettingsScreen } from './components/pages/SettingsScreen'
import { TmuxScreen } from './components/pages/TmuxScreen'
import { TerminalWorkspace } from './components/pages/TerminalWorkspace'
import {
  CLEANUP_ROUTE,
  NEW_THREAD_ROUTE,
  PROJECT_ROUTE,
  SETTINGS_ROUTE,
  THREAD_ROUTE,
  TMUX_ROUTE,
  WORKSPACE_ROUTE,
  newThreadPath,
  workspacePath,
  workspaceToolFromRoute,
} from './routes'
import { activityDisplayThreadId } from './sidebar-thread-activity.mjs'
import type {
  CodingAgentStart,
  PiThreadActivity,
  ProcessWebServer,
  Profile,
  Project,
  Thread,
  WorkspaceTool,
  ThreadUsageSnapshot,
} from './types'

const defaultWorkspaceTool: WorkspaceTool = 'pi'
const activeProfileStorageKey = 'kiwi-code-active-profile'

type NewThreadStart = CodingAgentStart & {
  kind: 'new-thread-start'
  projectId: string
  threadId: string
}

function newThreadStartFromState(state: unknown): NewThreadStart | null {
  if (!state || typeof state !== 'object') return null
  const candidate = state as Partial<NewThreadStart>
  const hasImagePaths = Array.isArray(candidate.imagePaths)
    && candidate.imagePaths.length > 0
    && candidate.imagePaths.every((path) => typeof path === 'string' && path.trim().length > 0)
  if (
    candidate.kind !== 'new-thread-start'
    || typeof candidate.projectId !== 'string'
    || typeof candidate.threadId !== 'string'
    || (candidate.agent !== 'pi' && candidate.agent !== 'claude' && candidate.agent !== 'claude-gpt')
    || (candidate.presentation !== undefined
      && candidate.presentation !== 'native'
      && candidate.presentation !== 'terminal')
    || (candidate.agent === 'claude-gpt'
      && candidate.presentation !== undefined
      && candidate.presentation !== 'terminal')
    || typeof candidate.model !== 'string'
    || typeof candidate.thinkingLevel !== 'string'
    || typeof candidate.prompt !== 'string'
    || (candidate.imagePaths !== undefined && !hasImagePaths)
    || (hasImagePaths && (
      (candidate.agent !== 'pi' && candidate.agent !== 'claude') || candidate.presentation !== 'native'
    ))
  ) {
    return null
  }
  return candidate as NewThreadStart
}

function piActivityKey(projectId: string, threadId: string) {
  return `${projectId}:${threadId}`
}

function samePiActivities(current: PiThreadActivity[], next: PiThreadActivity[]) {
  if (current.length !== next.length) return false
  return current.every((activity) => next.some((candidate) =>
    candidate.projectId === activity.projectId
      && candidate.threadId === activity.threadId
      && candidate.state === activity.state,
  ))
}

function visibleProjectSnapshots(items: Project[]) {
  return items.map((project) => {
    const threads = project.threads.filter((thread) => !thread.rollbackPending)
    return threads.length === project.threads.length ? project : { ...project, threads }
  })
}

function sameThreads(current: Thread[], next: Thread[]) {
  if (current.length !== next.length) return false
  return current.every((thread, index) => {
    const candidate = next[index]
    return candidate
      && candidate.id === thread.id
      && candidate.title === thread.title
      && candidate.cwd === thread.cwd
      && candidate.createdAt === thread.createdAt
      && candidate.lastPromptAt === thread.lastPromptAt
      && candidate.parentThreadId === thread.parentThreadId
      && candidate.agentModel === thread.agentModel
      && candidate.agentThinkingLevel === thread.agentThinkingLevel
      && candidate.workflowRunId === thread.workflowRunId
      && candidate.workflowAgentId === thread.workflowAgentId
      && candidate.worktree === thread.worktree
      && candidate.branch === thread.branch
      && candidate.worktreePath === thread.worktreePath
      && candidate.autoNamed === thread.autoNamed
      && candidate.closedAt === thread.closedAt
      && candidate.archivedAt === thread.archivedAt
      && candidate.bookmarked === thread.bookmarked
      && candidate.tokenLimit === thread.tokenLimit
      && candidate.costLimitUsd === thread.costLimitUsd
      && candidate.nestedDepth === thread.nestedDepth
      && candidate.rollbackPending === thread.rollbackPending
  })
}

function sameProfiles(current: Profile[], next: Profile[]) {
  if (current.length !== next.length) return false
  return current.every((profile, index) => {
    const candidate = next[index]
    return candidate && candidate.id === profile.id && candidate.name === profile.name
  })
}

function sameProjects(current: Project[], next: Project[]) {
  if (current.length !== next.length) return false
  return current.every((project, index) => {
    const candidate = next[index]
    return candidate
      && candidate.id === project.id
      && candidate.name === project.name
      && candidate.path === project.path
      && candidate.profileId === project.profileId
      && candidate.host === project.host
      && candidate.isGitRepo === project.isGitRepo
      && candidate.createdAt === project.createdAt
      && candidate.subAgentNestingDepthOverride === project.subAgentNestingDepthOverride
      && sameThreads(project.threads, candidate.threads)
  })
}

function projectsWithProfileOrder(current: Project[], profileId: string, projectIds: string[]) {
  const profileProjects = current.filter((project) => project.profileId === profileId)
  if (profileProjects.length !== projectIds.length || new Set(projectIds).size !== projectIds.length) return current

  const byId = new Map(profileProjects.map((project) => [project.id, project]))
  const ordered = projectIds.map((id) => byId.get(id))
  if (ordered.some((project) => !project)) return current

  let orderedIndex = 0
  return current.map((project) =>
    project.profileId === profileId ? ordered[orderedIndex++]! : project,
  )
}

function projectsWithThreadOrder(current: Project[], projectId: string, threadIds: string[]) {
  return current.map((project) => {
    if (project.id !== projectId || project.threads.length !== threadIds.length) return project
    if (new Set(threadIds).size !== threadIds.length) return project

    const byId = new Map(project.threads.map((thread) => [thread.id, thread]))
    const ordered = threadIds.map((id) => byId.get(id))
    if (ordered.some((thread) => !thread)) return project
    return { ...project, threads: ordered as Thread[] }
  })
}

type LastWorkspace = {
  projectId: string
  threadId: string
  tool: WorkspaceTool
}

function firstWorkspacePath(projects: Project[], preferredProjectId?: string): string | null {
  const preferredProject = preferredProjectId
    ? projects.find((project) => project.id === preferredProjectId)
    : undefined
  const preferredActiveThread = preferredProject?.threads.find((thread) => !thread.parentThreadId && !thread.archivedAt)
  if (preferredProject && preferredActiveThread) {
    return workspacePath(preferredProject.id, preferredActiveThread.id, defaultWorkspaceTool)
  }

  const activeProject = projects.find((project) => project.threads.some((thread) => !thread.parentThreadId && !thread.archivedAt))
  const activeThread = activeProject?.threads.find((thread) => !thread.parentThreadId && !thread.archivedAt)
  if (activeProject && activeThread) {
    return workspacePath(activeProject.id, activeThread.id, defaultWorkspaceTool)
  }

  const project = preferredProject?.threads.length
    ? preferredProject
    : projects.find((item) => item.threads.length > 0)
  const thread = project?.threads.find((candidate) => !candidate.parentThreadId) ?? project?.threads[0]
  return project && thread ? workspacePath(project.id, thread.id, defaultWorkspaceTool) : null
}

function rememberedWorkspacePath(projects: Project[], lastWorkspace: LastWorkspace | null): string | null {
  if (!lastWorkspace) return null
  const project = projects.find((item) => item.id === lastWorkspace.projectId)
  const thread = project?.threads.find((item) => item.id === lastWorkspace.threadId)
  return project && thread
    ? workspacePath(project.id, thread.id, lastWorkspace.tool)
    : null
}

export default function App() {
  const navigate = useNavigate()
  const routeLocation = useLocation()
  const workspaceMatch = useMatch(WORKSPACE_ROUTE)
  const newThreadMatch = useMatch(NEW_THREAD_ROUTE)
  const threadMatch = useMatch(THREAD_ROUTE)
  const projectMatch = useMatch(PROJECT_ROUTE)
  const cleanupMatch = useMatch(CLEANUP_ROUTE)
  const settingsMatch = useMatch(SETTINGS_ROUTE)
  const tmuxMatch = useMatch(TMUX_ROUTE)

  const [profiles, setProfiles] = useState<Profile[]>([])
  const [activeProfileId, setActiveProfileId] = useState(() => {
    try {
      return window.localStorage.getItem(activeProfileStorageKey) || 'personal'
    } catch {
      return 'personal'
    }
  })
  const [projects, setProjects] = useState<Project[]>([])
  const [projectsLoading, setProjectsLoading] = useState(true)
  const [profilesLoading, setProfilesLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [deletingId, setDeletingId] = useState<string | null>(null)
  const [deletingThreadId, setDeletingThreadId] = useState<string | null>(null)
  const [archivingThreadId, setArchivingThreadId] = useState<string | null>(null)
  const [bookmarkingThreadId, setBookmarkingThreadId] = useState<string | null>(null)
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const [projectFinderOpen, setProjectFinderOpen] = useState(false)
  const [detailsSidebarExpanded, setDetailsSidebarExpanded] = useState(false)
  const [piActivities, setPiActivities] = useState<PiThreadActivity[]>([])
  const [processWebServers, setProcessWebServers] = useState<ProcessWebServer[]>([])
  const [usageSnapshots, setUsageSnapshots] = useState<ThreadUsageSnapshot[]>([])
  const lastWorkspacesRef = useRef<Record<string, LastWorkspace>>({})
  const piActivitiesRef = useRef<PiThreadActivity[]>([])
  const projectsRef = useRef<Project[]>(projects)
  projectsRef.current = projects
  const pendingPiAcknowledgementsRef = useRef(new Set<string>())
  const activeThreadIdentityRef = useRef<string | null>(null)
  const previousActiveThreadRef = useRef<string | null>(null)

  const workspaceProjectId = workspaceMatch?.params.projectId
  const workspaceThreadId = workspaceMatch?.params.threadId
  const activeTool = workspaceToolFromRoute(workspaceMatch?.params.tool)

  const queuePiAcknowledgement = useCallback((projectId: string, threadId: string) => {
    const key = piActivityKey(projectId, threadId)
    if (pendingPiAcknowledgementsRef.current.has(key)) return

    pendingPiAcknowledgementsRef.current.add(key)
    const nextActivities = piActivitiesRef.current.filter((item) =>
      item.projectId !== projectId || item.threadId !== threadId,
    )
    piActivitiesRef.current = nextActivities
    setPiActivities(nextActivities)
    void acknowledgePiThreadActivity(projectId, threadId)
      .catch(() => {})
      .finally(() => pendingPiAcknowledgementsRef.current.delete(key))
  }, [])

  const acknowledgeThreadActivity = useCallback((projectId: string, threadId: string) => {
    const project = projectsRef.current.find((item) => item.id === projectId)
    if (!project) return
    const activities = piActivitiesRef.current.filter((activity) =>
      activity.projectId === projectId
        && activity.state === 'finished'
        && activityDisplayThreadId(project.threads, activity) === threadId,
    )
    for (const activity of activities) queuePiAcknowledgement(projectId, activity.threadId)
  }, [queuePiAcknowledgement])

  const applyPiActivities = useCallback((nextActivities: PiThreadActivity[]) => {
    const pending = pendingPiAcknowledgementsRef.current
    const visibleActivities = nextActivities.filter((activity) =>
      activity.state === 'working' || !pending.has(piActivityKey(activity.projectId, activity.threadId)),
    )
    if (!samePiActivities(piActivitiesRef.current, visibleActivities)) {
      piActivitiesRef.current = visibleActivities
      setPiActivities(visibleActivities)
    }

    const activeFinished = visibleActivities.filter((activity) => {
      if (activity.state !== 'finished') return false
      const project = projectsRef.current.find((item) => item.id === activity.projectId)
      if (!project) return false
      const displayThreadId = activityDisplayThreadId(project.threads, activity)
      return piActivityKey(activity.projectId, displayThreadId) === activeThreadIdentityRef.current
    })
    for (const activity of activeFinished) {
      queuePiAcknowledgement(activity.projectId, activity.threadId)
    }
  }, [queuePiAcknowledgement])

  useEffect(() => {
    function handleProjectFinderShortcut(event: KeyboardEvent) {
      if (!event.ctrlKey || event.metaKey || event.altKey || event.shiftKey || event.key.toLowerCase() !== 'f') return
      event.preventDefault()
      event.stopPropagation()
      setProjectFinderOpen(true)
    }

    window.addEventListener('keydown', handleProjectFinderShortcut, true)
    return () => window.removeEventListener('keydown', handleProjectFinderShortcut, true)
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    const events = new EventSource(apiUrl('/api/events'))
    let receivedProjectSnapshot = false
    let receivedProfileSnapshot = false

    function applyProjects(items: Project[]) {
      const visibleItems = visibleProjectSnapshots(items)
      setProjects((current) => sameProjects(current, visibleItems) ? current : visibleItems)
      setLoadError('')
      setProjectsLoading(false)
    }

    function applyProfiles(items: Profile[]) {
      setProfiles((current) => sameProfiles(current, items) ? current : items)
      setLoadError('')
      setProfilesLoading(false)
    }

    function handleProjects(event: Event) {
      try {
        const items: unknown = JSON.parse((event as MessageEvent<string>).data)
        if (!Array.isArray(items)) return
        receivedProjectSnapshot = true
        applyProjects(items as Project[])
      } catch {
        // Ignore a malformed event; EventSource keeps listening for the next snapshot.
      }
    }

    function handleProfiles(event: Event) {
      try {
        const items: unknown = JSON.parse((event as MessageEvent<string>).data)
        if (!Array.isArray(items)) return
        receivedProfileSnapshot = true
        applyProfiles(items as Profile[])
      } catch {
        // Ignore a malformed event; EventSource keeps listening for the next snapshot.
      }
    }

    function handlePiActivity(event: Event) {
      try {
        const activities: unknown = JSON.parse((event as MessageEvent<string>).data)
        if (Array.isArray(activities)) applyPiActivities(activities as PiThreadActivity[])
      } catch {
        // Ignore a malformed event; EventSource keeps listening for the next snapshot.
      }
    }

    function handleThreadUsage(event: Event) {
      try {
        const snapshots: unknown = JSON.parse((event as MessageEvent<string>).data)
        if (Array.isArray(snapshots)) setUsageSnapshots(snapshots as ThreadUsageSnapshot[])
      } catch {
        // Ignore a malformed event; EventSource keeps listening for the next snapshot.
      }
    }

    function handleProcesses(event: Event) {
      try {
        const webServers: unknown = JSON.parse((event as MessageEvent<string>).data)
        if (Array.isArray(webServers)) setProcessWebServers(webServers as ProcessWebServer[])
      } catch {
        // Ignore a malformed event; EventSource keeps listening for the next snapshot.
      }
    }

    events.addEventListener('projects', handleProjects)
    events.addEventListener('profiles', handleProfiles)
    events.addEventListener('pi-activity', handlePiActivity)
    events.addEventListener('thread-usage', handleThreadUsage)
    events.addEventListener('processes', handleProcesses)

    listProjects(controller.signal)
      .then((items) => {
        if (!receivedProjectSnapshot) applyProjects(Array.isArray(items) ? items : [])
      })
      .catch((reason) => {
        if (controller.signal.aborted || receivedProjectSnapshot) return
        setLoadError(reason instanceof Error ? reason.message : 'Could not load projects.')
      })
      .finally(() => {
        if (!controller.signal.aborted) setProjectsLoading(false)
      })

    listProfiles(controller.signal)
      .then((items) => {
        if (!receivedProfileSnapshot) applyProfiles(Array.isArray(items) ? items : [])
      })
      .catch((reason) => {
        if (controller.signal.aborted || receivedProfileSnapshot) return
        setLoadError(reason instanceof Error ? reason.message : 'Could not load profiles.')
      })
      .finally(() => {
        if (!controller.signal.aborted) setProfilesLoading(false)
      })

    return () => {
      controller.abort()
      events.close()
    }
  }, [applyPiActivities])

  const selectedProject = useMemo(
    () => projects.find((project) => project.id === workspaceProjectId) ?? null,
    [projects, workspaceProjectId],
  )

  const selectedThread = useMemo(
    () => selectedProject?.threads.find((thread) => thread.id === workspaceThreadId) ?? null,
    [selectedProject, workspaceThreadId],
  )

  const pendingThreadStart = newThreadStartFromState(routeLocation.state)
  const initialThreadStart = pendingThreadStart
    && pendingThreadStart.projectId === selectedProject?.id
    && pendingThreadStart.threadId === selectedThread?.id
    ? pendingThreadStart
    : null

  const newThreadProject = useMemo(
    () => projects.find((project) => project.id === newThreadMatch?.params.projectId) ?? null,
    [newThreadMatch?.params.projectId, projects],
  )

  const legacyProject = useMemo(
    () => projects.find((project) => project.id === threadMatch?.params.projectId) ?? null,
    [projects, threadMatch?.params.projectId],
  )

  const legacyThread = useMemo(
    () => legacyProject?.threads.find((thread) => thread.id === threadMatch?.params.threadId) ?? null,
    [legacyProject, threadMatch?.params.threadId],
  )

  const landingProject = useMemo(
    () => projects.find((project) => project.id === projectMatch?.params.projectId) ?? null,
    [projectMatch?.params.projectId, projects],
  )

  const activeProfile = useMemo(
    () => profiles.find((profile) => profile.id === activeProfileId) ?? profiles[0] ?? null,
    [activeProfileId, profiles],
  )
  const activeProjects = useMemo(
    () => projects.filter((project) => project.profileId === activeProfileId),
    [activeProfileId, projects],
  )
  const activeProcessWebServers = useMemo(() => {
    const projectIds = new Set(activeProjects.map((project) => project.id))
    return processWebServers.filter((webServer) => projectIds.has(webServer.projectId))
  }, [activeProjects, processWebServers])
  const defaultWorkspacePath = useMemo(() => firstWorkspacePath(activeProjects), [activeProjects])
  const routedProject = selectedProject ?? newThreadProject ?? legacyProject ?? landingProject
  const routedProfileId = routedProject?.profileId
  const routedProjectId = routedProject?.id ?? null
  const newThreadProjectId = newThreadProject?.id ?? null
  const activeThreadIdentity = selectedProject && selectedThread
    ? piActivityKey(selectedProject.id, selectedThread.id)
    : null
  const loading = projectsLoading || profilesLoading

  useEffect(() => {
    if (profilesLoading || profiles.length === 0 || profiles.some((profile) => profile.id === activeProfileId)) return
    setActiveProfileId(profiles[0].id)
  }, [activeProfileId, profiles, profilesLoading])

  useEffect(() => {
    if (!routedProfileId) return
    setActiveProfileId((current) => current === routedProfileId ? current : routedProfileId)
  }, [routedProfileId])

  useEffect(() => {
    if (!routedProjectId) return
    const projectId = routedProjectId

    function handleNewThreadShortcut(event: KeyboardEvent) {
      if (!event.ctrlKey || event.metaKey || event.altKey || event.shiftKey || event.key.toLowerCase() !== 'n') return
      event.preventDefault()
      event.stopPropagation()
      if (event.repeat || newThreadProjectId === projectId) return

      navigate(newThreadPath(projectId))
      setSidebarOpen(false)
      setProjectFinderOpen(false)
    }

    window.addEventListener('keydown', handleNewThreadShortcut, true)
    return () => window.removeEventListener('keydown', handleNewThreadShortcut, true)
  }, [navigate, newThreadProjectId, routedProjectId])

  useEffect(() => {
    try {
      window.localStorage.setItem(activeProfileStorageKey, activeProfileId)
    } catch {
      // Profile selection still works when browser storage is unavailable.
    }
  }, [activeProfileId])

  useEffect(() => {
    activeThreadIdentityRef.current = activeThreadIdentity
    if (activeThreadIdentity && previousActiveThreadRef.current !== activeThreadIdentity && selectedProject && selectedThread) {
      acknowledgeThreadActivity(selectedProject.id, selectedThread.id)
    }
    previousActiveThreadRef.current = activeThreadIdentity
  }, [acknowledgeThreadActivity, activeThreadIdentity, selectedProject, selectedThread])

  useEffect(() => {
    if (!selectedProject || !selectedThread || !activeTool) return
    lastWorkspacesRef.current[selectedProject.profileId] = {
      projectId: selectedProject.id,
      threadId: selectedThread.id,
      tool: activeTool,
    }
  }, [activeTool, selectedProject, selectedThread])

  function workspaceReturnDestination(preferredProjectId?: string) {
    return rememberedWorkspacePath(activeProjects, lastWorkspacesRef.current[activeProfileId] ?? null)
      ?? firstWorkspacePath(activeProjects, preferredProjectId)
      ?? '/'
  }

  function handleThreadSelected(projectId: string, threadId: string) {
    acknowledgeThreadActivity(projectId, threadId)
    const tool = selectedProject?.id === projectId && selectedThread?.id === threadId && activeTool
      ? activeTool
      : defaultWorkspaceTool
    navigate(workspacePath(projectId, threadId, tool))
    setSidebarOpen(false)
    setProjectFinderOpen(false)
  }

  function handleFinderProjectSelected(project: Project) {
    const thread = project.threads.find((item) => !item.parentThreadId && !item.archivedAt)
      ?? project.threads.find((item) => !item.parentThreadId)
      ?? project.threads[0]
    if (thread) {
      handleThreadSelected(project.id, thread.id)
      return
    }
    navigate(newThreadPath(project.id))
    setSidebarOpen(false)
    setProjectFinderOpen(false)
  }

  function handleProfileSelected(profileId: string) {
    if (!profiles.some((profile) => profile.id === profileId)) return
    setActiveProfileId(profileId)
    setSidebarOpen(false)
    if (cleanupMatch || settingsMatch || tmuxMatch) return

    const profileProjects = projects.filter((project) => project.profileId === profileId)
    const destination = rememberedWorkspacePath(profileProjects, lastWorkspacesRef.current[profileId] ?? null)
      ?? firstWorkspacePath(profileProjects)
      ?? '/'
    navigate(destination)
  }

  function handleProfileCreated(profile: Profile) {
    setProfiles((current) => current.some((item) => item.id === profile.id)
      ? current
      : [...current, profile])
    setActiveProfileId(profile.id)
    setSidebarOpen(false)
    if (!cleanupMatch && !settingsMatch && !tmuxMatch) navigate('/')
  }

  function handleCreated(project: Project) {
    setProjects((current) => current.some((item) => item.id === project.id)
      ? current
      : [...current, project])
    setSidebarOpen(false)
    const thread = project.threads.find((item) => !item.parentThreadId) ?? project.threads[0]
    navigate(thread ? workspacePath(project.id, thread.id, defaultWorkspaceTool) : newThreadPath(project.id))
  }

  function handleThreadCreated(
    projectId: string,
    thread: Thread,
    start: CodingAgentStart,
  ) {
    setProjects((current) => current.map((project) => {
      if (project.id !== projectId || project.threads.some((item) => item.id === thread.id)) return project
      return {
        ...project,
        threads: [thread, ...project.threads],
      }
    }))
    setSidebarOpen(false)
    navigate(workspacePath(projectId, thread.id, 'pi'), {
      replace: true,
      state: {
        kind: 'new-thread-start',
        projectId,
        threadId: thread.id,
        ...start,
      } satisfies NewThreadStart,
    })
  }

  function handleThreadUpdated(projectId: string, updatedThread: Thread) {
    setProjects((current) => current.map((project) =>
      project.id === projectId
        ? {
            ...project,
            threads: project.threads.map((thread) =>
              thread.id === updatedThread.id ? updatedThread : thread,
            ),
          }
        : project,
    ))
  }

  function handleProjectUpdated(updatedProject: Project) {
    const visibleProject = visibleProjectSnapshots([updatedProject])[0]
    setProjects((current) => current.map((project) =>
      project.id === visibleProject.id ? visibleProject : project,
    ))
    if (selectedProject?.id === updatedProject.id) {
      setActiveProfileId(updatedProject.profileId)
    }
  }

  async function handleProjectsReordered(projectIds: string[]) {
    const profileId = activeProfileId
    const previousProjectIds = projects
      .filter((project) => project.profileId === profileId)
      .map((project) => project.id)
    setProjects((current) => projectsWithProfileOrder(current, profileId, projectIds))
    try {
      await updateProjectOrder(profileId, projectIds)
    } catch (reason) {
      try {
        setProjects(visibleProjectSnapshots(await listProjects()))
      } catch {
        setProjects((current) => projectsWithProfileOrder(current, profileId, previousProjectIds))
      }
      window.alert(reason instanceof Error ? reason.message : 'Could not save the project order.')
    }
  }

  async function handleThreadsReordered(projectId: string, threadIds: string[]) {
    const previousThreadIds = projects
      .find((project) => project.id === projectId)
      ?.threads.map((thread) => thread.id) ?? []
    setProjects((current) => projectsWithThreadOrder(current, projectId, threadIds))
    try {
      await updateThreadOrder(projectId, threadIds)
    } catch (reason) {
      try {
        setProjects(visibleProjectSnapshots(await listProjects()))
      } catch {
        setProjects((current) => projectsWithThreadOrder(current, projectId, previousThreadIds))
      }
      window.alert(reason instanceof Error ? reason.message : 'Could not save the thread order.')
    }
  }

  async function handleDelete(project: Project) {
    if (deletingId || !window.confirm(`Remove “${project.name}” from Kiwi Code?\n\nIts tmux sessions and running tools will be stopped. The project folder will not be deleted. Clean managed worktrees may be removed later according to automatic cleanup settings; their Git branches will remain.`)) {
      return
    }

    setDeletingId(project.id)
    try {
      await deleteProject(project.id)
      setProjects((current) => current.filter((item) => item.id !== project.id))
    } catch (reason) {
      window.alert(reason instanceof Error ? reason.message : 'Could not remove that project.')
    } finally {
      setDeletingId(null)
    }
  }

  async function handleThreadArchived(project: Project, thread: Thread, archived: boolean) {
    if (archivingThreadId) return
    setArchivingThreadId(thread.id)
    try {
      const updated = await setThreadArchived(project.id, thread.id, archived)
      handleThreadUpdated(project.id, updated)
      if (archived && selectedProject?.id === project.id && selectedThread?.id === thread.id) {
        const nextThread = project.threads.find((candidate) => candidate.id !== thread.id && !candidate.parentThreadId && !candidate.archivedAt)
        navigate(nextThread
          ? workspacePath(project.id, nextThread.id, defaultWorkspaceTool)
          : newThreadPath(project.id))
      }
    } catch (reason) {
      window.alert(reason instanceof Error ? reason.message : `Could not ${archived ? 'archive' : 'restore'} that thread.`)
    } finally {
      setArchivingThreadId(null)
    }
  }

  async function handleThreadBookmarked(project: Project, thread: Thread, bookmarked: boolean) {
    if (bookmarkingThreadId) return
    setBookmarkingThreadId(thread.id)
    try {
      const updated = await setThreadBookmarked(project.id, thread.id, bookmarked)
      setProjects((current) => current.map((candidateProject) =>
        candidateProject.id === project.id
          ? {
              ...candidateProject,
              threads: candidateProject.threads.map((candidateThread) =>
                candidateThread.id === thread.id
                  ? { ...candidateThread, bookmarked: updated.bookmarked }
                  : candidateThread,
              ),
            }
          : candidateProject,
      ))
    } catch (reason) {
      window.alert(reason instanceof Error ? reason.message : `Could not ${bookmarked ? 'bookmark' : 'remove the bookmark from'} that thread.`)
    } finally {
      setBookmarkingThreadId(null)
    }
  }

  async function handleDeleteThread(project: Project, thread: Thread) {
    const descendantIds = new Set<string>()
    const collectDescendants = (parentId: string) => {
      for (const candidate of project.threads) {
        if (candidate.parentThreadId !== parentId || descendantIds.has(candidate.id)) continue
        descendantIds.add(candidate.id)
        collectDescendants(candidate.id)
      }
    }
    collectDescendants(thread.id)
    const childNotice = descendantIds.size > 0
      ? `\n${descendantIds.size} agent ${descendantIds.size === 1 ? 'thread' : 'threads'} will also be deleted.`
      : ''
    const deletedThreadIds = new Set(descendantIds).add(thread.id)
    const worktreeCount = project.threads.filter((candidate) =>
      deletedThreadIds.has(candidate.id) && candidate.worktree,
    ).length
    const worktreeNotice = worktreeCount === 1
      ? '\nIts managed worktree will become unattached. If it stays clean, automatic cleanup may remove it later; its Git branch will remain.'
      : worktreeCount > 1
        ? `\n${worktreeCount} managed worktrees will become unattached. Clean worktrees may be removed later; their Git branches will remain.`
        : ''
    if (deletingThreadId || !window.confirm(`Delete “${thread.title}”?\n\nIts tmux sessions and running tools will be stopped.${childNotice}${worktreeNotice}`)) return

    setDeletingThreadId(thread.id)
    try {
      await deleteThread(project.id, thread.id)
      descendantIds.add(thread.id)
      setProjects((current) => current.map((item) =>
        item.id === project.id
          ? { ...item, threads: item.threads.filter((candidate) => !descendantIds.has(candidate.id)) }
          : item,
      ))
    } catch (reason) {
      window.alert(reason instanceof Error ? reason.message : 'Could not remove that thread.')
    } finally {
      setDeletingThreadId(null)
    }
  }

  const invalidWorkspaceDestination = selectedProject && selectedThread
    ? workspacePath(selectedProject.id, selectedThread.id, defaultWorkspaceTool)
    : defaultWorkspacePath ?? '/'
  const legacyDestination = legacyProject && legacyThread
    ? workspacePath(legacyProject.id, legacyThread.id, defaultWorkspaceTool)
    : defaultWorkspacePath ?? '/'
  const landingThread = landingProject?.threads.find((thread) => !thread.parentThreadId && !thread.archivedAt)
    ?? landingProject?.threads.find((thread) => !thread.parentThreadId)
    ?? landingProject?.threads[0]
  const projectDestination = landingProject
    ? landingThread
      ? workspacePath(landingProject.id, landingThread.id, defaultWorkspaceTool)
      : newThreadPath(landingProject.id)
    : defaultWorkspacePath ?? '/'

  return (
    <div className="flex h-dvh min-h-[32rem] overflow-hidden bg-ghost-black text-ghost-bright-white antialiased">
      <ProjectSidebar
        profiles={profiles}
        activeProfileId={activeProfileId}
        projects={activeProjects}
        piActivities={piActivities}
        processWebServers={activeProcessWebServers}
        usageSnapshots={usageSnapshots}
        selectedThreadId={selectedThread?.id ?? null}
        deletingProjectId={deletingId}
        deletingThreadId={deletingThreadId}
        archivingThreadId={archivingThreadId}
        bookmarkingThreadId={bookmarkingThreadId}
        cleanupSelected={Boolean(cleanupMatch)}
        tmuxSelected={Boolean(tmuxMatch)}
        settingsSelected={Boolean(settingsMatch)}
        isOpen={sidebarOpen}
        onClose={() => setSidebarOpen(false)}
        onOpenFinder={() => setProjectFinderOpen(true)}
        onSelectProfile={handleProfileSelected}
        onProfileCreated={handleProfileCreated}
        onSelectThread={handleThreadSelected}
        onNewThread={(projectId) => {
          navigate(newThreadPath(projectId))
          setSidebarOpen(false)
        }}
        onOpenCleanup={() => {
          navigate(CLEANUP_ROUTE)
          setSidebarOpen(false)
        }}
        onOpenTmux={() => {
          navigate(TMUX_ROUTE)
          setSidebarOpen(false)
        }}
        onOpenSettings={() => {
          navigate(SETTINGS_ROUTE)
          setSidebarOpen(false)
        }}
        onProjectCreated={handleCreated}
        onReorderProjects={handleProjectsReordered}
        onReorderThreads={handleThreadsReordered}
        onDeleteProject={handleDelete}
        onArchiveThread={(project, thread, archived) => void handleThreadArchived(project, thread, archived)}
        onBookmarkThread={(project, thread, bookmarked) => void handleThreadBookmarked(project, thread, bookmarked)}
        onDeleteThread={(project, thread) => void handleDeleteThread(project, thread)}
      />

      <div className="min-w-0 flex-1">
        {loading ? (
          <WorkspaceLoadingState />
        ) : (
          <Routes>
            <Route
              path={CLEANUP_ROUTE}
              element={(
                <CleanupScreen
                  onOpenSidebar={() => setSidebarOpen(true)}
                  onBack={() => navigate(workspaceReturnDestination(), { replace: true })}
                />
              )}
            />
            <Route
              path={TMUX_ROUTE}
              element={(
                <TmuxScreen
                  onOpenSidebar={() => setSidebarOpen(true)}
                  onBack={() => navigate(workspaceReturnDestination(), { replace: true })}
                />
              )}
            />
            <Route
              path={SETTINGS_ROUTE}
              element={(
                <SettingsScreen
                  onOpenSidebar={() => setSidebarOpen(true)}
                  onBack={() => navigate(workspaceReturnDestination(), { replace: true })}
                />
              )}
            />
            <Route
              path={NEW_THREAD_ROUTE}
              element={newThreadProject ? (
                <NewThreadScreen
                  key={newThreadProject.id}
                  project={newThreadProject}
                  onOpenSidebar={() => setSidebarOpen(true)}
                  onCancel={() => navigate(workspaceReturnDestination(newThreadProject.id), { replace: true })}
                  onCreated={(thread, start) =>
                    handleThreadCreated(newThreadProject.id, thread, start)}
                />
              ) : (
                <Navigate to={defaultWorkspacePath ?? '/'} replace />
              )}
            />
            <Route
              path={WORKSPACE_ROUTE}
              element={selectedProject && selectedThread && activeTool ? (
                <TerminalWorkspace
                  key={`${selectedProject.id}:${selectedThread.id}`}
                  profiles={profiles}
                  project={selectedProject}
                  thread={selectedThread}
                  usage={usageSnapshots.find((snapshot) =>
                    snapshot.projectId === selectedProject.id && snapshot.threadId === selectedThread.id)}
                  activeTool={activeTool}
                  detailsExpanded={detailsSidebarExpanded}
                  nativeViewSuppressed={sidebarOpen || projectFinderOpen}
                  onDetailsExpandedChange={setDetailsSidebarExpanded}
                  onOpenSidebar={() => setSidebarOpen(true)}
                  onThreadInteraction={() => acknowledgeThreadActivity(selectedProject.id, selectedThread.id)}
                  onProjectUpdated={handleProjectUpdated}
                  onThreadUpdated={(thread) => handleThreadUpdated(selectedProject.id, thread)}
                  onSelectThread={(thread) => handleThreadSelected(selectedProject.id, thread.id)}
                  initialCodingAgent={initialThreadStart?.agent}
                  initialPresentation={initialThreadStart?.presentation}
                  initialModel={initialThreadStart?.model}
                  initialThinkingLevel={initialThreadStart?.thinkingLevel}
                  initialPrompt={initialThreadStart?.prompt}
                  initialImagePaths={initialThreadStart?.imagePaths}
                  onInitialPromptSent={initialThreadStart ? () => {
                    navigate({
                      pathname: routeLocation.pathname,
                      search: routeLocation.search,
                      hash: routeLocation.hash,
                    }, { replace: true, state: null })
                  } : undefined}
                />
              ) : (
                <Navigate to={invalidWorkspaceDestination} replace />
              )}
            />
            <Route path={THREAD_ROUTE} element={<Navigate to={legacyDestination} replace />} />
            <Route path={PROJECT_ROUTE} element={<Navigate to={projectDestination} replace />} />
            <Route
              path="/"
              element={defaultWorkspacePath ? (
                <Navigate to={defaultWorkspacePath} replace />
              ) : (
                <EmptyWorkspace
                  loadError={loadError}
                  projectCount={activeProjects.length}
                  profileName={activeProfile?.name ?? 'this profile'}
                  onOpenSidebar={() => setSidebarOpen(true)}
                />
              )}
            />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        )}
      </div>

      {projectFinderOpen && (
        <ProjectThreadFinder
          profiles={profiles}
          projects={projects}
          currentProjectId={selectedProject?.id ?? null}
          currentThreadId={selectedThread?.id ?? null}
          onClose={() => setProjectFinderOpen(false)}
          onSelectProject={handleFinderProjectSelected}
          onSelectThread={(project, thread) => handleThreadSelected(project.id, thread.id)}
        />
      )}
    </div>
  )
}
