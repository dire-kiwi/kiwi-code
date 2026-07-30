import { useEffect, useRef, useState, type FormEvent } from 'react'
import {
  Check,
  FileJson2,
  FolderGit2,
  FolderOpen,
  Globe,
  LoaderCircle,
  Plus,
  Save,
  Shield,
  SquareTerminal,
  Trash2,
} from 'lucide-react'
import {
  updateGlobalSandboxConfig,
  updateThreadSandboxConfig,
} from '@/api'
import type {
  Project,
  SandboxCommandRule,
  SandboxConfig,
  SandboxConfigState,
  Thread,
} from '@/types'
import { useSubscription } from '@/wire/react'
import { SandboxConfigTopic } from '@/wire/topics'
import { GhostButton, PrimaryButton } from '@/ui/buttons'
import { Select, TextArea, TextInput } from '@/ui/inputs'
import { FeedbackMessage, InfoCallout, LoadErrorPanel, LoadingPanel, StatusBadge } from '@/ui/feedback'
import { FormScreenTemplate, PageIntro, ScreenHeader, SectionHeader, Surface } from '@/ui/layout'

type SandboxSettingsScreenProps = {
  scope: 'global' | 'thread'
  project?: Project
  thread?: Thread
  /** Render only the configuration body, for hosts that provide their own page chrome. */
  embedded?: boolean
  onOpenSidebar?: () => void
  onBack?: () => void
}

type NetworkChoice = 'inherit' | 'allowed' | 'blocked'
type PtyChoice = 'inherit' | 'enabled' | 'disabled'

type RuleDraft = {
  key: number
  patterns: string
  filesEnabled: boolean
  read: string
  write: string
  network: NetworkChoice
}

type Draft = {
  defaultsEnabled: boolean
  defaultsRead: string
  defaultsWrite: string
  commandsEnabled: boolean
  rules: RuleDraft[]
  network: NetworkChoice
  pty: PtyChoice
  shell: string
  relatedProjects: string
}

function splitLines(value: string): string[] {
  return value.split('\n').map((line) => line.trim()).filter(Boolean)
}

function networkChoice(value: boolean | undefined): NetworkChoice {
  return value === undefined ? 'inherit' : value ? 'allowed' : 'blocked'
}

function ptyChoice(value: boolean | undefined): PtyChoice {
  return value === undefined ? 'inherit' : value ? 'enabled' : 'disabled'
}

function ruleDraft(rule: SandboxCommandRule, key: number): RuleDraft {
  return {
    key,
    patterns: rule.patterns.join('\n'),
    filesEnabled: rule.files !== undefined,
    read: rule.files?.read.join('\n') ?? '',
    write: rule.files?.write.join('\n') ?? '',
    network: networkChoice(rule.network),
  }
}

function draftFromState(state: SandboxConfigState, nextKey: () => number): Draft {
  const { config, inherited } = state
  return {
    defaultsEnabled: config.defaults !== undefined,
    defaultsRead: (config.defaults ?? inherited.defaults).read.join('\n'),
    defaultsWrite: (config.defaults ?? inherited.defaults).write.join('\n'),
    commandsEnabled: config.commands !== undefined,
    rules: (config.commands ?? []).map((rule) => ruleDraft(rule, nextKey())),
    network: networkChoice(config.network),
    pty: ptyChoice(config.pty),
    shell: config.shell ?? '',
    relatedProjects: (config.relatedProjects ?? []).join('\n'),
  }
}

function configFromDraft(draft: Draft, scope: 'global' | 'thread'): SandboxConfig {
  const config: SandboxConfig = {}
  if (draft.defaultsEnabled) {
    config.defaults = {
      read: splitLines(draft.defaultsRead),
      write: splitLines(draft.defaultsWrite),
    }
  }
  if (draft.commandsEnabled) {
    config.commands = draft.rules.map((rule) => ({
      patterns: splitLines(rule.patterns),
      ...(rule.filesEnabled
        ? { files: { read: splitLines(rule.read), write: splitLines(rule.write) } }
        : {}),
      ...(rule.network === 'inherit' ? {} : { network: rule.network === 'allowed' }),
    }))
  }
  if (draft.network !== 'inherit') config.network = draft.network === 'allowed'
  if (draft.pty !== 'inherit') config.pty = draft.pty === 'enabled'
  if (draft.shell.trim()) config.shell = draft.shell.trim()
  if (scope === 'thread') {
    const relatedProjects = splitLines(draft.relatedProjects)
    if (relatedProjects.length > 0) config.relatedProjects = relatedProjects
  }
  return config
}

const pathListHelp = 'The session working directory is always readable and writable. Add one extra path per line; paths may use $CWD, $HOME, $TMPDIR, and ~, and relative paths resolve against the session directory.'

export function SandboxSettingsScreen({
  scope,
  project,
  thread,
  embedded = false,
  onOpenSidebar,
  onBack,
}: SandboxSettingsScreenProps) {
  const projectId = project?.id
  const threadId = thread?.id
  const sandboxParams = scope === 'thread' && projectId && threadId
    ? { scope: 'thread' as const, projectId, threadId }
    : { scope: 'global' as const }
  const subscription = useSubscription(SandboxConfigTopic, sandboxParams)
  const [state, setState] = useState<SandboxConfigState | null>(null)
  const [loadError, setLoadError] = useState('')
  const [loading, setLoading] = useState(true)
  const [draft, setDraft] = useState<Draft | null>(null)
  const [baseline, setBaseline] = useState('')
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState('')
  const [saveMessage, setSaveMessage] = useState('')
  const [externalUpdateNotice, setExternalUpdateNotice] = useState(false)
  const ruleKeyRef = useRef(0)
  const stateRef = useRef<SandboxConfigState | null>(null)
  const draftRef = useRef<Draft | null>(null)
  const baselineRef = useRef('')
  const sandboxIdentity = SandboxConfigTopic.key(sandboxParams)
  const sandboxIdentityRef = useRef(sandboxIdentity)
  function nextRuleKey() {
    ruleKeyRef.current += 1
    return ruleKeyRef.current
  }

  function replaceEditorState(nextState: SandboxConfigState) {
    const nextDraft = draftFromState(nextState, nextRuleKey)
    const nextBaseline = JSON.stringify(configFromDraft(nextDraft, nextState.scope))
    stateRef.current = nextState
    draftRef.current = nextDraft
    baselineRef.current = nextBaseline
    setState(nextState)
    setDraft(nextDraft)
    setBaseline(nextBaseline)
    setExternalUpdateNotice(false)
  }

  useEffect(() => {
    if (sandboxIdentityRef.current === sandboxIdentity) return
    sandboxIdentityRef.current = sandboxIdentity
    stateRef.current = null
    draftRef.current = null
    baselineRef.current = ''
    setState(null)
    setDraft(null)
    setBaseline('')
    setLoadError('')
    setLoading(true)
    setExternalUpdateNotice(false)
  }, [sandboxIdentity])

  useEffect(() => {
    if (subscription.state === 'loading') {
      setLoading(true)
      return
    }
    setLoading(false)
    if (subscription.state === 'error') {
      setLoadError(subscription.error.message)
      return
    }
    setLoadError('')
    const nextState = subscription.data as SandboxConfigState
    const currentState = stateRef.current
    const currentDraft = draftRef.current
    const currentDirty = currentState !== null
      && currentDraft !== null
      && JSON.stringify(configFromDraft(currentDraft, currentState.scope)) !== baselineRef.current
    if (currentDirty && currentState.scope === nextState.scope) {
      if (JSON.stringify(currentState) !== JSON.stringify(nextState)) {
        setExternalUpdateNotice(true)
      }
      stateRef.current = nextState
      setState(nextState)
      return
    }
    replaceEditorState(nextState)
  }, [subscription])

  const inheritedLabel = scope === 'global' ? 'built-in default' : 'global config'
  const config = draft && state ? configFromDraft(draft, state.scope) : null
  const dirty = config !== null && JSON.stringify(config) !== baseline
  const emptyRule = draft?.commandsEnabled
    ? draft.rules.some((rule) => splitLines(rule.patterns).length === 0)
    : false
  const invalidShell = draft !== null && draft.shell.trim() !== '' && !draft.shell.trim().startsWith('/')

  function updateDraft(patch: Partial<Draft>) {
    setDraft((current) => {
      const next = current ? { ...current, ...patch } : current
      draftRef.current = next
      return next
    })
    setSaveError('')
    setSaveMessage('')
  }

  function updateRule(key: number, patch: Partial<RuleDraft>) {
    setDraft((current) => {
      const next = current
        ? { ...current, rules: current.rules.map((rule) => rule.key === key ? { ...rule, ...patch } : rule) }
        : current
      draftRef.current = next
      return next
    })
    setSaveError('')
    setSaveMessage('')
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!state || !config || saving || !dirty || emptyRule || invalidShell) return
    setSaving(true)
    setSaveError('')
    setSaveMessage('')
    try {
      const next = scope === 'thread' && projectId && threadId
        ? await updateThreadSandboxConfig(projectId, threadId, config)
        : await updateGlobalSandboxConfig(config)
      replaceEditorState(next)
      setSaveMessage(next.exists
        ? 'Sandbox configuration saved. It applies to newly started agent sessions.'
        : 'Sandbox configuration cleared; everything now inherits.')
    } catch (reason) {
      setSaveError(reason instanceof Error ? reason.message : 'Could not save the sandbox configuration.')
    } finally {
      setSaving(false)
    }
  }

  const title = scope === 'global' ? 'Kiwi Sandbox' : 'Thread sandbox'
  const subtitle = scope === 'global'
    ? 'Global sandbox configuration'
    : [project?.name, thread?.branch || thread?.title].filter(Boolean).join(' · ')

  const body = loading && (!state || !draft) ? (
          <LoadingPanel label="Loading sandbox configuration" />
        ) : !state || !draft ? (
          <LoadErrorPanel
            message={loadError || 'Could not load the sandbox configuration.'}
            onRetry={subscription.retry}
          />
        ) : (
          <form onSubmit={(event) => void handleSubmit(event)} className="space-y-5">
            {loadError && (
              <FeedbackMessage role="alert" tone="error">
                {loadError} The last loaded configuration is still shown; any unsaved draft has been preserved.
              </FeedbackMessage>
            )}
            {externalUpdateNotice && (
              <InfoCallout>
                The stored sandbox configuration changed while you were editing. Your unsaved draft was preserved;
                saving will replace the newer stored configuration.
              </InfoCallout>
            )}
            <Surface as="section" variant="elevated-panel" className="overflow-hidden">
              <SectionHeader
                icon={<FileJson2 size={16} />}
                title="Configuration file"
                description={scope === 'global'
                  ? 'Stored in your home directory and shared by every project.'
                  : 'Stored inside this thread’s working directory.'}
                tone="yellow"
                badge={(
                  <StatusBadge tone={state.exists ? 'success' : 'neutral'}>
                    {state.exists ? 'File exists' : 'Not created yet'}
                  </StatusBadge>
                )}
              />
              <div className="p-4 sm:p-5">
                <div className="rounded-xl border border-ghost-border/55 bg-ghost-black/25 px-3.5 py-3">
                  <p className="text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-faint">Location</p>
                  <p className="mt-1.5 break-all font-mono text-[10px] leading-4 text-ghost-muted" title={state.path}>
                    {state.path}
                  </p>
                </div>
                <InfoCallout className="mt-4">
                  Settings left on “inherit” fall back to the {inheritedLabel}. Saved changes apply to agent
                  sessions started afterwards; running sessions keep their current policy.
                </InfoCallout>
                {state.parseError && (
                  <FeedbackMessage role="alert" tone="error" className="mt-4">
                    The existing file could not be read: {state.parseError}. Saving from this page will replace it.
                  </FeedbackMessage>
                )}
                {state.globalParseError && (
                  <FeedbackMessage role="alert" tone="error" className="mt-4">
                    The global sandbox configuration could not be read: {state.globalParseError}. Inherited values
                    shown here fall back to the built-in defaults.
                  </FeedbackMessage>
                )}
              </div>
            </Surface>

            <Surface as="section" variant="elevated-panel" className="overflow-hidden">
              <SectionHeader
                icon={<Globe size={16} />}
                title="Runtime capabilities"
                description="Network, pseudo-terminal, and shell settings for sandboxed commands."
                tone="blue"
              />
              <div className="space-y-4 p-4 sm:p-5">
                <div>
                  <label
                    htmlFor="sandbox-network-select"
                    className="block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim"
                  >
                    Network access
                  </label>
                  <div className="mt-2.5 max-w-72">
                    <Select
                      id="sandbox-network-select"
                      variant="code"
                      value={draft.network}
                      options={[
                        {
                          value: 'inherit',
                          label: `Inherit (${state.inherited.network ? 'allowed' : 'blocked'})`,
                        },
                        { value: 'allowed', label: 'Allowed' },
                        { value: 'blocked', label: 'Blocked' },
                      ]}
                      onChange={(value) => updateDraft({ network: value as NetworkChoice })}
                      disabled={saving}
                      leadingIcon={<Globe size={12} />}
                    />
                  </div>
                  <p className="mt-2 text-[9px] leading-4 text-ghost-faint">
                    Individual command rules below can still allow or block the network per command.
                  </p>
                </div>
                <div>
                  <label
                    htmlFor="sandbox-pty-select"
                    className="block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim"
                  >
                    Pseudo-terminal access
                  </label>
                  <div className="mt-2.5 max-w-72">
                    <Select
                      id="sandbox-pty-select"
                      variant="code"
                      value={draft.pty}
                      options={[
                        {
                          value: 'inherit',
                          label: `Inherit (${state.inherited.pty ? 'enabled' : 'disabled'})`,
                        },
                        { value: 'enabled', label: 'Enabled' },
                        { value: 'disabled', label: 'Disabled' },
                      ]}
                      onChange={(value) => updateDraft({ pty: value as PtyChoice })}
                      disabled={saving}
                      leadingIcon={<SquareTerminal size={12} />}
                    />
                  </div>
                  <p className="mt-2 text-[9px] leading-4 text-ghost-faint">
                    Disabled by default. Enable explicitly for openpty(), forkpty(), or terminal integration tests.
                  </p>
                </div>
                <div>
                  <label
                    htmlFor="sandbox-shell-input"
                    className="block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim"
                  >
                    Shell
                    <TextInput
                      id="sandbox-shell-input"
                      variant="code-large"
                      value={draft.shell}
                      onChange={(event) => updateDraft({ shell: event.target.value })}
                      disabled={saving}
                      autoComplete="off"
                      spellCheck={false}
                      placeholder={`Inherit (${state.inherited.shell})`}
                      className="mt-2.5"
                    />
                  </label>
                  <p className="mt-2 text-[9px] leading-4 text-ghost-faint">
                    Absolute path to the shell used for sandboxed commands. Leave blank to inherit.
                  </p>
                  {invalidShell && (
                    <FeedbackMessage role="alert" tone="error" className="mt-3">
                      The shell must be an absolute path, such as /bin/zsh.
                    </FeedbackMessage>
                  )}
                </div>
              </div>
            </Surface>

            <Surface as="section" variant="elevated-panel" className="overflow-hidden">
              <SectionHeader
                icon={<FolderOpen size={16} />}
                title="File access defaults"
                description="Extra directories sandboxed commands may access in addition to their always-readable and writable working directory."
                tone="green"
                badge={(
                  <StatusBadge tone={draft.defaultsEnabled ? 'success' : 'neutral'}>
                    {draft.defaultsEnabled ? 'Customized' : 'Inherited'}
                  </StatusBadge>
                )}
              />
              <div className="p-4 sm:p-5">
                <label className="flex cursor-pointer items-start gap-3 rounded-xl border border-ghost-border/55 bg-ghost-black/25 p-3.5">
                  <input
                    type="checkbox"
                    checked={draft.defaultsEnabled}
                    disabled={saving}
                    onChange={(event) => updateDraft({ defaultsEnabled: event.target.checked })}
                    className="mt-0.5 size-4 accent-ghost-green"
                  />
                  <span>
                    <span className="block text-[10px] font-semibold text-ghost-bright-white">
                      Customize file access defaults
                    </span>
                    <span className="mt-1 block text-[9px] leading-4 text-ghost-faint">
                      When off, the {inheritedLabel} applies: read{' '}
                      <span className="font-mono">{state.inherited.defaults.read.join(', ')}</span>; write{' '}
                      <span className="font-mono">{state.inherited.defaults.write.join(', ')}</span>.
                    </span>
                  </span>
                </label>

                {draft.defaultsEnabled && (
                  <div className="mt-4 grid gap-4 sm:grid-cols-2">
                    <label className="block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim">
                      Readable paths
                      <TextArea
                        value={draft.defaultsRead}
                        onChange={(event) => updateDraft({ defaultsRead: event.target.value })}
                        disabled={saving}
                        spellCheck={false}
                        placeholder={'$CWD\n/opt/homebrew'}
                        className="mt-2.5"
                      />
                    </label>
                    <label className="block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim">
                      Writable paths
                      <TextArea
                        value={draft.defaultsWrite}
                        onChange={(event) => updateDraft({ defaultsWrite: event.target.value })}
                        disabled={saving}
                        spellCheck={false}
                        placeholder={'$CWD\n$TMPDIR'}
                        className="mt-2.5"
                      />
                    </label>
                    <p className="text-[9px] leading-4 text-ghost-faint sm:col-span-2">{pathListHelp}</p>
                  </div>
                )}
              </div>
            </Surface>

            <Surface as="section" variant="elevated-panel" className="overflow-hidden">
              <SectionHeader
                icon={<SquareTerminal size={16} />}
                title="Command rules"
                description="Per-command overrides matched against the command line, such as widening file access for a package manager."
                tone="magenta"
                badge={(
                  <StatusBadge tone={draft.commandsEnabled ? 'success' : 'neutral'}>
                    {draft.commandsEnabled
                      ? `${draft.rules.length} ${draft.rules.length === 1 ? 'rule' : 'rules'}`
                      : 'Inherited'}
                  </StatusBadge>
                )}
              />
              <div className="p-4 sm:p-5">
                <label className="flex cursor-pointer items-start gap-3 rounded-xl border border-ghost-border/55 bg-ghost-black/25 p-3.5">
                  <input
                    type="checkbox"
                    checked={draft.commandsEnabled}
                    disabled={saving}
                    onChange={(event) => updateDraft({ commandsEnabled: event.target.checked })}
                    className="mt-0.5 size-4 accent-ghost-green"
                  />
                  <span>
                    <span className="block text-[10px] font-semibold text-ghost-bright-white">
                      Customize command rules
                    </span>
                    <span className="mt-1 block text-[9px] leading-4 text-ghost-faint">
                      When off, the {inheritedLabel} applies
                      ({state.inherited.commands.length} {state.inherited.commands.length === 1 ? 'rule' : 'rules'}).
                      Turning this on replaces the inherited rules entirely.
                    </span>
                  </span>
                </label>

                {draft.commandsEnabled && (
                  <div className="mt-4 space-y-4">
                    {draft.rules.map((rule, index) => (
                      <div key={rule.key} className="rounded-xl border border-ghost-border/55 bg-ghost-black/25 p-3.5">
                        <div className="flex items-center justify-between gap-3">
                          <p className="text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-faint">
                            Rule {index + 1}
                          </p>
                          <GhostButton
                            type="button"
                            size="xs"
                            disabled={saving}
                            onClick={() => updateDraft({ rules: draft.rules.filter((candidate) => candidate.key !== rule.key) })}
                            className="flex items-center gap-1.5 text-ghost-dim hover:text-ghost-bright-red"
                          >
                            <Trash2 size={11} />
                            Remove
                          </GhostButton>
                        </div>

                        <label className="mt-2 block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim">
                          Command patterns
                          <TextArea
                            value={rule.patterns}
                            onChange={(event) => updateRule(rule.key, { patterns: event.target.value })}
                            disabled={saving}
                            spellCheck={false}
                            placeholder={'npm *\npnpm *'}
                            className="mt-2.5 min-h-16"
                          />
                        </label>
                        <p className="mt-2 text-[9px] leading-4 text-ghost-faint">
                          One glob pattern per line, matched against the whole command line.
                        </p>

                        <div className="mt-3">
                          <label
                            htmlFor={`sandbox-rule-network-${rule.key}`}
                            className="block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim"
                          >
                            Network for matching commands
                          </label>
                          <div className="mt-2.5 max-w-72">
                            <Select
                              id={`sandbox-rule-network-${rule.key}`}
                              variant="code"
                              value={rule.network}
                              options={[
                                { value: 'inherit', label: 'Use the policy above' },
                                { value: 'allowed', label: 'Allowed' },
                                { value: 'blocked', label: 'Blocked' },
                              ]}
                              onChange={(value) => updateRule(rule.key, { network: value as NetworkChoice })}
                              disabled={saving}
                              leadingIcon={<Globe size={12} />}
                            />
                          </div>
                        </div>

                        <label className="mt-3 flex cursor-pointer items-start gap-3">
                          <input
                            type="checkbox"
                            checked={rule.filesEnabled}
                            disabled={saving}
                            onChange={(event) => updateRule(rule.key, { filesEnabled: event.target.checked })}
                            className="mt-0.5 size-4 accent-ghost-green"
                          />
                          <span className="text-[10px] font-semibold text-ghost-bright-white">
                            Restrict file access for matching commands
                            <span className="mt-1 block text-[9px] font-normal leading-4 text-ghost-faint">
                              When off, matching commands run with unrestricted filesystem access.
                            </span>
                          </span>
                        </label>

                        {rule.filesEnabled && (
                          <div className="mt-3 grid gap-3 sm:grid-cols-2">
                            <label className="block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim">
                              Readable paths
                              <TextArea
                                value={rule.read}
                                onChange={(event) => updateRule(rule.key, { read: event.target.value })}
                                disabled={saving}
                                spellCheck={false}
                                placeholder="$HOME/.config/tool"
                                className="mt-2.5 min-h-16"
                              />
                            </label>
                            <label className="block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim">
                              Writable paths
                              <TextArea
                                value={rule.write}
                                onChange={(event) => updateRule(rule.key, { write: event.target.value })}
                                disabled={saving}
                                spellCheck={false}
                                placeholder="$TMPDIR/tool-cache"
                                className="mt-2.5 min-h-16"
                              />
                            </label>
                          </div>
                        )}
                        {rule.filesEnabled && (
                          <p className="mt-2 text-[9px] leading-4 text-ghost-faint">
                            These paths are additional; matching commands always retain read/write access to their working directory.
                          </p>
                        )}
                      </div>
                    ))}

                    <GhostButton
                      type="button"
                      size="sm"
                      disabled={saving}
                      onClick={() => updateDraft({
                        rules: [...draft.rules, {
                          key: nextRuleKey(),
                          patterns: '',
                          filesEnabled: false,
                          read: '',
                          write: '',
                          network: 'inherit',
                        }],
                      })}
                      className="flex items-center gap-1.5 border border-ghost-border/70"
                    >
                      <Plus size={12} />
                      Add rule
                    </GhostButton>

                    {emptyRule && (
                      <FeedbackMessage role="alert" tone="error">
                        Every command rule needs at least one pattern.
                      </FeedbackMessage>
                    )}
                  </div>
                )}
              </div>
            </Surface>

            {scope === 'thread' && (
              <Surface as="section" variant="elevated-panel" className="overflow-hidden">
                <SectionHeader
                  icon={<FolderGit2 size={16} />}
                  title="Related projects"
                  description="Extra directories the agent may read, write, and use as a working directory alongside this thread."
                  tone="green"
                />
                <div className="p-4 sm:p-5">
                  <label className="block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim">
                    Related project paths
                    <TextArea
                      value={draft.relatedProjects}
                      onChange={(event) => updateDraft({ relatedProjects: event.target.value })}
                      disabled={saving}
                      spellCheck={false}
                      placeholder={'../shared-library\n$HOME/personal/other-repo'}
                      className="mt-2.5"
                    />
                  </label>
                  <p className="mt-2 text-[9px] leading-4 text-ghost-faint">
                    {pathListHelp} Related projects are included in Pi&apos;s default file policy and passed to Claude Code with --add-dir.
                  </p>
                </div>
              </Surface>
            )}

            <Surface as="section" variant="elevated-panel" className="overflow-hidden">
              <div className="p-4 sm:p-5">
                {saveError && (
                  <FeedbackMessage role="alert" tone="error" className="mb-4">
                    {saveError}
                  </FeedbackMessage>
                )}
                {saveMessage && (
                  <FeedbackMessage role="status" tone="success" size="status" className="mb-4 flex items-center gap-2">
                    <Check size={13} />
                    {saveMessage}
                  </FeedbackMessage>
                )}
                <div className="flex items-center justify-end">
                  <PrimaryButton
                    type="submit"
                    size="md"
                    disabled={!dirty || saving || emptyRule || invalidShell}
                    className="flex min-w-40 items-center justify-center gap-2"
                  >
                    {saving ? <LoaderCircle size={14} className="animate-spin" /> : <Save size={14} />}
                    Save sandbox config
                  </PrimaryButton>
                </div>
              </div>
            </Surface>
    </form>
  )

  if (embedded) return body

  return (
    <FormScreenTemplate
      header={(
        <ScreenHeader
          title={title}
          subtitle={subtitle}
          backLabel={scope === 'global' ? 'Back to settings' : 'Back to workspace'}
          backDisabled={saving}
          onOpenSidebar={onOpenSidebar ?? (() => {})}
          onBack={onBack ?? (() => {})}
        />
      )}
    >
      <div className="relative mx-auto w-full max-w-[44rem]">
        <PageIntro icon={<Shield size={20} />} title={title}>
          {scope === 'global'
            ? 'The sandbox policy applied to every Pi session that Kiwi Code launches. Individual threads can override these values for their own git branch; Claude Code currently uses only each thread’s related-project paths.'
            : 'Sandbox overrides for this thread only. Because the thread works on its own git branch, the settings are stored inside the thread directory and travel with it. Anything you leave inherited comes from the global sandbox configuration.'}
        </PageIntro>
        {body}
      </div>
    </FormScreenTemplate>
  )
}
