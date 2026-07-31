import { useState } from 'react'
import { Navigate, useNavigate, useParams } from 'react-router-dom'
import { Search } from 'lucide-react'
import { classNames } from '@/lib/classNames'
import { projectSettingsPath, settingsPath } from '@/app/routes'
import { useAppSelector } from '@/store/hooks'
import { retryTopic } from '@/store/socketAccess'
import { selectSettings, selectSettingsError, selectSettingsStatus } from '@/store/slices/settings'
import type { Profile, Project } from '@/types'
import { SettingsTopic } from '@/wire/topics'
import { TextInput } from '@/ui/inputs'
import { LoadErrorPanel, LoadingPanel } from '@/ui/feedback'
import { ScreenHeader } from '@/ui/layout'
import { ProjectEnvironmentSettings } from './ProjectEnvironmentSettings'
import {
  DEFAULT_GLOBAL_SETTINGS_SECTION,
  DEFAULT_PROJECT_SETTINGS_SECTION,
  GLOBAL_SETTINGS_SECTIONS,
  PROJECT_SETTINGS_SECTIONS,
  isGlobalSettingsSection,
  isProjectSettingsSection,
  settingsSectionMatches,
  type GlobalSettingsSectionId,
  type ProjectSettingsSectionId,
  type SettingsSectionMeta,
  type SettingsSectionTone,
} from './registry'
import { AgentsSection } from '@/features/settings/sections/AgentsSection'
import { AppearanceSection } from '@/features/settings/sections/AppearanceSection'
import { ClaudeProfilesSection } from '@/features/settings/sections/ClaudeProfilesSection'
import { CleanupSection } from '@/features/settings/sections/CleanupSection'
import { ProjectBranchesSection } from '@/features/settings/sections/ProjectBranchesSection'
import { ProjectGeneralSection } from '@/features/settings/sections/ProjectGeneralSection'
import { SkillsSection } from '@/features/settings/sections/SkillsSection'
import { WorktreesSection } from '@/features/settings/sections/WorktreesSection'

type SettingsShellProps = {
  scope: 'global' | 'project'
  project?: Project
  profiles: Profile[]
  onProjectUpdated: (project: Project) => void
  onOpenSidebar: () => void
  onBack: () => void
}

const navIconToneStyles: Record<SettingsSectionTone, string> = {
  green: 'text-ghost-green',
  yellow: 'text-ghost-yellow',
  blue: 'text-ghost-blue',
  magenta: 'text-ghost-magenta',
}

type NavEntryProps = {
  meta: SettingsSectionMeta
  active: boolean
  onSelect: () => void
}

function NavEntry({ meta, active, onSelect }: NavEntryProps) {
  const Icon = meta.icon
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-current={active ? 'page' : undefined}
      className={classNames(
        'flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[11px] font-medium transition',
        active
          ? 'bg-ghost-raised text-ghost-bright-white'
          : 'text-ghost-muted hover:bg-ghost-raised/60 hover:text-ghost-white',
      )}
    >
      <Icon size={14} className={classNames('shrink-0', navIconToneStyles[meta.tone])} />
      <span className="truncate">{meta.label}</span>
    </button>
  )
}

function NavPill({ meta, active, onSelect }: NavEntryProps) {
  const Icon = meta.icon
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-current={active ? 'page' : undefined}
      className={classNames(
        'flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-lg px-2.5 py-1.5 text-[10px] font-medium transition',
        active
          ? 'bg-ghost-raised text-ghost-bright-white'
          : 'text-ghost-dim hover:bg-ghost-raised/60 hover:text-ghost-white',
      )}
    >
      <Icon size={12} className={classNames('shrink-0', navIconToneStyles[meta.tone])} />
      {meta.label}
    </button>
  )
}

export function SettingsShell({
  scope,
  project,
  profiles,
  onProjectUpdated,
  onOpenSidebar,
  onBack,
}: SettingsShellProps) {
  const navigate = useNavigate()
  const { section } = useParams()
  // Settings come from the store rather than a local copy, so a section that
  // saves is visible to every other reader immediately. ThemeProvider watches the
  // same slice, which is why this no longer has to push the theme itself.
  const settings = useAppSelector(selectSettings)
  const settingsStatus = useAppSelector(selectSettingsStatus)
  const settingsError = useAppSelector(selectSettingsError)
  const settingsLoading = settingsStatus === 'loading'

  const [query, setQuery] = useState('')

  if (scope === 'global' && !isGlobalSettingsSection(section)) {
    return <Navigate to={settingsPath(DEFAULT_GLOBAL_SETTINGS_SECTION)} replace />
  }
  if (scope === 'project' && (!project || !isProjectSettingsSection(section))) {
    return project
      ? <Navigate to={projectSettingsPath(project.id, DEFAULT_PROJECT_SETTINGS_SECTION)} replace />
      : <Navigate to={settingsPath(DEFAULT_GLOBAL_SETTINGS_SECTION)} replace />
  }

  const sections: SettingsSectionMeta[] = scope === 'global'
    ? GLOBAL_SETTINGS_SECTIONS
    : PROJECT_SETTINGS_SECTIONS
  const visibleSections = sections.filter((meta) => settingsSectionMatches(meta, query))

  function openSection(id: string) {
    if (scope === 'project' && project) {
      navigate(projectSettingsPath(project.id, id))
      return
    }
    navigate(settingsPath(id))
  }

  function renderSectionContent() {
    if (scope === 'project' && project) {
      switch (section as ProjectSettingsSectionId) {
        case 'profile':
          return (
            <ProjectGeneralSection
              project={project}
              profiles={profiles}
              onProjectUpdated={onProjectUpdated}
            />
          )
        case 'environment':
          return <ProjectEnvironmentSettings project={project} onProjectUpdated={onProjectUpdated} />
        case 'branches':
          return <ProjectBranchesSection project={project} onProjectUpdated={onProjectUpdated} />
      }
    }

    if (settingsLoading) return <LoadingPanel label="Loading settings" />
    if (!settings) {
      return (
        <LoadErrorPanel
          message={settingsError || 'Could not load settings.'}
          onRetry={() => retryTopic(SettingsTopic, undefined)}
        />
      )
    }

    switch (section as GlobalSettingsSectionId) {
      case 'worktrees':
        return <WorktreesSection settings={settings} />
      case 'profiles':
        return <ClaudeProfilesSection settings={settings} />
      case 'cleanup':
        return <CleanupSection settings={settings} />
      case 'agents':
        return <AgentsSection settings={settings} />
      case 'appearance':
        return <AppearanceSection settings={settings} />
      case 'skills':
        return <SkillsSection />
    }
  }

  return (
    <div className="flex h-full min-w-0 flex-col bg-ghost-black">
      <ScreenHeader
        title={scope === 'project' ? 'Project settings' : 'Settings'}
        subtitle={scope === 'project' && project ? project.name : 'Kiwi Code configuration'}
        backLabel="Back to workspace"
        onOpenSidebar={onOpenSidebar}
        onBack={onBack}
      />

      <div className="flex min-h-0 flex-1">
        <aside className="hidden w-60 shrink-0 flex-col gap-3 overflow-y-auto border-r border-ghost-border/60 bg-ghost-sidebar/50 p-3 lg:flex">
          <div className="relative">
            <Search size={13} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-ghost-faint" />
            <TextInput
              variant="search"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search settings"
              aria-label="Search settings"
              autoComplete="off"
              spellCheck={false}
            />
          </div>
          {visibleSections.length > 0 ? (
            <div className="space-y-0.5">
              {visibleSections.map((meta) => (
                <NavEntry
                  key={meta.id}
                  meta={meta}
                  active={section === meta.id}
                  onSelect={() => openSection(meta.id)}
                />
              ))}
            </div>
          ) : (
            <p className="px-2.5 pt-1 text-[10px] leading-4 text-ghost-faint">
              No settings match “{query.trim()}”.
            </p>
          )}
        </aside>

        <main className="relative min-h-0 min-w-0 flex-1 overflow-y-auto px-5 py-8 sm:px-8 sm:py-10">
          <div className="empty-grid pointer-events-none absolute inset-0 opacity-35" />
          <div className="relative mx-auto w-full max-w-[44rem]">
            <nav
              aria-label="Settings sections"
              className="mb-6 flex gap-1 overflow-x-auto pb-1 lg:hidden"
            >
              {sections.map((meta) => (
                <NavPill
                  key={meta.id}
                  meta={meta}
                  active={section === meta.id}
                  onSelect={() => openSection(meta.id)}
                />
              ))}
            </nav>

            <div className="space-y-5">
              {renderSectionContent()}
            </div>
          </div>
        </main>
      </div>
    </div>
  )
}
