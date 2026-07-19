import { useEffect, useState, type FormEvent } from 'react'
import {
  Archive,
  Check,
  Clock3,
  Download,
  FolderGit2,
  LoaderCircle,
  Network,
  Palette,
  RotateCcw,
  Save,
  Settings2,
  Sparkles,
  Workflow,
} from 'lucide-react'
import {
  getAgentSkillStatus,
  getSettings,
  installAgentSkill,
  updateSettings,
} from '../../api'
import { isHexColor, MAX_CLEANUP_RETENTION_DAYS } from '../../lib/validation'
import { DEFAULT_THEME, themesEqual, useTheme } from '../../theme'
import type { AgentSkillStatus, AppSettings, ThemeColors, ThemeSettings } from '../../types'
import { GhostButton, PrimaryButton } from '../atoms/Button'
import { TextInput } from '../atoms/Input'
import { Select } from '../atoms/Select'
import { StatusBadge } from '../atoms/StatusBadge'
import { Surface } from '../atoms/Surface'
import { LoadErrorPanel, LoadingPanel } from '../molecules/AsyncStatePanel'
import { FeedbackMessage } from '../molecules/FeedbackMessage'
import { InfoCallout } from '../molecules/InfoCallout'
import { PageIntro } from '../molecules/PageIntro'
import { ScreenHeader } from '../molecules/ScreenHeader'
import { SectionHeader } from '../molecules/SectionHeader'
import { ThemeColorInput } from '../molecules/ThemeColorInput'
import { FormScreenTemplate } from '../templates/FormScreenTemplate'

type SettingsScreenProps = {
  onOpenSidebar: () => void
  onBack: () => void
}

type SavingAction = 'worktree-save' | 'worktree-reset' | 'cleanup-save' | 'nesting-save' | 'workflows-save' | 'theme-save' | 'theme-reset' | null

type ThemeColorGroup = {
  title: string
  description: string
  colors: Array<{ key: keyof ThemeColors; label: string }>
}

const themeColorGroups: ThemeColorGroup[] = [
  {
    title: 'Interface',
    description: 'Application surfaces and text',
    colors: [
      { key: 'canvas', label: 'Canvas' },
      { key: 'sidebar', label: 'Sidebar' },
      { key: 'background', label: 'Terminal' },
      { key: 'panel', label: 'Panel' },
      { key: 'raised', label: 'Raised' },
      { key: 'selected', label: 'Selected' },
      { key: 'border', label: 'Border' },
      { key: 'foreground', label: 'Foreground' },
      { key: 'muted', label: 'Muted text' },
      { key: 'dim', label: 'Dim text' },
    ],
  },
  {
    title: 'Cursor & selection',
    description: 'Terminal interaction colors',
    colors: [
      { key: 'cursor', label: 'Cursor' },
      { key: 'selectionBackground', label: 'Selection' },
      { key: 'selectionForeground', label: 'Selected text' },
    ],
  },
  {
    title: 'Normal palette',
    description: 'ANSI terminal colors 0–7',
    colors: [
      { key: 'black', label: 'Black' },
      { key: 'red', label: 'Red' },
      { key: 'green', label: 'Green' },
      { key: 'yellow', label: 'Yellow' },
      { key: 'blue', label: 'Blue' },
      { key: 'magenta', label: 'Magenta' },
      { key: 'cyan', label: 'Cyan' },
      { key: 'white', label: 'White' },
    ],
  },
  {
    title: 'Bright palette',
    description: 'ANSI terminal colors 8–15',
    colors: [
      { key: 'brightBlack', label: 'Bright black' },
      { key: 'brightRed', label: 'Bright red' },
      { key: 'brightGreen', label: 'Bright green' },
      { key: 'brightYellow', label: 'Bright yellow' },
      { key: 'brightBlue', label: 'Bright blue' },
      { key: 'brightMagenta', label: 'Bright magenta' },
      { key: 'brightCyan', label: 'Bright cyan' },
      { key: 'brightWhite', label: 'Bright white' },
    ],
  },
]

export function SettingsScreen({ onOpenSidebar, onBack }: SettingsScreenProps) {
  const { setTheme: applyTheme } = useTheme()
  const [settings, setSettings] = useState<AppSettings | null>(null)
  const [worktreeBasePath, setWorktreeBasePath] = useState('')
  const [archivedThreadRetentionDays, setArchivedThreadRetentionDays] = useState('')
  const [orphanedWorktreeRetentionDays, setOrphanedWorktreeRetentionDays] = useState('')
  const [subAgentNestingDepth, setSubAgentNestingDepth] = useState('')
  const [disableWorkflows, setDisableWorkflows] = useState(false)
  const [workflowKeywordTrigger, setWorkflowKeywordTrigger] = useState(true)
  const [workflowSizeGuideline, setWorkflowSizeGuideline] = useState<AppSettings['workflowSizeGuideline']>('unrestricted')
  const [theme, setTheme] = useState<ThemeSettings>(DEFAULT_THEME)
  const [loading, setLoading] = useState(true)
  const [loadKey, setLoadKey] = useState(0)
  const [saving, setSaving] = useState<SavingAction>(null)
  const [error, setError] = useState('')
  const [savedMessage, setSavedMessage] = useState('')
  const [cleanupError, setCleanupError] = useState('')
  const [cleanupMessage, setCleanupMessage] = useState('')
  const [nestingError, setNestingError] = useState('')
  const [nestingMessage, setNestingMessage] = useState('')
  const [workflowsError, setWorkflowsError] = useState('')
  const [workflowsMessage, setWorkflowsMessage] = useState('')
  const [themeError, setThemeError] = useState('')
  const [themeMessage, setThemeMessage] = useState('')
  const [agentSkill, setAgentSkill] = useState<AgentSkillStatus | null>(null)
  const [installingSkill, setInstallingSkill] = useState(false)
  const [skillError, setSkillError] = useState('')
  const [skillMessage, setSkillMessage] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError('')
    Promise.all([
      getSettings(controller.signal),
      getAgentSkillStatus(controller.signal),
    ])
      .then(([next, skill]) => {
        setSettings(next)
        setWorktreeBasePath(next.worktreeBasePath)
        setArchivedThreadRetentionDays(String(next.archivedThreadRetentionDays))
        setOrphanedWorktreeRetentionDays(String(next.orphanedWorktreeRetentionDays))
        setSubAgentNestingDepth(String(next.subAgentNestingDepth))
        setDisableWorkflows(next.disableWorkflows)
        setWorkflowKeywordTrigger(next.workflowKeywordTriggerEnabled)
        setWorkflowSizeGuideline(next.workflowSizeGuideline)
        setTheme(next.theme)
        applyTheme(next.theme)
        setAgentSkill(skill)
      })
      .catch((reason) => {
        if (controller.signal.aborted) return
        setError(reason instanceof Error ? reason.message : 'Could not load settings.')
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false)
      })
    return () => controller.abort()
  }, [applyTheme, loadKey])

  async function handleSave(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const path = worktreeBasePath.trim()
    if (!path || saving) return

    setSaving('worktree-save')
    setError('')
    setSavedMessage('')
    try {
      const next = await updateSettings(path)
      setSettings(next)
      setWorktreeBasePath(next.worktreeBasePath)
      setSavedMessage('Worktree location saved.')
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not save settings.')
    } finally {
      setSaving(null)
    }
  }

  async function handleReset() {
    if (saving) return
    setSaving('worktree-reset')
    setError('')
    setSavedMessage('')
    try {
      const next = await updateSettings('')
      setSettings(next)
      setWorktreeBasePath(next.worktreeBasePath)
      setSavedMessage('Worktree location reset to the default.')
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not reset settings.')
    } finally {
      setSaving(null)
    }
  }

  async function handleCleanupSave(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (saving) return
    const archivedDays = Number(archivedThreadRetentionDays)
    const orphanedDays = Number(orphanedWorktreeRetentionDays)
    if (
      !archivedThreadRetentionDays.trim()
      || !orphanedWorktreeRetentionDays.trim()
      || !Number.isInteger(archivedDays)
      || !Number.isInteger(orphanedDays)
      || archivedDays < 0
      || orphanedDays < 0
      || archivedDays > MAX_CLEANUP_RETENTION_DAYS
      || orphanedDays > MAX_CLEANUP_RETENTION_DAYS
    ) {
      setCleanupError(`Retention must be a whole number from 0 to ${MAX_CLEANUP_RETENTION_DAYS} days.`)
      return
    }

    setSaving('cleanup-save')
    setCleanupError('')
    setCleanupMessage('')
    try {
      const next = await updateSettings({
        archivedThreadRetentionDays: archivedDays,
        orphanedWorktreeRetentionDays: orphanedDays,
      })
      setSettings(next)
      setArchivedThreadRetentionDays(String(next.archivedThreadRetentionDays))
      setOrphanedWorktreeRetentionDays(String(next.orphanedWorktreeRetentionDays))
      setCleanupMessage('Automatic cleanup settings saved.')
    } catch (reason) {
      setCleanupError(reason instanceof Error ? reason.message : 'Could not save cleanup settings.')
    } finally {
      setSaving(null)
    }
  }

  async function handleNestingSave(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (saving || !settings) return
    const depth = Number(subAgentNestingDepth)
    if (
      !subAgentNestingDepth.trim()
      || !Number.isInteger(depth)
      || depth < 0
      || depth > settings.maxSubAgentNestingDepth
    ) {
      setNestingError(`Depth must be a whole number from 0 to ${settings.maxSubAgentNestingDepth}.`)
      return
    }

    setSaving('nesting-save')
    setNestingError('')
    setNestingMessage('')
    try {
      const next = await updateSettings({ subAgentNestingDepth: depth })
      setSettings(next)
      setSubAgentNestingDepth(String(next.subAgentNestingDepth))
      setNestingMessage('Sub-agent nesting depth saved.')
    } catch (reason) {
      setNestingError(reason instanceof Error ? reason.message : 'Could not save sub-agent nesting depth.')
    } finally {
      setSaving(null)
    }
  }

  async function handleWorkflowsSave(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (saving || !settings) return
    setSaving('workflows-save')
    setWorkflowsError('')
    setWorkflowsMessage('')
    try {
      const next = await updateSettings({
        disableWorkflows,
        workflowKeywordTriggerEnabled: workflowKeywordTrigger,
        workflowSizeGuideline,
      })
      setSettings(next)
      setDisableWorkflows(next.disableWorkflows)
      setWorkflowKeywordTrigger(next.workflowKeywordTriggerEnabled)
      setWorkflowSizeGuideline(next.workflowSizeGuideline)
      setWorkflowsMessage('Dynamic workflow settings saved.')
    } catch (reason) {
      setWorkflowsError(reason instanceof Error ? reason.message : 'Could not save workflow settings.')
    } finally {
      setSaving(null)
    }
  }

  function updateThemeColor(key: keyof ThemeColors, value: string) {
    setTheme((current) => ({
      ...current,
      colors: { ...current.colors, [key]: value },
    }))
    setThemeError('')
    setThemeMessage('')
  }

  async function handleThemeSave(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (saving) return

    setSaving('theme-save')
    setThemeError('')
    setThemeMessage('')
    try {
      const next = await updateSettings({
        theme: { ...theme, fontFamily: theme.fontFamily.trim() },
      })
      setSettings(next)
      setTheme(next.theme)
      applyTheme(next.theme)
      setThemeMessage('Appearance saved and applied.')
    } catch (reason) {
      setThemeError(reason instanceof Error ? reason.message : 'Could not save appearance settings.')
    } finally {
      setSaving(null)
    }
  }

  async function handleThemeReset() {
    if (saving || !settings) return
    setSaving('theme-reset')
    setThemeError('')
    setThemeMessage('')
    try {
      const next = await updateSettings({ theme: settings.defaultTheme })
      setSettings(next)
      setTheme(next.theme)
      applyTheme(next.theme)
      setThemeMessage('Appearance reset to the default theme.')
    } catch (reason) {
      setThemeError(reason instanceof Error ? reason.message : 'Could not reset appearance settings.')
    } finally {
      setSaving(null)
    }
  }

  async function handleInstallSkill() {
    if (installingSkill) return
    setInstallingSkill(true)
    setSkillError('')
    setSkillMessage('')
    try {
      const next = await installAgentSkill()
      setAgentSkill(next)
      setSkillMessage('Agent skills installed. Start a new Pi session or use /reload to load them.')
    } catch (reason) {
      setSkillError(reason instanceof Error ? reason.message : 'Could not install the agent skills.')
    } finally {
      setInstallingSkill(false)
    }
  }

  const normalizedInput = worktreeBasePath.trim()
  const dirty = settings !== null && normalizedInput !== settings.worktreeBasePath
  const canReset = settings !== null
    && (!settings.usingDefault || normalizedInput !== settings.defaultWorktreeBasePath)
  const parsedArchivedDays = Number(archivedThreadRetentionDays)
  const parsedOrphanedDays = Number(orphanedWorktreeRetentionDays)
  const cleanupValuesValid = archivedThreadRetentionDays.trim() !== ''
    && orphanedWorktreeRetentionDays.trim() !== ''
    && Number.isInteger(parsedArchivedDays)
    && Number.isInteger(parsedOrphanedDays)
    && parsedArchivedDays >= 0
    && parsedOrphanedDays >= 0
    && parsedArchivedDays <= MAX_CLEANUP_RETENTION_DAYS
    && parsedOrphanedDays <= MAX_CLEANUP_RETENTION_DAYS
  const cleanupDirty = settings !== null && cleanupValuesValid && (
    parsedArchivedDays !== settings.archivedThreadRetentionDays
    || parsedOrphanedDays !== settings.orphanedWorktreeRetentionDays
  )
  const parsedNestingDepth = Number(subAgentNestingDepth)
  const nestingValueValid = settings !== null
    && subAgentNestingDepth.trim() !== ''
    && Number.isInteger(parsedNestingDepth)
    && parsedNestingDepth >= 0
    && parsedNestingDepth <= settings.maxSubAgentNestingDepth
  const nestingDirty = settings !== null
    && nestingValueValid
    && parsedNestingDepth !== settings.subAgentNestingDepth
  const workflowsDirty = settings !== null && (
    disableWorkflows !== settings.disableWorkflows
    || workflowKeywordTrigger !== settings.workflowKeywordTriggerEnabled
    || workflowSizeGuideline !== settings.workflowSizeGuideline
  )
  const validTheme = theme.fontFamily.trim().length > 0
    && theme.fontFamily.trim().length <= 512
    && Number.isInteger(theme.fontSize)
    && theme.fontSize >= 6
    && theme.fontSize <= 72
    && Object.values(theme.colors).every(isHexColor)
  const themeDirty = settings !== null && !themesEqual(theme, settings.theme)
  const canResetTheme = settings !== null
    && (!settings.usingDefaultTheme || !themesEqual(theme, settings.defaultTheme))
  const bundledSkills = agentSkill?.skills?.length ? agentSkill.skills : agentSkill ? [agentSkill] : []

  return (
    <FormScreenTemplate
      header={(
        <ScreenHeader
          title="Settings"
          subtitle="dire/mux configuration"
          backLabel="Back to workspace"
          backDisabled={Boolean(saving) || installingSkill}
          onOpenSidebar={onOpenSidebar}
          onBack={onBack}
        />
      )}
    >
      <div className="relative mx-auto w-full max-w-[44rem]">
        <PageIntro icon={<Settings2 size={20} />} title="Settings">
          Configure project workspaces, retention, appearance, and agent integrations on this machine.
        </PageIntro>

        {loading ? (
          <LoadingPanel label="Loading settings" />
        ) : !settings ? (
          <LoadErrorPanel
            message={error || 'Could not load settings.'}
            onRetry={() => setLoadKey((current) => current + 1)}
          />
        ) : (
          <div className="space-y-5">
            <Surface
              as="form"
              variant="elevated-panel"
              onSubmit={(event) => void handleSave(event)}
              className="overflow-hidden"
            >
              <SectionHeader
                icon={<FolderGit2 size={16} />}
                title="Git worktrees"
                description="Choose the base directory used for newly-created worktree threads."
                tone="green"
                badge={(
                  <StatusBadge tone={settings.usingDefault ? 'neutral' : 'success'}>
                    {settings.usingDefault ? 'Default' : 'Custom'}
                  </StatusBadge>
                )}
              />

              <div className="p-4 sm:p-5">
                <label className="block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim">
                  Worktree base location
                  <TextInput
                    variant="code-large"
                    value={worktreeBasePath}
                    onChange={(event) => {
                      setWorktreeBasePath(event.target.value)
                      setError('')
                      setSavedMessage('')
                    }}
                    required
                    autoComplete="off"
                    spellCheck={false}
                    placeholder="/Users/me/worktrees"
                    className="mt-2.5"
                  />
                </label>

                <div className="mt-3 rounded-xl border border-ghost-border/55 bg-ghost-black/25 px-3.5 py-3">
                  <p className="text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-faint">Default location</p>
                  <p className="mt-1.5 break-all font-mono text-[10px] leading-4 text-ghost-muted">
                    {settings.defaultWorktreeBasePath}
                  </p>
                </div>

                <InfoCallout className="mt-4">
                  This only changes where future worktrees are created. Existing worktrees and their files are not moved.
                  The selected directory is created when you save it.
                </InfoCallout>

                {error && (
                  <FeedbackMessage role="alert" tone="error" className="mt-4">
                    {error}
                  </FeedbackMessage>
                )}
                {savedMessage && (
                  <FeedbackMessage
                    role="status"
                    tone="success"
                    size="status"
                    className="mt-4 flex items-center gap-2"
                  >
                    <Check size={13} />
                    {savedMessage}
                  </FeedbackMessage>
                )}
              </div>

              <div className="flex flex-wrap items-center justify-end gap-2 border-t border-ghost-border/60 bg-ghost-black/15 px-4 py-3 sm:px-5">
                <GhostButton
                  type="button"
                  size="md"
                  onClick={() => void handleReset()}
                  disabled={!canReset || Boolean(saving)}
                  className="mr-auto flex items-center gap-2 px-3 disabled:cursor-not-allowed disabled:opacity-35"
                >
                  {saving === 'worktree-reset' ? <LoaderCircle size={13} className="animate-spin" /> : <RotateCcw size={13} />}
                  Reset to default
                </GhostButton>
                <GhostButton
                  type="button"
                  size="md"
                  onClick={onBack}
                  disabled={Boolean(saving)}
                  className="px-3.5 disabled:opacity-40"
                >
                  Cancel
                </GhostButton>
                <PrimaryButton
                  type="submit"
                  size="md"
                  disabled={!dirty || !normalizedInput || Boolean(saving)}
                  className="flex min-w-28 items-center justify-center gap-2"
                >
                  {saving === 'worktree-save' ? <LoaderCircle size={14} className="animate-spin" /> : <Save size={14} />}
                  Save
                </PrimaryButton>
              </div>
            </Surface>

            <Surface
              as="form"
              variant="elevated-panel"
              onSubmit={(event) => void handleCleanupSave(event)}
              className="overflow-hidden"
            >
              <SectionHeader
                icon={<Clock3 size={16} />}
                title="Automatic cleanup"
                description="Choose how long archived threads and unattached worktrees are retained."
                tone="yellow"
              />

              <div className="space-y-4 p-4 sm:p-5">
                <label className="block rounded-xl border border-ghost-border/55 bg-ghost-black/25 p-3.5">
                  <span className="flex items-center gap-2 text-[10px] font-semibold text-ghost-bright-white">
                    <Archive size={14} className="text-ghost-yellow" />
                    Delete archived threads after
                  </span>
                  <span className="mt-3 flex items-center gap-2">
                    <TextInput
                      type="number"
                      min={0}
                      max={MAX_CLEANUP_RETENTION_DAYS}
                      step={1}
                      value={archivedThreadRetentionDays}
                      onChange={(event) => {
                        setArchivedThreadRetentionDays(event.target.value)
                        setCleanupError('')
                        setCleanupMessage('')
                      }}
                      required
                      inputMode="numeric"
                      className="max-w-28 font-mono"
                      aria-describedby="archived-thread-retention-help"
                    />
                    <span className="text-[10px] text-ghost-muted">days</span>
                  </span>
                  <span id="archived-thread-retention-help" className="mt-2 block text-[9px] leading-4 text-ghost-faint">
                    Deletion stops the thread’s tmux sessions. Enter 0 to keep archived threads forever.
                  </span>
                </label>

                <label className="block rounded-xl border border-ghost-border/55 bg-ghost-black/25 p-3.5">
                  <span className="flex items-center gap-2 text-[10px] font-semibold text-ghost-bright-white">
                    <FolderGit2 size={14} className="text-ghost-green" />
                    Delete unattached worktrees after
                  </span>
                  <span className="mt-3 flex items-center gap-2">
                    <TextInput
                      type="number"
                      min={0}
                      max={MAX_CLEANUP_RETENTION_DAYS}
                      step={1}
                      value={orphanedWorktreeRetentionDays}
                      onChange={(event) => {
                        setOrphanedWorktreeRetentionDays(event.target.value)
                        setCleanupError('')
                        setCleanupMessage('')
                      }}
                      required
                      inputMode="numeric"
                      className="max-w-28 font-mono"
                      aria-describedby="orphaned-worktree-retention-help"
                    />
                    <span className="text-[10px] text-ghost-muted">days</span>
                  </span>
                  <span id="orphaned-worktree-retention-help" className="mt-2 block text-[9px] leading-4 text-ghost-faint">
                    Only worktrees with no staged, unstaged, or untracked changes are removed. Git branches are kept. Enter 0 to disable cleanup.
                  </span>
                </label>

                <InfoCallout>
                  Cleanup runs when Dire Mux starts and then once per hour. A worktree becomes unattached when its thread or project is deleted.
                </InfoCallout>

                {cleanupError && (
                  <FeedbackMessage role="alert" tone="error">
                    {cleanupError}
                  </FeedbackMessage>
                )}
                {cleanupMessage && (
                  <FeedbackMessage role="status" tone="success" size="status" className="flex items-center gap-2">
                    <Check size={13} />
                    {cleanupMessage}
                  </FeedbackMessage>
                )}
              </div>

              <div className="flex items-center justify-end border-t border-ghost-border/60 bg-ghost-black/15 px-4 py-3 sm:px-5">
                <PrimaryButton
                  type="submit"
                  size="md"
                  disabled={!cleanupDirty || !cleanupValuesValid || Boolean(saving)}
                  className="flex min-w-28 items-center justify-center gap-2"
                >
                  {saving === 'cleanup-save' ? <LoaderCircle size={14} className="animate-spin" /> : <Save size={14} />}
                  Save cleanup
                </PrimaryButton>
              </div>
            </Surface>

            <Surface
              as="form"
              variant="elevated-panel"
              onSubmit={(event) => void handleNestingSave(event)}
              className="overflow-hidden"
            >
              <SectionHeader
                icon={<Network size={16} />}
                title="Sub-agent nesting"
                description="Limit how many generations of child agents can delegate to more children."
                tone="blue"
              />

              <div className="p-4 sm:p-5">
                <label className="block rounded-xl border border-ghost-border/55 bg-ghost-black/25 p-3.5">
                  <span className="text-[10px] font-semibold text-ghost-bright-white">
                    Global nesting depth
                  </span>
                  <span className="mt-3 flex items-center gap-2">
                    <TextInput
                      type="number"
                      min={0}
                      max={settings.maxSubAgentNestingDepth}
                      step={1}
                      value={subAgentNestingDepth}
                      onChange={(event) => {
                        setSubAgentNestingDepth(event.target.value)
                        setNestingError('')
                        setNestingMessage('')
                      }}
                      required
                      inputMode="numeric"
                      className="max-w-28 font-mono"
                      aria-describedby="sub-agent-nesting-help"
                    />
                    <span className="text-[10px] text-ghost-muted">
                      {parsedNestingDepth === 1 ? 'child level' : 'child levels'}
                    </span>
                  </span>
                  <span id="sub-agent-nesting-help" className="mt-2 block text-[9px] leading-4 text-ghost-faint">
                    0 disables child agents, including skill forks and workflows. 1 lets a root create one child
                    generation. Projects can override this value in their details sidebar.
                  </span>
                </label>

                <InfoCallout className="mt-4">
                  This limits child-agent delegation depth, not the number of agents scheduled in parallel. Lowering
                  it only blocks future child creation; existing child threads remain retained.
                </InfoCallout>

                {nestingError && (
                  <FeedbackMessage role="alert" tone="error" className="mt-4">
                    {nestingError}
                  </FeedbackMessage>
                )}
                {nestingMessage && (
                  <FeedbackMessage role="status" tone="success" size="status" className="mt-4 flex items-center gap-2">
                    <Check size={13} />
                    {nestingMessage}
                  </FeedbackMessage>
                )}
              </div>

              <div className="flex items-center justify-end border-t border-ghost-border/60 bg-ghost-black/15 px-4 py-3 sm:px-5">
                <PrimaryButton
                  type="submit"
                  size="md"
                  disabled={!nestingDirty || !nestingValueValid || Boolean(saving)}
                  className="flex min-w-28 items-center justify-center gap-2"
                >
                  {saving === 'nesting-save' ? <LoaderCircle size={14} className="animate-spin" /> : <Save size={14} />}
                  Save depth
                </PrimaryButton>
              </div>
            </Surface>

            <Surface
              as="form"
              variant="elevated-panel"
              onSubmit={(event) => void handleWorkflowsSave(event)}
              className="overflow-hidden"
            >
              <SectionHeader
                icon={<Workflow size={16} />}
                title="Dynamic workflows · Pi"
                description="Configure Dire Mux workflows exposed through Pi sessions."
                tone="green"
                badge={(
                  <StatusBadge tone={disableWorkflows ? 'neutral' : 'success'}>
                    {disableWorkflows ? 'Disabled' : 'Enabled'}
                  </StatusBadge>
                )}
              />

              <div className="space-y-3 p-4 sm:p-5">
                <label className="flex cursor-pointer items-start gap-3 rounded-xl border border-ghost-border/55 bg-ghost-black/25 p-3.5">
                  <input
                    type="checkbox"
                    checked={!disableWorkflows}
                    onChange={(event) => {
                      setDisableWorkflows(!event.target.checked)
                      setWorkflowsError('')
                      setWorkflowsMessage('')
                    }}
                    className="mt-0.5 size-4 accent-ghost-green"
                  />
                  <span>
                    <span className="block text-[10px] font-semibold text-ghost-bright-white">Enable dynamic workflows</span>
                    <span className="mt-1 block text-[9px] leading-4 text-ghost-faint">
                      Disabling blocks new and resumed Dire Mux runs, saved commands, and Pi ultracode activation. Retained runs remain visible.
                    </span>
                  </span>
                </label>

                <label className="flex cursor-pointer items-start gap-3 rounded-xl border border-ghost-border/55 bg-ghost-black/25 p-3.5">
                  <input
                    type="checkbox"
                    checked={workflowKeywordTrigger}
                    disabled={disableWorkflows}
                    onChange={(event) => {
                      setWorkflowKeywordTrigger(event.target.checked)
                      setWorkflowsError('')
                      setWorkflowsMessage('')
                    }}
                    className="mt-0.5 size-4 accent-ghost-green"
                  />
                  <span>
                    <span className="block text-[10px] font-semibold text-ghost-bright-white">Pi ultracode keyword trigger</span>
                    <span className="mt-1 block text-[9px] leading-4 text-ghost-faint">
                      A human-typed “ultracode” opts in for one prompt. Direct requests such as “use a workflow” still work when this is off.
                    </span>
                  </span>
                </label>

                <label className="block rounded-xl border border-ghost-border/55 bg-ghost-black/25 p-3.5">
                  <span className="text-[10px] font-semibold text-ghost-bright-white">Workflow size guidance</span>
                  <div className="mt-3 max-w-52">
                    <Select
                      value={workflowSizeGuideline}
                      options={[
                        { value: 'unrestricted', label: 'Unrestricted' },
                        { value: 'small', label: 'Small · fewer than 5 agents' },
                        { value: 'medium', label: 'Medium · fewer than 15' },
                        { value: 'large', label: 'Large · fewer than 50' },
                      ]}
                      disabled={disableWorkflows}
                      onChange={(value) => {
                        setWorkflowSizeGuideline(value as AppSettings['workflowSizeGuideline'])
                        setWorkflowsError('')
                        setWorkflowsMessage('')
                      }}
                      aria-label="Workflow size guidance"
                      className="font-sans text-[10px]"
                      menuClassName="font-sans text-[10px]"
                    />
                  </div>
                  <span className="mt-2 block text-[9px] leading-4 text-ghost-faint">
                    This is advice sent to the parent Pi model. The hard caps remain 16 concurrent and 1,000 total agents.
                  </span>
                </label>

                <InfoCallout>
                  In Pi, workflows activate from the current human prompt—use “ultracode,” directly ask to use or run a workflow, or invoke a saved /command—or from session-scoped Ultracode effort. Claude Code keeps its separate built-in Ultracode behavior.
                </InfoCallout>

                {workflowsError && (
                  <FeedbackMessage role="alert" tone="error">{workflowsError}</FeedbackMessage>
                )}
                {workflowsMessage && (
                  <FeedbackMessage role="status" tone="success" size="status" className="flex items-center gap-2">
                    <Check size={13} />
                    {workflowsMessage}
                  </FeedbackMessage>
                )}
              </div>

              <div className="flex items-center justify-end border-t border-ghost-border/60 bg-ghost-black/15 px-4 py-3 sm:px-5">
                <PrimaryButton
                  type="submit"
                  size="md"
                  disabled={!workflowsDirty || Boolean(saving)}
                  className="flex min-w-28 items-center justify-center gap-2"
                >
                  {saving === 'workflows-save' ? <LoaderCircle size={14} className="animate-spin" /> : <Save size={14} />}
                  Save workflows
                </PrimaryButton>
              </div>
            </Surface>

            <Surface
              as="form"
              variant="elevated-panel"
              onSubmit={(event) => void handleThemeSave(event)}
              className="overflow-hidden"
            >
              <SectionHeader
                icon={<Palette size={16} />}
                title="Appearance"
                description="Set the terminal typeface and size, interface surfaces, and complete ANSI color palette."
                tone="magenta"
                badge={(
                  <StatusBadge tone={settings.usingDefaultTheme ? 'neutral' : 'success'}>
                    {settings.usingDefaultTheme ? 'Default' : 'Custom'}
                  </StatusBadge>
                )}
              />

              <div className="p-4 sm:p-5">
                <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_8rem]">
                  <label className="block text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-dim">
                    Font family
                    <TextInput
                      variant="code"
                      value={theme.fontFamily}
                      onChange={(event) => {
                        setTheme((current) => ({ ...current, fontFamily: event.target.value }))
                        setThemeError('')
                        setThemeMessage('')
                      }}
                      maxLength={512}
                      autoComplete="off"
                      spellCheck={false}
                      className="mt-1.5"
                    />
                  </label>
                  <label className="block text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-dim">
                    Font size
                    <TextInput
                      variant="code"
                      type="number"
                      min={6}
                      max={72}
                      step={1}
                      value={theme.fontSize}
                      onChange={(event) => {
                        setTheme((current) => ({ ...current, fontSize: Number(event.target.value) }))
                        setThemeError('')
                        setThemeMessage('')
                      }}
                      className="mt-1.5"
                    />
                  </label>
                </div>

                <div
                  className="mt-4 overflow-hidden rounded-xl border border-ghost-border/70 px-4 py-3"
                  style={{
                    backgroundColor: theme.colors.background,
                    color: theme.colors.foreground,
                    fontFamily: theme.fontFamily,
                    fontSize: `${Math.min(Math.max(theme.fontSize || 6, 6), 24)}px`,
                  }}
                  aria-label="Theme preview"
                >
                  <p className="truncate leading-relaxed">The quick brown fox jumps over the lazy dog.</p>
                  <p className="mt-1 truncate leading-relaxed">
                    <span style={{ color: theme.colors.green }}>➜</span>{' '}
                    <span style={{ color: theme.colors.blue }}>~/dire-mux</span>{' '}
                    <span style={{ color: theme.colors.muted }}>git:(</span>
                    <span style={{ color: theme.colors.red }}>main</span>
                    <span style={{ color: theme.colors.muted }}>)</span>
                  </p>
                </div>

                <div className="mt-5 space-y-5">
                  {themeColorGroups.map((group) => (
                    <section key={group.title}>
                      <div className="mb-2.5 flex flex-wrap items-baseline justify-between gap-1">
                        <h3 className="text-[10px] font-semibold uppercase tracking-[0.12em] text-ghost-dim">
                          {group.title}
                        </h3>
                        <p className="text-[9px] text-ghost-faint">{group.description}</p>
                      </div>
                      <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-3">
                        {group.colors.map((color) => (
                          <ThemeColorInput
                            key={color.key}
                            label={color.label}
                            value={theme.colors[color.key]}
                            onChange={(value) => updateThemeColor(color.key, value)}
                          />
                        ))}
                      </div>
                    </section>
                  ))}
                </div>

                <InfoCallout className="mt-5">
                  Colors use six-digit hexadecimal values. Font sizes from 6 to 72 pixels are supported.
                  Saving applies the theme to the interface and every terminal you open.
                </InfoCallout>

                {themeError && (
                  <FeedbackMessage role="alert" tone="error" className="mt-4">
                    {themeError}
                  </FeedbackMessage>
                )}
                {themeMessage && (
                  <FeedbackMessage
                    role="status"
                    tone="success"
                    size="status"
                    className="mt-4 flex items-center gap-2"
                  >
                    <Check size={13} />
                    {themeMessage}
                  </FeedbackMessage>
                )}
              </div>

              <div className="flex flex-wrap items-center justify-end gap-2 border-t border-ghost-border/60 bg-ghost-black/15 px-4 py-3 sm:px-5">
                <GhostButton
                  type="button"
                  size="md"
                  onClick={() => void handleThemeReset()}
                  disabled={!canResetTheme || Boolean(saving)}
                  className="mr-auto flex items-center gap-2 px-3 disabled:cursor-not-allowed disabled:opacity-35"
                >
                  {saving === 'theme-reset' ? <LoaderCircle size={13} className="animate-spin" /> : <RotateCcw size={13} />}
                  Reset to default
                </GhostButton>
                <PrimaryButton
                  type="submit"
                  size="md"
                  disabled={!themeDirty || !validTheme || Boolean(saving)}
                  className="flex min-w-28 items-center justify-center gap-2"
                >
                  {saving === 'theme-save' ? <LoaderCircle size={14} className="animate-spin" /> : <Save size={14} />}
                  Save theme
                </PrimaryButton>
              </div>
            </Surface>

            <Surface as="section" variant="elevated-panel" className="overflow-hidden">
              <SectionHeader
                icon={<Sparkles size={16} />}
                title="Agent skills"
                description="Install global Dire Mux thread-control and process-management skills for Agent Skills-compatible coding agents."
                tone="blue"
                badge={agentSkill ? (
                  <StatusBadge tone={agentSkill.upToDate ? 'success' : agentSkill.installed ? 'warning' : 'neutral'}>
                    {agentSkill.upToDate ? 'Installed' : agentSkill.installed ? 'Update available' : 'Not installed'}
                  </StatusBadge>
                ) : undefined}
              />

              <div className="p-4 sm:p-5">
                <div className="rounded-xl border border-ghost-border/55 bg-ghost-black/25 px-3.5 py-3">
                  <p className="text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-faint">Install locations</p>
                  <div className="mt-1.5 space-y-1">
                    {(bundledSkills.length ? bundledSkills : [
                      { name: 'kiwi-code-processes', path: '~/.agents/skills/kiwi-code-processes' },
                      { name: 'dire-mux-threads', path: '~/.agents/skills/dire-mux-threads' },
                      { name: 'dire-mux-mermaid', path: '~/.agents/skills/dire-mux-mermaid' },
                    ]).map((skill) => (
                      <p key={skill.name} className="break-all font-mono text-[10px] leading-4 text-ghost-muted">
                        {skill.path}
                      </p>
                    ))}
                  </div>
                </div>

                <InfoCallout className="mt-4">
                  The dependency-free Node.js helpers can create, rename, archive, restore, inspect, and close threads; read Pi,
                  Claude, shell, tool, and process output; and manage persistent process shells. Claude Code launched through
                  Dire Mux already receives the process skill from its bundled plugin. Use{' '}
                  <span className="font-mono text-ghost-blue">/reload</span> in an existing Pi session after installation.
                </InfoCallout>

                {skillError && (
                  <FeedbackMessage role="alert" tone="error" className="mt-4">
                    {skillError}
                  </FeedbackMessage>
                )}
                {skillMessage && (
                  <FeedbackMessage
                    role="status"
                    tone="success"
                    className="mt-4 flex items-center gap-2"
                  >
                    <Check size={13} className="shrink-0" />
                    {skillMessage}
                  </FeedbackMessage>
                )}
              </div>

              <div className="flex items-center justify-end border-t border-ghost-border/60 bg-ghost-black/15 px-4 py-3 sm:px-5">
                <PrimaryButton
                  type="button"
                  size="md"
                  onClick={() => void handleInstallSkill()}
                  disabled={installingSkill || !agentSkill}
                  className="flex min-w-32 items-center justify-center gap-2"
                >
                  {installingSkill ? <LoaderCircle size={14} className="animate-spin" /> : <Download size={14} />}
                  {agentSkill?.upToDate ? 'Reinstall skills' : agentSkill?.installed ? 'Update skills' : 'Install skills'}
                </PrimaryButton>
              </div>
            </Surface>
          </div>
        )}
      </div>
    </FormScreenTemplate>
  )
}
