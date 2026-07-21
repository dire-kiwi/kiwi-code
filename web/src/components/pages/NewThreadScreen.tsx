import {
  useEffect,
  useState,
  type ChangeEvent,
  type ClipboardEvent,
  type DragEvent,
  type FormEvent,
  type KeyboardEvent,
} from 'react'
import {
  Bot,
  ImagePlus,
  LoaderCircle,
  Network,
  Plus,
  X,
} from 'lucide-react'
import { createThread, getSettings, listCodingAgents, listProjectGitBranches, uploadPiImage } from '../../api'
import { fallbackCodingAgentConfigs } from '../../codingAgents'
import {
  formatImageSize,
  imageFilesFromClipboard,
  PI_IMAGE_ACCEPT,
  piNativePromptImagePolicy,
  validateImageAdditions,
} from '../../lib/promptImages'
import { readNewThreadDraft, writeNewThreadDraft } from '../../lib/promptDrafts'
import { useImageAttachments } from '../../lib/useImageAttachments'
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
import { GhostButton, PrimaryButton } from '../atoms/Button'
import { TextArea } from '../atoms/Input'
import { Select } from '../atoms/Select'
import { Surface } from '../atoms/Surface'
import { AgentModelControls } from '../molecules/AgentModelControls'
import { FeedbackMessage } from '../molecules/FeedbackMessage'
import { PageIntro } from '../molecules/PageIntro'
import { ScreenHeader } from '../molecules/ScreenHeader'
import {
  ThreadLocationPicker,
  type ThreadLocation,
} from '../organisms/ThreadLocationPicker'
import { FormScreenTemplate } from '../templates/FormScreenTemplate'

type NewThreadScreenProps = {
  project: Project
  onOpenSidebar: () => void
  onCancel: () => void
  onCreated: (thread: Thread, start: CodingAgentStart) => void
}

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
      || (candidate.codingAgent !== 'pi'
        && candidate.codingAgent !== 'pi-native'
        && candidate.codingAgent !== 'claude'
        && candidate.codingAgent !== 'claude-gpt'
        && candidate.codingAgent !== 'claude-native')
      || typeof candidate.baseBranch !== 'string'
    ) {
      return null
    }

    const agentModels: Partial<Record<CodingAgent, AgentModelPreferences>> = {}
    if (candidate.agentModels && typeof candidate.agentModels === 'object') {
      for (const agent of ['pi', 'claude', 'claude-gpt'] as const) {
        const preferences = candidate.agentModels[agent]
        if (
          preferences
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
  const [rememberedPreferences] = useState(() => rememberedNewThreadPreferences(project.id))
  const [location, setLocation] = useState<ThreadLocation>(() => {
    if (!project.isGitRepo) return 'project'
    return rememberedPreferences?.location ?? 'worktree'
  })
  const [baseBranch, setBaseBranch] = useState(rememberedPreferences?.baseBranch ?? '')
  const [branchState, setBranchState] = useState<GitBranchState | null>(null)
  const [branchesLoading, setBranchesLoading] = useState(project.isGitRepo)
  const [branchLoadError, setBranchLoadError] = useState('')
  const [branchReload, setBranchReload] = useState(0)
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
  const [settings, setSettings] = useState<AppSettings | null>(null)
  const [settingsLoading, setSettingsLoading] = useState(true)
  const [settingsError, setSettingsError] = useState('')
  const [nestedDepth, setNestedDepth] = useState<number | 'inherit'>('inherit')
  const [initialPrompt, setInitialPrompt] = useState(() => readNewThreadDraft(project.id))
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
  }, [initialPrompt, project.id])

  useEffect(() => {
    const controller = new AbortController()
    setCodingAgentsLoading(true)
    setCodingAgentsError('')
    listCodingAgents(controller.signal, project.id)
      .then((configs) => {
        if (controller.signal.aborted || configs.length === 0) return
        setCodingAgents(configs)
      })
      .catch((reason) => {
        if (controller.signal.aborted) return
        setCodingAgentsError(reason instanceof Error ? reason.message : 'Could not load coding-agent models.')
      })
      .finally(() => {
        if (!controller.signal.aborted) setCodingAgentsLoading(false)
      })
    return () => controller.abort()
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    setSettingsLoading(true)
    setSettingsError('')
    getSettings(controller.signal)
      .then((next) => {
        if (!controller.signal.aborted) setSettings(next)
      })
      .catch((reason) => {
        if (controller.signal.aborted) return
        setSettingsError(reason instanceof Error ? reason.message : 'Could not load sub-agent settings.')
      })
      .finally(() => {
        if (!controller.signal.aborted) setSettingsLoading(false)
      })
    return () => controller.abort()
  }, [])

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

    const controller = new AbortController()
    setBranchesLoading(true)
    setBranchLoadError('')
    listProjectGitBranches(project.id, controller.signal)
      .then((next) => {
        if (controller.signal.aborted) return
        setBranchState(next)
        if (!next.isRepository) {
          setBaseBranch('')
          setBranchLoadError('This project is no longer inside a Git repository.')
          return
        }
        setBaseBranch((current) => {
          if (next.branches.some((branch) => branch.name === current)) return current
          if (!next.detached && next.branches.some((branch) => branch.name === next.current)) {
            return next.current
          }
          return next.branches[0]?.name ?? ''
        })
      })
      .catch((reason) => {
        if (controller.signal.aborted) return
        setBranchLoadError(reason instanceof Error ? reason.message : 'Could not load Git branches.')
      })
      .finally(() => {
        if (!controller.signal.aborted) setBranchesLoading(false)
      })
    return () => controller.abort()
  }, [branchReload, project.id, project.isGitRepo])

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (submitting) return

    const prompt = initialPrompt.trim()
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

  const selectedAgentId = codingAgentIdForSelection(codingAgent)
  const selectedAgent = codingAgents.find((agent) => agent.id === selectedAgentId)
    ?? fallbackCodingAgentConfigs[0]
  const selectedAgentLabel = codingAgent === 'pi-native'
    ? 'Pi Native'
    : codingAgent === 'claude-native'
      ? 'Claude Native'
      : selectedAgent.label
  const startsAgent = Boolean(initialPrompt.trim() || initialPromptImages.length > 0)
  const selectedAgentModelsUnavailable = selectedAgentId === 'claude-gpt'
    && selectedAgent.models.length === 0
  const submitDisabled = submitting
    || selectedAgentModelsUnavailable
    || (location === 'worktree' && (branchesLoading || Boolean(branchLoadError) || !baseBranch))

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
    const nextAgentModels = {
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
        <PageIntro icon={<Plus size={20} />} title="Start a new thread">
          Choose where the thread should work, then optionally give a coding agent its first task.
        </PageIntro>

        <Surface variant="elevated-panel" className="p-4 sm:p-5">
          <div className="rounded-xl border border-ghost-green/20 bg-ghost-green/[0.05] px-3.5 py-3">
            <p className="text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-green">Automatic naming</p>
            <p className="mt-1.5 text-[10px] leading-4 text-ghost-muted">
              When you send the first prompt, {selectedAgentLabel} uses it to generate a concise title and, for a worktree, a matching branch name.
            </p>
          </div>

          <fieldset className="mt-6">
            <legend className="text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim">
              Coding agent
            </legend>

            <label htmlFor="thread-coding-agent" className="mt-2.5 block text-xs font-medium text-ghost-bright-white">
              Agent
            </label>
            <div className="mt-2">
              <Select
                id="thread-coding-agent"
                value={codingAgent}
                options={[
                  ...codingAgents.map((agent) => ({ value: agent.id, label: agent.label })),
                  { value: 'pi-native', label: 'Pi Native' },
                  { value: 'claude-native', label: 'Claude Native' },
                ]}
                onChange={(agent) => handleCodingAgentChange(agent as CodingAgentSelection)}
                disabled={submitting}
                leadingIcon={<Bot size={12} />}
              />
            </div>

            <AgentModelControls
              variant="form"
              className="mt-4 grid gap-3 sm:grid-cols-2"
              modelId="thread-agent-model"
              thinkingId="thread-agent-thinking"
              model={model}
              modelOptions={selectedAgent.models.map((option) => ({ value: option.id, label: option.label }))}
              modelDisabled={submitting || selectedAgentModelsUnavailable}
              onModelChange={handleModelChange}
              thinking={thinkingLevel}
              thinkingOptions={selectedAgent.thinkingLevels.map((option) => ({ value: option.id, label: option.label }))}
              thinkingDisabled={submitting}
              onThinkingChange={handleThinkingLevelChange}
            />

            <p className="mt-2 text-[9px] leading-4 text-ghost-faint">
              {codingAgentsLoading
                ? 'Loading models available to your coding agents…'
                : selectedAgentModelsUnavailable
                  ? 'CLIProxyAPI did not return any GPT models. Make sure it is running and its client key is configured.'
                  : codingAgentsError
                    ? 'Could not refresh available models. Agent defaults and built-in choices are still available.'
                    : 'Model and thinking settings apply when this thread starts the agent.'}
            </p>
          </fieldset>

          <fieldset className="mt-6">
            <legend className="text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim">
              Sub-agent delegation
            </legend>
            <label htmlFor="thread-sub-agent-depth" className="mt-2.5 block text-xs font-medium text-ghost-bright-white">
              Maximum depth
            </label>
            <div className="mt-2">
              <Select
                id="thread-sub-agent-depth"
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
                aria-describedby="thread-sub-agent-depth-help"
                leadingIcon={<Network size={12} />}
              />
            </div>
            <p id="thread-sub-agent-depth-help" className="mt-2 text-[9px] leading-4 text-ghost-faint">
              {settingsError && effectiveNestingDepth === null
                ? 'Could not load the project limit. This thread can still inherit the project setting.'
                : 'Controls how deeply child agents from this thread may delegate. It can reduce, but not exceed, the project limit.'}
            </p>
          </fieldset>

          <ThreadLocationPicker
            project={project}
            value={location}
            disabled={submitting}
            baseBranch={baseBranch}
            branchState={branchState}
            branchesLoading={branchesLoading}
            branchLoadError={branchLoadError}
            onChange={setLocation}
            onBaseBranchChange={setBaseBranch}
            onReloadBranches={() => setBranchReload((current) => current + 1)}
          />

          <fieldset className="mt-6">
            <legend className="text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim">
              First task
            </legend>
            <label htmlFor="thread-initial-prompt" className="mt-2.5 block text-xs font-medium text-ghost-bright-white">
              Initial prompt <span className="font-normal text-ghost-faint">(optional)</span>
            </label>
            <TextArea
              id="thread-initial-prompt"
              value={initialPrompt}
              onChange={(event) => {
                setInitialPrompt(event.target.value)
                setError('')
              }}
              onPaste={handleInitialPromptPaste}
              onKeyDown={handleInitialPromptKeyDown}
              onDragOver={(event) => event.preventDefault()}
              onDrop={handleInitialPromptDrop}
              disabled={submitting}
              rows={5}
              maxLength={12_000}
              placeholder="Describe what you want to build, investigate, or change…"
              aria-describedby="thread-initial-prompt-help"
              className="mt-2 min-h-32"
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

            <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-2">
              <label
                className={`inline-flex h-7 items-center gap-1.5 rounded-md border border-ghost-border/75 px-2.5 text-[9px] font-medium transition ${
                  submitting
                    ? 'cursor-not-allowed opacity-40'
                    : 'cursor-pointer text-ghost-muted hover:border-ghost-green/45 hover:bg-ghost-green/[0.08] hover:text-ghost-bright-white'
                }`}
              >
                <ImagePlus size={12} className="text-ghost-green" />
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
              <span className="text-[9px] text-ghost-faint">Paste or drop images · PNG, JPEG, GIF, WebP · 50 MB max</span>
            </div>
            <p id="thread-initial-prompt-help" className="mt-2 text-[9px] leading-4 text-ghost-faint">
              Add text or images to give {selectedAgentLabel} its first task immediately. Leave both blank to open {selectedAgentLabel} without sending a prompt. Press ⌘Enter in this field to create the thread.
            </p>
          </fieldset>

          {error && (
            <FeedbackMessage role="alert" tone="error" className="mt-4">
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
