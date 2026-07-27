import {
  useEffect,
  useRef,
  useState,
  type ChangeEvent,
  type ClipboardEvent,
  type DragEvent,
  type FormEvent,
  type KeyboardEvent,
} from 'react'
import {
  Bot,
  Folder,
  GitBranch,
  ImagePlus,
  LoaderCircle,
  Plus,
  X,
} from 'lucide-react'
import { createThread, uploadPiImage } from '../../api'
import {
  codingAgentSelectionForSetting,
  defaultCodingAgentSelection,
  fallbackCodingAgentConfigs,
  isClaudeGPTCodingAgent,
  isCodingAgent,
  isCodingAgentSelection,
} from '../../codingAgents'
import {
  formatImageSize,
  imageFilesFromClipboard,
  PI_IMAGE_ACCEPT,
  piNativePromptImagePolicy,
  validateImageAdditions,
} from '../../lib/promptImages'
import { classNames } from '../../lib/classNames'
import {
  readNewThreadDraft,
  readNewThreadPastes,
  writeNewThreadDraft,
  writeNewThreadPastes,
} from '../../lib/promptDrafts'
import { useImageAttachments } from '../../lib/useImageAttachments'
import {
  collapsePromptPaste,
  expandPromptPastes,
  prunePromptPastes,
} from '../../prompt-pastes.mjs'
import type {
  AppSettings,
  CodingAgent,
  CodingAgentConfig,
  CodingAgentSelection,
  CodingAgentStart,
  GitBranchState,
  Project,
  Thread,
} from '../../types'
import { useSubscription } from '../../wire/react'
import { CodingAgentsTopic, GitBranchesTopic, SettingsTopic } from '../../wire/topics'
import { GhostButton, PrimaryButton } from '../atoms/Button'
import { TextArea } from '../atoms/Input'
import { Select } from '../atoms/Select'
import { Surface } from '../atoms/Surface'
import { FeedbackMessage } from '../molecules/FeedbackMessage'
import { ScreenHeader } from '../molecules/ScreenHeader'
import { FormScreenTemplate } from '../templates/FormScreenTemplate'

export type ThreadLocation = 'project' | 'worktree'

type NewThreadScreenProps = {
  project: Project
  onOpenSidebar: () => void
  onCancel: () => void
  onCreated: (thread: Thread, start: CodingAgentStart) => void
}

const INITIAL_PROMPT_MAX_LENGTH = 12_000

// Matches the Pi composer footer treatment: dim mono label beside a borderless
// inline Select, with hairline dividers between neighbouring controls.
const inlineSettingClass = 'flex h-[26px] min-w-0 items-center gap-1 whitespace-nowrap font-mono text-[8px] text-ghost-dim'
const inlineDividerClass = 'ml-[7px] border-l border-ghost-border/55 pl-[7px]'

type AgentModelPreferences = {
  model: string
  thinkingLevel: string
}

type NewThreadPreferences = {
  location: ThreadLocation
  baseBranch: string
  codingAgent: CodingAgentSelection
  agentModels: Partial<Record<CodingAgent, AgentModelPreferences>>
}

function newThreadPreferencesStorageKey(projectId: string) {
  return `kiwi-code:new-thread-preferences:${projectId}`
}

function rememberedNewThreadPreferences(projectId: string): NewThreadPreferences | null {
  try {
    const value: unknown = JSON.parse(
      window.localStorage.getItem(newThreadPreferencesStorageKey(projectId)) ?? 'null',
    )
    if (!value || typeof value !== 'object') return null
    const candidate = value as Partial<NewThreadPreferences> & Partial<AgentModelPreferences>
    if (
      (candidate.location !== 'project' && candidate.location !== 'worktree')
      || !isCodingAgentSelection(candidate.codingAgent)
      || typeof candidate.baseBranch !== 'string'
    ) {
      return null
    }

    const agentModels: Partial<Record<CodingAgent, AgentModelPreferences>> = {}
    if (candidate.agentModels && typeof candidate.agentModels === 'object') {
      for (const [agent, preferences] of Object.entries(candidate.agentModels)) {
        if (
          isCodingAgent(agent)
          && preferences
          && typeof preferences.model === 'string'
          && typeof preferences.thinkingLevel === 'string'
        ) {
          agentModels[agent] = preferences
        }
      }
    }

    // Migrate preferences saved before model settings were remembered per agent.
    if (typeof candidate.model === 'string' && typeof candidate.thinkingLevel === 'string') {
      const agent = codingAgentIdForSelection(candidate.codingAgent)
      agentModels[agent] ??= {
        model: candidate.model,
        thinkingLevel: candidate.thinkingLevel,
      }
    }

    return {
      location: candidate.location,
      baseBranch: candidate.baseBranch,
      codingAgent: candidate.codingAgent,
      agentModels,
    }
  } catch {
    // Storage can be unavailable or contain stale data. The form defaults are
    // still usable for this visit.
    return null
  }
}

function rememberNewThreadPreferences(projectId: string, preferences: NewThreadPreferences) {
  try {
    window.localStorage.setItem(
      newThreadPreferencesStorageKey(projectId),
      JSON.stringify(preferences),
    )
  } catch {
    // A successful thread creation should not fail just because the browser
    // blocks persistent storage.
  }
}

function initialPromptWithImages(prompt: string, imagePaths: string[]) {
  return [prompt, imagePaths.join('\n')].filter(Boolean).join('\n\n')
}

function codingAgentIdForSelection(selection: CodingAgentSelection): CodingAgent {
  if (selection === 'pi-native') return 'pi'
  if (selection === 'claude-native') return 'claude'
  return selection
}

export function NewThreadScreen({
  project,
  onOpenSidebar,
  onCancel,
  onCreated,
}: NewThreadScreenProps) {
  const codingAgentsSubscription = useSubscription(CodingAgentsTopic, { projectId: project.id })
  const settingsSubscription = useSubscription(SettingsTopic, undefined)
  const branchesSubscription = useSubscription(
    GitBranchesTopic,
    { projectId: project.id },
    { enabled: project.isGitRepo },
  )
  const settingsInitializedRef = useRef(false)
  const [rememberedPreferences] = useState(() => rememberedNewThreadPreferences(project.id))
  const [location, setLocation] = useState<ThreadLocation>(() => {
    if (!project.isGitRepo) return 'project'
    return rememberedPreferences?.location ?? 'worktree'
  })
  const [baseBranch, setBaseBranch] = useState(rememberedPreferences?.baseBranch ?? '')
  const [branchState, setBranchState] = useState<GitBranchState | null>(null)
  const [branchesLoading, setBranchesLoading] = useState(project.isGitRepo)
  const [branchLoadError, setBranchLoadError] = useState('')
  const [codingAgents, setCodingAgents] = useState<CodingAgentConfig[]>(fallbackCodingAgentConfigs)
  const [codingAgentsLoading, setCodingAgentsLoading] = useState(true)
  const [codingAgentsError, setCodingAgentsError] = useState('')
  const [codingAgent, setCodingAgent] = useState<CodingAgentSelection>(
    rememberedPreferences?.codingAgent ?? 'pi-native',
  )
  const [agentModels, setAgentModels] = useState<Partial<Record<CodingAgent, AgentModelPreferences>>>(
    rememberedPreferences?.agentModels ?? {},
  )
  const initialAgentModel = agentModels[codingAgentIdForSelection(codingAgent)]
  const [model, setModel] = useState(initialAgentModel?.model ?? '')
  const [thinkingLevel, setThinkingLevel] = useState(initialAgentModel?.thinkingLevel ?? '')
  const settings: AppSettings | null = settingsSubscription.state === 'ready'
    ? settingsSubscription.data
    : null
  const settingsLoading = settingsSubscription.state === 'loading'
  const [nestedDepth, setNestedDepth] = useState<number | 'inherit'>('inherit')
  const [initialPrompt, setInitialPrompt] = useState(() => readNewThreadDraft(project.id))
  const [initialPromptPastes, setInitialPromptPastes] = useState(() => (
    readNewThreadPastes(project.id)
  ))
  const {
    attachments: initialPromptImages,
    addFiles: addInitialPromptImageFiles,
    removeAttachment: removeInitialPromptImage,
  } = useImageAttachments()
  const [submitting, setSubmitting] = useState(false)
  const [uploadingImages, setUploadingImages] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    writeNewThreadDraft(project.id, initialPrompt)
    writeNewThreadPastes(project.id, initialPromptPastes)
  }, [initialPrompt, initialPromptPastes, project.id])

  useEffect(() => {
    if (codingAgentsSubscription.state === 'loading') {
      setCodingAgentsLoading(true)
      return
    }
    setCodingAgentsLoading(false)
    if (codingAgentsSubscription.state === 'error') {
      setCodingAgentsError(codingAgentsSubscription.error.message)
      return
    }
    setCodingAgentsError('')
    const configs = codingAgentsSubscription.data as CodingAgentConfig[]
    if (configs.length === 0) return
    setCodingAgents(configs)
    setCodingAgent((current) => configs.some((config) =>
      config.id === codingAgentIdForSelection(current)) ? current : 'pi-native')
  }, [codingAgentsSubscription])

  useEffect(() => {
    if (settingsSubscription.state !== 'ready' || settingsInitializedRef.current) return
    settingsInitializedRef.current = true
    const next = settingsSubscription.data
    const availableAgents = next.codingAgents.map(codingAgentSelectionForSetting)
    const configuredDefault = defaultCodingAgentSelection(next.codingAgents)
    const nextAgent = availableAgents.includes(configuredDefault) ? configuredDefault : 'pi-native'
    const rememberedModel = agentModels[codingAgentIdForSelection(nextAgent)]
    setCodingAgent(nextAgent)
    setModel(rememberedModel?.model ?? '')
    setThinkingLevel(rememberedModel?.thinkingLevel ?? '')
  }, [agentModels, settingsSubscription])

  useEffect(() => {
    if (codingAgentsLoading) return
    const agentId = codingAgentIdForSelection(codingAgent)
    const config = codingAgents.find((agent) => agent.id === agentId)
      ?? fallbackCodingAgentConfigs.find((agent) => agent.id === agentId)
    if (!config) return
    const nextModel = config.models.some((option) => option.id === model)
      ? model
      : config.models[0]?.id ?? ''
    const nextThinkingLevel = config.thinkingLevels.some((option) => option.id === thinkingLevel)
      ? thinkingLevel
      : config.thinkingLevels[0]?.id ?? ''
    if (nextModel === model && nextThinkingLevel === thinkingLevel) return

    setModel(nextModel)
    setThinkingLevel(nextThinkingLevel)
    setAgentModels((current) => ({
      ...current,
      [agentId]: { model: nextModel, thinkingLevel: nextThinkingLevel },
    }))
  }, [codingAgent, codingAgents, codingAgentsLoading, model, thinkingLevel])

  useEffect(() => {
    if (!project.isGitRepo) {
      setBranchesLoading(false)
      return
    }
    if (branchesSubscription.state === 'loading') {
      setBranchesLoading(true)
      return
    }
    setBranchesLoading(false)
    if (branchesSubscription.state === 'error') {
      setBranchLoadError(branchesSubscription.error.message)
      return
    }
    const next = branchesSubscription.data as GitBranchState
    setBranchState(next)
    if (!next.isRepository) {
      setBaseBranch('')
      setBranchLoadError('This project is no longer inside a Git repository.')
      return
    }
    setBranchLoadError('')
    setBaseBranch((current) => {
      if (next.branches.some((branch) => branch.name === current)) return current
      if (!next.detached && next.branches.some((branch) => branch.name === next.current)) {
        return next.current
      }
      return next.branches[0]?.name ?? ''
    })
  }, [branchesSubscription, project.isGitRepo])

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (submitting) return

    const prompt = expandPromptPastes(initialPrompt, initialPromptPastes).trim()
    const creatingWorktree = location === 'worktree'
    if (creatingWorktree && !baseBranch) {
      setError('Select a base branch for the new worktree.')
      return
    }
    if (codingAgent === 'pi-native' || codingAgent === 'claude-native') {
      const validation = validateImageAdditions(
        [],
        initialPromptImages.map(({ file }) => file),
        piNativePromptImagePolicy,
      )
      if (validation.error) {
        setError(validation.error)
        return
      }
    }

    setSubmitting(true)
    setUploadingImages(initialPromptImages.length > 0)
    setError('')
    try {
      const imagePaths = await Promise.all(initialPromptImages.map(async ({ file }) => {
        const upload = await uploadPiImage(project.id, file)
        return upload.path
      }))
      setUploadingImages(false)
      const nativeAgent = codingAgent === 'pi-native' || codingAgent === 'claude-native'
      const firstTask = nativeAgent ? prompt : initialPromptWithImages(prompt, imagePaths)
      const thread = await createThread(project.id, {
        worktree: creatingWorktree,
        baseBranch: creatingWorktree ? baseBranch : undefined,
        nestedDepth: nestedDepth === 'inherit' ? undefined : nestedDepth,
      })
      rememberNewThreadPreferences(project.id, {
        location,
        baseBranch,
        codingAgent,
        agentModels: {
          ...agentModels,
          [codingAgentIdForSelection(codingAgent)]: { model, thinkingLevel },
        },
      })
      writeNewThreadDraft(project.id, '')
      writeNewThreadPastes(project.id, [])
      onCreated(thread, {
        agent: codingAgentIdForSelection(codingAgent),
        presentation: nativeAgent ? 'native' : 'terminal',
        model,
        thinkingLevel,
        prompt: firstTask,
        imagePaths: nativeAgent && imagePaths.length > 0 ? imagePaths : undefined,
      })
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not create that thread.')
      setUploadingImages(false)
      setSubmitting(false)
    }
  }

  function addInitialPromptImages(files: File[]) {
    if (files.length === 0 || submitting) return
    setError(addInitialPromptImageFiles(
      files,
      codingAgent === 'pi-native' ? piNativePromptImagePolicy : undefined,
    ))
  }

  function handleImageInput(event: ChangeEvent<HTMLInputElement>) {
    addInitialPromptImages(Array.from(event.target.files ?? []))
    event.target.value = ''
  }

  function handleInitialPromptPaste(event: ClipboardEvent<HTMLTextAreaElement>) {
    addInitialPromptImages(imageFilesFromClipboard(event.clipboardData))
    const pastedText = event.clipboardData.getData('text/plain')
    if (!pastedText) return
    const textarea = event.currentTarget
    const collapsed = collapsePromptPaste({
      value: initialPrompt,
      selectionStart: textarea.selectionStart,
      selectionEnd: textarea.selectionEnd,
      pastedText,
      pastes: initialPromptPastes,
      maxExpandedLength: INITIAL_PROMPT_MAX_LENGTH,
    })
    if (!collapsed) return

    event.preventDefault()
    setInitialPrompt(collapsed.value)
    setInitialPromptPastes(collapsed.pastes)
    setError('')
    window.requestAnimationFrame(() => {
      textarea.setSelectionRange(collapsed.selectionStart, collapsed.selectionStart)
    })
  }

  function handleInitialPromptChange(value: string) {
    const nextPastes = prunePromptPastes(value, initialPromptPastes)
    if (expandPromptPastes(value, nextPastes).length > INITIAL_PROMPT_MAX_LENGTH) {
      setError(`Initial prompts are limited to ${INITIAL_PROMPT_MAX_LENGTH.toLocaleString()} characters.`)
      return
    }
    setInitialPrompt(value)
    setInitialPromptPastes(nextPastes)
    setError('')
  }

  function handleInitialPromptDrop(event: DragEvent<HTMLTextAreaElement>) {
    const images = Array.from(event.dataTransfer.files)
      .filter((file) => file.type.startsWith('image/'))
    if (images.length === 0) return
    event.preventDefault()
    addInitialPromptImages(images)
  }

  const effectiveNestingDepth = project.subAgentNestingDepthOverride ?? settings?.subAgentNestingDepth ?? null
  const nestedDepthOptions = effectiveNestingDepth === null
    ? []
    : Array.from({ length: effectiveNestingDepth + 1 }, (_, index) => index)

  useEffect(() => {
    if (effectiveNestingDepth === null) return
    setNestedDepth((current) => current === 'inherit' || current <= effectiveNestingDepth
      ? current
      : effectiveNestingDepth)
  }, [effectiveNestingDepth])

  const configuredAgentOptions = settings?.codingAgents.map((agent) => ({
    value: codingAgentSelectionForSetting(agent),
    label: agent.name,
  })) ?? [
    { value: 'pi' as CodingAgentSelection, label: 'Pi' },
    { value: 'pi-native' as CodingAgentSelection, label: 'Pi Native' },
    { value: 'codex' as CodingAgentSelection, label: 'Codex CLI' },
  ]
  const selectedAgentId = codingAgentIdForSelection(codingAgent)
  const selectedAgent = codingAgents.find((agent) => agent.id === selectedAgentId)
    ?? fallbackCodingAgentConfigs[0]
  const selectedAgentLabel = codingAgent === 'pi-native'
    ? 'Pi Native'
    : codingAgent === 'claude-native'
      ? 'Claude Native'
      : selectedAgent.label
  const startsAgent = Boolean(initialPrompt.trim() || initialPromptImages.length > 0)
  const selectedAgentModelsUnavailable = isClaudeGPTCodingAgent(selectedAgentId)
    && selectedAgent.models.length === 0
  const agentNamesThread = selectedAgentId !== 'codex'
  const submitDisabled = submitting
    || selectedAgentModelsUnavailable
    || (location === 'worktree' && (branchesLoading || Boolean(branchLoadError) || !baseBranch))
  const modelSelectOptions = selectedAgent.models.map((option) => ({
    value: option.id,
    label: option.label,
  }))
  const thinkingSelectOptions = selectedAgent.thinkingLevels.map((option) => ({
    value: option.id,
    label: option.label,
  }))
  // One flat dropdown covers both choices: the project folder, or a worktree
  // from any local branch. Picking a branch selects the worktree location too.
  const workdirValue = location === 'project' ? 'project' : `worktree:${baseBranch}`
  const workdirOptions = [
    { value: 'project', label: 'project folder' },
    ...(project.isGitRepo
      ? (branchState?.branches ?? []).map((branch) => ({
          value: `worktree:${branch.name}`,
          label: `worktree · ${branch.name}${branch.current ? ' (current)' : ''}`,
        }))
      : []),
  ]
  if (!workdirOptions.some((option) => option.value === workdirValue)) {
    workdirOptions.unshift({
      value: workdirValue,
      label: `worktree · ${branchesLoading ? 'loading…' : 'select branch'}`,
    })
  }
  const settingsNotice = selectedAgentModelsUnavailable
    ? 'CLIProxyAPI did not return any GPT models. Make sure it is running and its client key is configured.'
    : codingAgentsError
      ? 'Could not refresh available models. Agent defaults and built-in choices are still available.'
      : location === 'worktree' && branchLoadError
        ? branchLoadError
        : ''

  function handleInitialPromptKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (
      event.key !== 'Enter'
      || (!event.metaKey && !event.ctrlKey)
      || event.altKey
      || event.shiftKey
      || event.nativeEvent.isComposing
    ) return

    event.preventDefault()
    if (event.repeat || submitDisabled) return
    event.currentTarget.form?.requestSubmit()
  }

  function handleCodingAgentChange(nextAgent: CodingAgentSelection) {
    const nextAgentId = codingAgentIdForSelection(nextAgent)
    const currentAgentId = codingAgentIdForSelection(codingAgent)
    const nextConfig = codingAgents.find((agent) => agent.id === nextAgentId)
      ?? fallbackCodingAgentConfigs.find((agent) => agent.id === nextAgentId)
    const nextAgentModels: Partial<Record<CodingAgent, AgentModelPreferences>> = {
      ...agentModels,
      [currentAgentId]: { model, thinkingLevel },
    }
    setAgentModels(nextAgentModels)
    setCodingAgent(nextAgent)
    if (nextAgentId !== currentAgentId) {
      const remembered = nextAgentModels[nextAgentId]
      setModel(remembered?.model ?? nextConfig?.models[0]?.id ?? '')
      setThinkingLevel(remembered?.thinkingLevel ?? nextConfig?.thinkingLevels[0]?.id ?? '')
    }
    setError('')
  }

  function handleWorkdirChange(value: string) {
    if (value === 'project') {
      setLocation('project')
    } else if (value.startsWith('worktree:')) {
      setLocation('worktree')
      const branch = value.slice('worktree:'.length)
      if (branch) setBaseBranch(branch)
    }
    setError('')
  }

  function handleModelChange(nextModel: string) {
    setModel(nextModel)
    setAgentModels((current) => ({
      ...current,
      [selectedAgentId]: { model: nextModel, thinkingLevel },
    }))
  }

  function handleThinkingLevelChange(nextThinkingLevel: string) {
    setThinkingLevel(nextThinkingLevel)
    setAgentModels((current) => ({
      ...current,
      [selectedAgentId]: { model, thinkingLevel: nextThinkingLevel },
    }))
  }

  return (
    <FormScreenTemplate
      header={(
        <ScreenHeader
          title="New thread"
          subtitle={project.name}
          backLabel="Cancel new thread"
          backDisabled={submitting}
          onOpenSidebar={onOpenSidebar}
          onBack={onCancel}
        />
      )}
    >
      <form
        onSubmit={(event) => void handleSubmit(event)}
        className="relative mx-auto w-full max-w-[38rem]"
      >
        <Surface variant="elevated-panel" className="p-4 sm:p-5">
          <div className="flex min-w-0 flex-wrap items-center gap-y-1 border-b border-ghost-border/45 px-0.5 pb-2">
            <div className="flex h-[26px] min-w-0 items-center">
              <Select
                id="thread-coding-agent"
                variant="inline"
                aria-label="Coding agent"
                value={codingAgent}
                options={configuredAgentOptions}
                onChange={(agent) => handleCodingAgentChange(agent as CodingAgentSelection)}
                disabled={submitting}
                leadingIcon={<Bot size={11} />}
              />
            </div>
            <label className={classNames(inlineSettingClass, inlineDividerClass)}>
              <span>Model</span>
              <Select
                id="thread-agent-model"
                variant="inline"
                value={model}
                options={modelSelectOptions.some((option) => option.value === model)
                  ? modelSelectOptions
                  : [{ value: model, label: 'Select model' }, ...modelSelectOptions]}
                onChange={handleModelChange}
                disabled={submitting || selectedAgentModelsUnavailable}
                style={{ maxWidth: '7.5rem' }}
              />
            </label>
            <label className={classNames(inlineSettingClass, inlineDividerClass)}>
              <span>Thinking</span>
              <Select
                id="thread-agent-thinking"
                variant="inline"
                value={thinkingLevel}
                options={thinkingSelectOptions.some((option) => option.value === thinkingLevel)
                  ? thinkingSelectOptions
                  : [{ value: thinkingLevel, label: 'Default' }, ...thinkingSelectOptions]}
                onChange={handleThinkingLevelChange}
                disabled={submitting}
                style={{ maxWidth: '90px' }}
              />
            </label>
            <div className={classNames('flex h-[26px] min-w-0 items-center', inlineDividerClass)}>
              <Select
                id="thread-working-directory"
                variant="inline"
                aria-label="Working directory"
                value={workdirValue}
                options={workdirOptions}
                onChange={handleWorkdirChange}
                disabled={submitting || !project.isGitRepo}
                leadingIcon={location === 'worktree' ? <GitBranch size={11} /> : <Folder size={11} />}
                style={{ maxWidth: '10rem' }}
              />
            </div>
          </div>

          {settingsNotice && (
            <p className="mt-2 text-[9px] leading-4 text-ghost-faint">
              {settingsNotice}
              {location === 'worktree' && Boolean(branchLoadError) && (
                <button
                  type="button"
                  onClick={branchesSubscription.retry}
                  className="ml-2 font-medium text-ghost-muted underline transition hover:text-ghost-bright-white"
                >
                  Retry
                </button>
              )}
            </p>
          )}

          <label htmlFor="thread-initial-prompt" className="sr-only">
            Initial prompt (optional)
          </label>
          <TextArea
            id="thread-initial-prompt"
            value={initialPrompt}
            onChange={(event) => handleInitialPromptChange(event.target.value)}
            onPaste={handleInitialPromptPaste}
            onKeyDown={handleInitialPromptKeyDown}
            onDragOver={(event) => event.preventDefault()}
            onDrop={handleInitialPromptDrop}
            disabled={submitting}
            rows={6}
            maxLength={INITIAL_PROMPT_MAX_LENGTH}
            placeholder="Describe what you want to build, investigate, or change…"
            aria-describedby="thread-initial-prompt-help"
            className="mt-3 min-h-40"
            autoFocus
          />

          {initialPromptImages.length > 0 && (
            <ul className="mt-2 grid gap-2 sm:grid-cols-2" aria-label="Attached images">
              {initialPromptImages.map((image) => (
                <li
                  key={image.id}
                  className="flex min-w-0 items-center gap-2.5 rounded-lg border border-ghost-border/70 bg-ghost-black/35 p-2"
                >
                  <img
                    src={image.previewUrl}
                    alt=""
                    className="size-11 shrink-0 rounded-md border border-ghost-border/65 object-cover"
                  />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-[10px] text-ghost-bright-white" title={image.file.name}>
                      {image.file.name || 'Pasted image'}
                    </span>
                    <span className="mt-0.5 block text-[9px] text-ghost-faint">
                      {formatImageSize(image.file.size)}
                    </span>
                  </span>
                  <button
                    type="button"
                    onClick={() => {
                      if (submitting) return
                      removeInitialPromptImage(image.id)
                      setError('')
                    }}
                    disabled={submitting}
                    aria-label={`Remove ${image.file.name || 'pasted image'}`}
                    className="grid size-7 shrink-0 place-items-center rounded-md text-ghost-faint transition hover:bg-ghost-raised hover:text-ghost-bright-white disabled:cursor-not-allowed disabled:opacity-40"
                  >
                    <X size={13} />
                  </button>
                </li>
              ))}
            </ul>
          )}

          <div className="mt-1.5 flex min-w-0 flex-wrap items-center gap-y-1 px-0.5">
            <label
              title="Paste or drop images into the prompt · PNG, JPEG, GIF, WebP · 50 MB max"
              className={classNames(
                'flex h-6 items-center gap-1 rounded-[5px] px-1 font-mono text-[9px] transition',
                submitting
                  ? 'cursor-not-allowed text-ghost-muted opacity-55'
                  : 'cursor-pointer text-ghost-muted hover:bg-ghost-raised/70 hover:text-ghost-bright-white',
              )}
            >
              <ImagePlus size={11} className="text-ghost-green" />
              Add images
              <input
                type="file"
                accept={PI_IMAGE_ACCEPT}
                multiple
                disabled={submitting}
                onChange={handleImageInput}
                className="sr-only"
              />
            </label>
            <label
              title="Controls how deeply child agents from this thread may delegate. It can reduce, but not exceed, the project limit."
              className={classNames(inlineSettingClass, inlineDividerClass)}
            >
              <span>Depth</span>
              <Select
                id="thread-sub-agent-depth"
                variant="inline"
                value={nestedDepth === 'inherit' ? nestedDepth : String(nestedDepth)}
                options={[
                  {
                    value: 'inherit',
                    label: effectiveNestingDepth === null
                      ? 'Use project setting'
                      : `Use project limit (${effectiveNestingDepth})`,
                  },
                  ...nestedDepthOptions.map((depth) => ({
                    value: String(depth),
                    label: depth === 0
                      ? 'Disabled'
                      : `${depth} ${depth === 1 ? 'child level' : 'child levels'}`,
                  })),
                ]}
                onChange={(value) => {
                  setNestedDepth(value === 'inherit' ? 'inherit' : Number(value))
                  setError('')
                }}
                disabled={submitting || (settingsLoading && effectiveNestingDepth === null)}
              />
            </label>
            <span className="ml-auto pl-3 font-mono text-[8px] text-ghost-faint">⌘Enter to create</span>
          </div>

          <p id="thread-initial-prompt-help" className="mt-2 px-0.5 text-[9px] leading-4 text-ghost-faint">
            {agentNamesThread
              ? `${selectedAgentLabel} uses the first prompt to name the thread${location === 'worktree' ? ' and its branch' : ''}. `
              : ''}
            Leave it blank to open {selectedAgentLabel} without a task.
          </p>

          {error && (
            <FeedbackMessage role="alert" tone="error" className="mt-3">
              {error}
            </FeedbackMessage>
          )}

          <div className="mt-6 flex items-center justify-end gap-2 border-t border-ghost-border/55 pt-4">
            <GhostButton
              type="button"
              size="md"
              onClick={onCancel}
              disabled={submitting}
              className="px-3.5 disabled:opacity-40"
            >
              Cancel
            </GhostButton>
            <PrimaryButton
              type="submit"
              size="md"
              disabled={submitDisabled}
              className="flex min-w-36 items-center justify-center gap-2"
            >
              {submitting
                ? <LoaderCircle size={14} className="animate-spin" />
                : startsAgent ? <Bot size={14} /> : <Plus size={14} />}
              {submitting
                ? uploadingImages
                  ? initialPromptImages.length === 1 ? 'Uploading image…' : 'Uploading images…'
                  : location === 'worktree'
                    ? 'Creating worktree…'
                    : startsAgent ? 'Starting agent…' : 'Creating thread…'
                : startsAgent ? `Start ${selectedAgentLabel}` : 'Create thread'}
            </PrimaryButton>
          </div>
        </Surface>
      </form>
    </FormScreenTemplate>
  )
}
