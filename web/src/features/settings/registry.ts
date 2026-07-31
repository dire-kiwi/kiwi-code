import {
  Clock3,
  FolderGit2,
  Palette,
  Settings2,
  Sparkles,
  UserRound,
  Workflow,
  type LucideIcon,
} from 'lucide-react'

export type SettingsSectionTone = 'green' | 'yellow' | 'blue' | 'magenta'

export type SettingsSectionMeta<Id extends string = string> = {
  id: Id
  label: string
  keywords: string[]
  icon: LucideIcon
  tone: SettingsSectionTone
}

export type GlobalSettingsSectionId =
  | 'worktrees'
  | 'profiles'
  | 'cleanup'
  | 'agents'
  | 'appearance'
  | 'skills'

export type ProjectSettingsSectionId = 'profile' | 'environment' | 'branches'

export const GLOBAL_SETTINGS_SECTIONS: Array<SettingsSectionMeta<GlobalSettingsSectionId>> = [
  {
    id: 'worktrees',
    label: 'Worktrees',
    keywords: ['git', 'worktree', 'base location', 'directory', 'threads'],
    icon: FolderGit2,
    tone: 'green',
  },
  {
    id: 'profiles',
    label: 'Coding agents',
    keywords: ['pi', 'pi native', 'claude code', 'gpt', 'default', 'order', 'dropdown', 'config directory', 'accounts', 'login', 'sessions'],
    icon: UserRound,
    tone: 'blue',
  },
  {
    id: 'cleanup',
    label: 'Cleanup',
    keywords: ['retention', 'archived threads', 'unattached worktrees', 'days', 'delete'],
    icon: Clock3,
    tone: 'yellow',
  },
  {
    id: 'agents',
    label: 'Agents & workflows',
    keywords: ['sub-agent', 'nesting depth', 'dynamic workflows', 'pi', 'ultracode', 'size guidance'],
    icon: Workflow,
    tone: 'green',
  },
  {
    id: 'appearance',
    label: 'Appearance',
    keywords: ['theme', 'font', 'colors', 'palette', 'ansi', 'terminal', 'canvas'],
    icon: Palette,
    tone: 'magenta',
  },
  {
    id: 'skills',
    label: 'Agent skills',
    keywords: ['install', 'threads', 'processes', 'mermaid', 'pi'],
    icon: Sparkles,
    tone: 'blue',
  },
]

export const PROJECT_SETTINGS_SECTIONS: Array<SettingsSectionMeta<ProjectSettingsSectionId>> = [
  {
    id: 'profile',
    label: 'Profile & agents',
    keywords: ['profile', 'sub-agent', 'nesting depth', 'figma', 'mcp'],
    icon: UserRound,
    tone: 'blue',
  },
  {
    id: 'environment',
    label: 'Environment',
    keywords: ['setup script', 'cleanup script', 'actions', 'variables', 'platform'],
    icon: Settings2,
    tone: 'green',
  },
  {
    id: 'branches',
    label: 'Branches & paths',
    keywords: ['branch prefix', 'worktree', 'project root', 'path', 'related projects', 'add-dir', 'claude'],
    icon: FolderGit2,
    tone: 'green',
  },
]

export const DEFAULT_GLOBAL_SETTINGS_SECTION: GlobalSettingsSectionId = 'worktrees'
export const DEFAULT_PROJECT_SETTINGS_SECTION: ProjectSettingsSectionId = 'profile'

export function isGlobalSettingsSection(section: string | undefined): section is GlobalSettingsSectionId {
  return GLOBAL_SETTINGS_SECTIONS.some((item) => item.id === section)
}

export function isProjectSettingsSection(section: string | undefined): section is ProjectSettingsSectionId {
  return PROJECT_SETTINGS_SECTIONS.some((item) => item.id === section)
}

export function settingsSectionMatches(meta: SettingsSectionMeta, query: string): boolean {
  const needle = query.trim().toLocaleLowerCase()
  if (!needle) return true
  return meta.label.toLocaleLowerCase().includes(needle)
    || meta.keywords.some((keyword) => keyword.toLocaleLowerCase().includes(needle))
}
