import { useCallback, useEffect, useMemo, useRef } from 'react'
import { Navigate, Route, Routes, useMatch, useNavigate } from 'react-router-dom'
import { piActivityKey } from '@/pi-activity-reconciliation.mjs'
import { WorkspaceLoadingState } from './WorkspaceLoadingState'
import { ProjectSidebar } from '@/features/project-sidebar/ProjectSidebar'
import { CleanupScreen } from '@/features/screens/CleanupScreen'
import { EmptyWorkspace } from '@/features/screens/EmptyWorkspace'
import { NewThreadScreen } from '@/features/new-thread/NewThreadScreen'
import { SandboxSettingsScreen } from '@/features/settings/SandboxSettingsScreen'
import { SessionLogScreen } from '@/features/screens/SessionLogScreen'
import { SettingsShell } from '@/features/settings/SettingsShell'
import {
  DEFAULT_GLOBAL_SETTINGS_SECTION,
  DEFAULT_PROJECT_SETTINGS_SECTION,
} from '@/features/settings/registry'
import { TmuxScreen } from '@/features/screens/TmuxScreen'
import { TerminalWorkspace } from '@/features/workspace/TerminalWorkspace'
import {
  CLEANUP_ROUTE,
  NEW_THREAD_ROUTE,
  PROJECT_ROUTE,
  PROJECT_SETTINGS_ROUTE,
  PROJECT_SETTINGS_SECTION_ROUTE,
  SESSION_LOG_ROUTE,
  SETTINGS_ROUTE,
  SETTINGS_SECTION_ROUTE,
  THREAD_ROUTE,
  THREAD_SANDBOX_ROUTE,
  TMUX_ROUTE,
  WORKSPACE_ROUTE,
  newThreadPath,
  projectSettingsPath,
  settingsPath,
  workspacePath,
  workspaceToolFromRoute,
} from './routes'
import { useAppDispatch, useAppSelector } from '@/store/hooks'
import { selectActiveProjects, selectThreadIndex } from '@/store/selectors/workspace'
import { profileCreated, selectProfiles, selectProfilesHydrated } from '@/store/slices/profiles'
import {
  projectCreated,
  projectRemoved,
  projectUpdated,
  selectArchivingThreadId,
  selectDeletingProjectId,
  selectDeletingThreadId,
  selectProjects,
  selectProjectsHydrated,
  threadArchived,
  threadCreated,
  threadRemoved,
} from '@/store/slices/projects'
import { threadActivityAcknowledged } from '@/store/thunks/agentActivity'
import { activeProfileSelected, selectActiveProfileId } from '@/store/slices/preferences'
import {
  sidebarClosed,
  sidebarDismissed,
  sidebarOpened,
} from '@/store/slices/ui'
import type {
  CodingAgentStart,
  Profile,
  Project,
  Thread,
  WorkspaceTool,
} from '@/types'
import { stateConnectionBanner } from '@/wire/connectionBanner'
import {
  useApplicationInstance,
  useConnectionStatus,
  useSubscription,
} from '@/wire/react'
import {
  AgentActivityTopic,
  ProfilesTopic,
  ProjectsTopic,
  SettingsTopic,
  ThreadUsageTopic,
} from '@/wire/topics'

const defaultWorkspaceTool: WorkspaceTool = 'pi'

import type { NewThreadStart } from '@/features/workspace/newThreadStart'

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
  const desktopApp = window.kiwiCodeDesktopApp ?? window.direMuxDesktopApp
  const desktopShellClassName = desktopApp
    ? `desktop-shell desktop-shell-${desktopApp.platform || 'unknown'}`
    : ''
  const workspaceMatch = useMatch(WORKSPACE_ROUTE)
  const newThreadMatch = useMatch(NEW_THREAD_ROUTE)
  const threadMatch = useMatch(THREAD_ROUTE)
  const projectMatch = useMatch(PROJECT_ROUTE)
  const projectSettingsMatch = useMatch(PROJECT_SETTINGS_ROUTE)
  const projectSettingsSectionMatch = useMatch(PROJECT_SETTINGS_SECTION_ROUTE)
  const cleanupMatch = useMatch(CLEANUP_ROUTE)
  const sessionLogMatch = useMatch(SESSION_LOG_ROUTE)
  const settingsMatch = useMatch(SETTINGS_ROUTE)
  const tmuxMatch = useMatch(TMUX_ROUTE)

  const projectSubscription = useSubscription(ProjectsTopic, undefined)
  const profileSubscription = useSubscription(ProfilesTopic, undefined)
  const activitySubscription = useSubscription(AgentActivityTopic, undefined)
  const usageSubscription = useSubscription(ThreadUsageTopic, undefined)
  const settingsSubscription = useSubscription(SettingsTopic, undefined)
  const stateConnection = useConnectionStatus()
  const reloadForNewInstance = useCallback((current: string, previous?: string) => {
    if (previous && current !== previous) window.location.reload()
  }, [])
  useApplicationInstance(reloadForNewInstance)

  const dispatch = useAppDispatch()
  // Server data comes from the store now; ServerStateBridge is what fills it.
  // The five refs that used to shadow this state -- projectsRef, piActivitiesRef,
  // threadIndexRef and the two acknowledgement maps -- existed only so socket
  // callbacks would not go stale. Thunks read getState() instead.
  const profiles = useAppSelector(selectProfiles)
  const profilesHydrated = useAppSelector(selectProfilesHydrated)
  const projects = useAppSelector(selectProjects)
  const projectsHydrated = useAppSelector(selectProjectsHydrated)
  const activeProfileId = useAppSelector(selectActiveProfileId)
  const threadIndex = useAppSelector(selectThreadIndex)
  const deletingId = useAppSelector(selectDeletingProjectId)
  const deletingThreadId = useAppSelector(selectDeletingThreadId)
  const archivingThreadId = useAppSelector(selectArchivingThreadId)
  const lastWorkspacesRef = useRef<Record<string, LastWorkspace>>({})
  const previousActiveThreadRef = useRef<string | null>(null)

  const workspaceProjectId = workspaceMatch?.params.projectId
  const workspaceThreadId = workspaceMatch?.params.threadId
  const activeTool = workspaceToolFromRoute(workspaceMatch?.params.tool)

  const acknowledgeThreadActivity = useCallback((
    projectId: string,
    threadId: string,
    retryFailed = false,
  ) => {
    void dispatch(threadActivityAcknowledged({ projectId, threadId, retryFailed }))
  }, [dispatch])

  const selectedProject = useMemo(
    () => workspaceProjectId ? threadIndex.projectById.get(workspaceProjectId) ?? null : null,
    [threadIndex, workspaceProjectId],
  )

  const selectedThread = useMemo(
    () => selectedProject && workspaceThreadId
      ? threadIndex.entry(selectedProject.id, workspaceThreadId)?.thread ?? null
      : null,
    [selectedProject, threadIndex, workspaceThreadId],
  )

  const newThreadProject = useMemo(
    () => threadIndex.projectById.get(newThreadMatch?.params.projectId ?? '') ?? null,
    [newThreadMatch?.params.projectId, threadIndex],
  )

  const legacyProject = useMemo(
    () => threadIndex.projectById.get(threadMatch?.params.projectId ?? '') ?? null,
    [threadIndex, threadMatch?.params.projectId],
  )

  const legacyThread = useMemo(
    () => legacyProject
      ? threadIndex.entry(legacyProject.id, threadMatch?.params.threadId ?? '')?.thread ?? null
      : null,
    [legacyProject, threadIndex, threadMatch?.params.threadId],
  )

  const landingProject = useMemo(
    () => threadIndex.projectById.get(projectMatch?.params.projectId ?? '') ?? null,
    [projectMatch?.params.projectId, threadIndex],
  )

  const settingsProjectId = projectSettingsSectionMatch?.params.projectId
    ?? projectSettingsMatch?.params.projectId
  const settingsProject = useMemo(
    () => threadIndex.projectById.get(settingsProjectId ?? '') ?? null,
    [settingsProjectId, threadIndex],
  )

  const activeProfile = useMemo(
    () => profiles.find((profile) => profile.id === activeProfileId) ?? profiles[0] ?? null,
    [activeProfileId, profiles],
  )
  const activeProjects = useAppSelector(selectActiveProjects)
  const defaultWorkspacePath = useMemo(() => firstWorkspacePath(activeProjects), [activeProjects])
  const routedProject = selectedProject ?? newThreadProject ?? legacyProject ?? landingProject ?? settingsProject
  const routedProfileId = routedProject?.profileId
  const routedProjectId = routedProject?.id ?? null
  const newThreadProjectId = newThreadProject?.id ?? null
  const activeThreadIdentity = selectedProject && selectedThread
    ? piActivityKey(selectedProject.id, selectedThread.id)
    : null
  const projectsLoading = projectSubscription.state === 'loading'
  const profilesLoading = profileSubscription.state === 'loading'
  // Subscription snapshots become ready one render before the effects above
  // copy them into mutation-friendly local state. Keep route guards suspended
  // for that hydration render so direct project/thread deep links do not
  // briefly look missing and redirect to the default workspace.
  const loading = projectsLoading
    || profilesLoading
    || (projectSubscription.state === 'ready' && !projectsHydrated)
    || (profileSubscription.state === 'ready' && !profilesHydrated)
  const loadError = projectSubscription.state === 'error'
    ? projectSubscription.error.message
    : profileSubscription.state === 'error'
      ? profileSubscription.error.message
      : ''
  const stateTopicError = [
    projectSubscription,
    profileSubscription,
    activitySubscription,
    usageSubscription,
    settingsSubscription,
  ].flatMap((subscription) =>
    subscription.state === 'error' ? [subscription.error.message] : [])[0] ?? ''

  function retryGlobalState() {
    if (projectSubscription.state === 'error') projectSubscription.retry()
    if (profileSubscription.state === 'error') profileSubscription.retry()
    if (activitySubscription.state === 'error') activitySubscription.retry()
    if (usageSubscription.state === 'error') usageSubscription.retry()
    if (settingsSubscription.state === 'error') settingsSubscription.retry()
  }
  const connectionBanner = stateConnectionBanner(stateConnection, stateTopicError)

  useEffect(() => {
    if (profilesLoading || profiles.length === 0 || profiles.some((profile) => profile.id === activeProfileId)) return
    dispatch(activeProfileSelected(profiles[0].id))
  }, [activeProfileId, dispatch, profiles, profilesLoading])

  useEffect(() => {
    if (!routedProfileId) return
    // An equal assignment is a no-op under immer, so no guard is needed here.
    dispatch(activeProfileSelected(routedProfileId))
  }, [dispatch, routedProfileId])

  useEffect(() => {
    if (!routedProjectId) return
    const projectId = routedProjectId

    function handleNewThreadShortcut(event: KeyboardEvent) {
      if (!event.ctrlKey || event.metaKey || event.altKey || event.shiftKey || event.key.toLowerCase() !== 'n') return
      event.preventDefault()
      event.stopPropagation()
      if (event.repeat || newThreadProjectId === projectId) return

      navigate(newThreadPath(projectId))
      dispatch(sidebarDismissed())
    }

    window.addEventListener('keydown', handleNewThreadShortcut, true)
    return () => window.removeEventListener('keydown', handleNewThreadShortcut, true)
  }, [navigate, newThreadProjectId, routedProjectId])

  useEffect(() => {
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
    // Selecting the row is an explicit retry after a previous acknowledgement
    // failure; passive workspace interaction does not keep flashing that status.
    acknowledgeThreadActivity(projectId, threadId, true)
    const tool = selectedProject?.id === projectId && selectedThread?.id === threadId && activeTool
      ? activeTool
      : defaultWorkspaceTool
    navigate(workspacePath(projectId, threadId, tool))
    dispatch(sidebarDismissed())
  }

  function handleProfileSelected(profileId: string) {
    if (!profiles.some((profile) => profile.id === profileId)) return
    dispatch(activeProfileSelected(profileId))
    dispatch(sidebarClosed())
    if (cleanupMatch || sessionLogMatch || settingsMatch || tmuxMatch) return

    const profileProjects = projects.filter((project) => project.profileId === profileId)
    const destination = rememberedWorkspacePath(profileProjects, lastWorkspacesRef.current[profileId] ?? null)
      ?? firstWorkspacePath(profileProjects)
      ?? '/'
    navigate(destination)
  }

  function handleProfileCreated(profile: Profile) {
    dispatch(profileCreated(profile))
    dispatch(activeProfileSelected(profile.id))
    dispatch(sidebarClosed())
    if (!cleanupMatch && !sessionLogMatch && !settingsMatch && !tmuxMatch) navigate('/')
  }

  function handleCreated(project: Project) {
    dispatch(projectCreated(project))
    dispatch(sidebarClosed())
    const thread = project.threads.find((item) => !item.parentThreadId) ?? project.threads[0]
    navigate(thread ? workspacePath(project.id, thread.id, defaultWorkspaceTool) : newThreadPath(project.id))
  }

  function handleThreadCreated(
    projectId: string,
    thread: Thread,
    start: CodingAgentStart,
  ) {
    dispatch(threadCreated({ projectId, thread }))
    dispatch(sidebarClosed())
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

  function handleProjectUpdated(updatedProject: Project) {
    dispatch(projectUpdated(updatedProject))
    if (selectedProject?.id === updatedProject.id) {
      dispatch(activeProfileSelected(updatedProject.profileId))
    }
  }

  // The five mutations below are thunks: each moves the list optimistically and
  // rolls back if the server refuses, and its pending/rejected lifecycle is what
  // drives the sidebar's in-flight spinners. What stays here is the part a thunk
  // must not own -- confirming with the user, and navigating afterwards.
  async function handleDelete(project: Project) {
    if (deletingId || !window.confirm(`Remove “${project.name}” from Kiwi Code?\n\nIts tmux sessions and running tools will be stopped. The project folder will not be deleted. Clean managed worktrees may be removed later according to automatic cleanup settings; their Git branches will remain.`)) {
      return
    }
    const result = await dispatch(projectRemoved(project.id))
    if (projectRemoved.rejected.match(result)) window.alert(result.payload)
  }

  async function handleThreadArchived(project: Project, thread: Thread, archived: boolean) {
    if (archivingThreadId) return
    const result = await dispatch(threadArchived({
      projectId: project.id,
      threadId: thread.id,
      archived,
    }))
    if (threadArchived.rejected.match(result)) {
      window.alert(result.payload)
      return
    }
    // Archiving the thread you are looking at has to move you somewhere real.
    if (archived && selectedProject?.id === project.id && selectedThread?.id === thread.id) {
      const nextThread = project.threads.find((candidate) => candidate.id !== thread.id && !candidate.parentThreadId && !candidate.archivedAt)
      navigate(nextThread
        ? workspacePath(project.id, nextThread.id, defaultWorkspaceTool)
        : newThreadPath(project.id))
    }
  }

  async function handleDeleteThread(project: Project, thread: Thread) {
    const descendantIds = new Set(
      threadIndex.tree(project.id)?.descendants(thread.id).map((candidate) => candidate.id) ?? [],
    )
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

    const result = await dispatch(threadRemoved({
      projectId: project.id,
      threadId: thread.id,
      descendantIds: [...descendantIds],
    }))
    if (threadRemoved.rejected.match(result)) window.alert(result.payload)
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
    <div className={`flex h-dvh min-h-[32rem] overflow-hidden bg-ghost-black text-ghost-bright-white antialiased ${
      desktopShellClassName
    }`}>
      {connectionBanner && (
        <div
          role="status"
          className="fixed left-1/2 top-2 z-[100] flex -translate-x-1/2 items-center gap-2 rounded-full border border-ghost-yellow/35 bg-ghost-black/90 px-3 py-1.5 font-mono text-[9px] text-ghost-yellow shadow-lg"
        >
          <span>{connectionBanner.message}</span>
          {connectionBanner.canRetryTopics && (
            <button
              type="button"
              onClick={retryGlobalState}
              className="font-semibold text-ghost-bright-white hover:text-white"
            >
              Retry
            </button>
          )}
        </div>
      )}
      <ProjectSidebar
        onSelectProfile={handleProfileSelected}
        onProfileCreated={handleProfileCreated}
        onSelectThread={handleThreadSelected}
        onProjectCreated={handleCreated}
        onDeleteProject={handleDelete}
        onArchiveThread={(project, thread, archived) => void handleThreadArchived(project, thread, archived)}
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
                  onOpenSidebar={() => dispatch(sidebarOpened())}
                  onBack={() => navigate(workspaceReturnDestination(), { replace: true })}
                />
              )}
            />
            <Route
              path={SESSION_LOG_ROUTE}
              element={(
                <SessionLogScreen
                  onOpenSidebar={() => dispatch(sidebarOpened())}
                  onBack={() => navigate(workspaceReturnDestination(), { replace: true })}
                />
              )}
            />
            <Route
              path={TMUX_ROUTE}
              element={(
                <TmuxScreen
                  onOpenSidebar={() => dispatch(sidebarOpened())}
                  onBack={() => navigate(workspaceReturnDestination(), { replace: true })}
                />
              )}
            />
            <Route
              path={SETTINGS_ROUTE}
              element={<Navigate to={settingsPath(DEFAULT_GLOBAL_SETTINGS_SECTION)} replace />}
            />
            <Route
              path={SETTINGS_SECTION_ROUTE}
              element={(
                <SettingsShell
                  scope="global"
                  profiles={profiles}
                  onProjectUpdated={handleProjectUpdated}
                  onOpenSidebar={() => dispatch(sidebarOpened())}
                  onBack={() => navigate(workspaceReturnDestination(), { replace: true })}
                />
              )}
            />
            <Route
              path={THREAD_SANDBOX_ROUTE}
              element={selectedProject && selectedThread ? (
                <SandboxSettingsScreen
                  key={`${selectedProject.id}:${selectedThread.id}:sandbox`}
                  scope="thread"
                  project={selectedProject}
                  thread={selectedThread}
                  onOpenSidebar={() => dispatch(sidebarOpened())}
                  onBack={() => navigate(
                    workspacePath(selectedProject.id, selectedThread.id, defaultWorkspaceTool),
                    { replace: true },
                  )}
                />
              ) : (
                <Navigate to={defaultWorkspacePath ?? '/'} replace />
              )}
            />
            <Route
              path={PROJECT_SETTINGS_ROUTE}
              element={settingsProject ? (
                <Navigate to={projectSettingsPath(settingsProject.id, DEFAULT_PROJECT_SETTINGS_SECTION)} replace />
              ) : (
                <Navigate to={defaultWorkspacePath ?? '/'} replace />
              )}
            />
            <Route
              path={PROJECT_SETTINGS_SECTION_ROUTE}
              element={settingsProject ? (
                <SettingsShell
                  scope="project"
                  project={settingsProject}
                  profiles={profiles}
                  onProjectUpdated={handleProjectUpdated}
                  onOpenSidebar={() => dispatch(sidebarOpened())}
                  onBack={() => navigate(workspaceReturnDestination(settingsProject.id), { replace: true })}
                />
              ) : (
                <Navigate to={defaultWorkspacePath ?? '/'} replace />
              )}
            />
            <Route
              path={NEW_THREAD_ROUTE}
              element={newThreadProject ? (
                <NewThreadScreen
                  key={newThreadProject.id}
                  project={newThreadProject}
                  onOpenSidebar={() => dispatch(sidebarOpened())}
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
                  project={selectedProject}
                  thread={selectedThread}
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
                  onOpenSidebar={() => dispatch(sidebarOpened())}
                />
              )}
            />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        )}
      </div>

    </div>
  )
}
