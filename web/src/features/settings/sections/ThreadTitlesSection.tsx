import { useMemo, useState, type FormEvent } from 'react'
import { LoaderCircle, Save, Type } from 'lucide-react'
import { updateSettings } from '@/api'
import { useAsyncFeedback } from '@/lib/useAsyncFeedback'
import { useAppDispatch } from '@/store/hooks'
import { settingsReceived } from '@/store/slices/settings'
import type { AppSettings, CodingAgentConfig } from '@/types'
import { CodingAgentsTopic } from '@/wire/topics'
import { useSubscription } from '@/wire/react'
import { PrimaryButton } from '@/ui/buttons'
import { Select, type SelectOption } from '@/ui/inputs'
import { SectionHeader, Surface } from '@/ui/layout'
import { ActionFeedback, InfoCallout } from '@/ui/feedback'

type ThreadTitlesSectionProps = {
  settings: AppSettings
}

const thinkingLevelLabels: Record<string, string> = {
  off: 'Off',
  minimal: 'Minimal',
  low: 'Low',
  medium: 'Medium',
  high: 'High',
  xhigh: 'Extra high',
  max: 'Maximum',
}

const thinkingLevelOrder = Object.keys(thinkingLevelLabels)

export function ThreadTitlesSection({ settings }: ThreadTitlesSectionProps) {
  const dispatch = useAppDispatch()
  const [titleModel, setTitleModel] = useState(settings.titleModel)
  const [titleThinking, setTitleThinking] = useState(settings.titleThinking)
  const action = useAsyncFeedback()
  const codingAgentsSubscription = useSubscription(CodingAgentsTopic, {})

  // Titles are always generated through pi's model registry, so the pi
  // agent's discovered models are the valid choices for every coding agent.
  const piModels = useMemo(
    () => (codingAgentsSubscription.state === 'ready'
      ? (codingAgentsSubscription.data as CodingAgentConfig[]).find((config) => config.id === 'pi')?.models ?? []
      : []),
    [codingAgentsSubscription],
  )

  const modelOptions = useMemo(() => {
    const result: SelectOption[] = [{
      value: '',
      label: `Default (${settings.defaultTitleModel})`,
      textValue: `Default (${settings.defaultTitleModel})`,
    }]
    const seen = new Set([''])
    for (const model of piModels) {
      if (!model.id || seen.has(model.id) || model.id === settings.defaultTitleModel) continue
      seen.add(model.id)
      result.push({ value: model.id, label: model.label, textValue: model.label })
    }
    if (!seen.has(titleModel) && titleModel !== '' && titleModel !== settings.defaultTitleModel) {
      result.push({ value: titleModel, label: titleModel, textValue: titleModel })
    }
    return result
  }, [piModels, settings.defaultTitleModel, titleModel])

  // The thinking choices follow the selected model: pi reports the levels each
  // discovered model supports, and levels the model cannot run are hidden.
  const effectiveModel = titleModel || settings.defaultTitleModel
  const supportedLevels = useMemo(() => {
    const model = piModels.find((candidate) => candidate.id === effectiveModel)
    const reported = model?.reasoningLevels ?? []
    if (reported.length === 0) return thinkingLevelOrder
    return thinkingLevelOrder.filter((level) => reported.includes(level))
  }, [piModels, effectiveModel])

  const thinkingOptions = useMemo(() => {
    const defaultLabel = thinkingLevelLabels[settings.defaultTitleThinking] ?? settings.defaultTitleThinking
    const result: SelectOption[] = [{
      value: '',
      label: `Default (${defaultLabel})`,
      textValue: `Default (${defaultLabel})`,
    }]
    for (const level of supportedLevels) {
      result.push({ value: level, label: thinkingLevelLabels[level], textValue: thinkingLevelLabels[level] })
    }
    if (titleThinking !== '' && !supportedLevels.includes(titleThinking)) {
      const label = thinkingLevelLabels[titleThinking] ?? titleThinking
      result.push({ value: titleThinking, label, textValue: label })
    }
    return result
  }, [supportedLevels, settings.defaultTitleThinking, titleThinking])

  const modelSupportsThinking = !(supportedLevels.length === 1 && supportedLevels[0] === 'off')

  function handleModelChange(value: string) {
    setTitleModel(value)
    action.clearFeedback()
    // Keep the thinking selection valid for the newly selected model.
    const model = piModels.find((candidate) => candidate.id === (value || settings.defaultTitleModel))
    const reported = model?.reasoningLevels ?? []
    if (titleThinking !== '' && reported.length > 0 && !reported.includes(titleThinking)) {
      setTitleThinking('')
    }
  }

  const normalizedModel = titleModel === settings.defaultTitleModel ? '' : titleModel
  const normalizedThinking = titleThinking === settings.defaultTitleThinking ? '' : titleThinking
  const dirty = normalizedModel !== settings.titleModel || normalizedThinking !== settings.titleThinking

  async function handleSave(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (action.pending || !dirty) return
    const next = await action.run(
      'default',
      () => updateSettings({ titleModel: normalizedModel, titleThinking: normalizedThinking }),
      {
        success: 'Thread title settings saved.',
        failure: 'Could not save the thread title settings.',
      },
    )
    if (!next) return
    dispatch(settingsReceived(next))
    setTitleModel(next.titleModel)
    setTitleThinking(next.titleThinking)
  }

  return (
    <Surface
      as="form"
      variant="elevated-panel"
      onSubmit={(event) => void handleSave(event)}
      className="overflow-hidden"
    >
      <SectionHeader
        icon={<Type size={16} />}
        title="Thread titles"
        description="Choose the model used to auto-generate a thread's title from its first message."
        tone="magenta"
      />

      <div className="space-y-4 p-4 sm:p-5">
        <label className="block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim">
          Title generation model
          <Select
            variant="code"
            aria-label="Title generation model"
            value={titleModel}
            options={modelOptions}
            onChange={handleModelChange}
            searchable
            searchPlaceholder="Search models"
            rootClassName="mt-2.5"
          />
        </label>

        <label className="block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim">
          Thinking level
          <Select
            variant="code"
            aria-label="Title generation thinking level"
            value={titleThinking}
            options={thinkingOptions}
            onChange={(value) => {
              setTitleThinking(value)
              action.clearFeedback()
            }}
            rootClassName="mt-2.5"
          />
        </label>

        {!modelSupportsThinking && (
          <p className="text-[10px] leading-4 text-ghost-dim">
            The selected model does not support thinking; titles are generated without it.
          </p>
        )}

        {codingAgentsSubscription.state === 'error' && (
          <p className="text-[10px] leading-4 text-ghost-yellow">
            Could not list pi models; the current selection is still shown and can be saved.
          </p>
        )}

        <InfoCallout>
          Titles are generated through pi&apos;s model registry for every coding agent, so the model
          must be available to pi. Agents already running pick up the change after a restart.
        </InfoCallout>

        <ActionFeedback feedback={action.feedback} />
      </div>

      <div className="flex items-center justify-end border-t border-ghost-border/60 bg-ghost-black/15 px-4 py-3 sm:px-5">
        <PrimaryButton
          type="submit"
          size="md"
          disabled={!dirty || action.pending}
          className="flex min-w-28 items-center justify-center gap-2"
        >
          {action.pending ? <LoaderCircle size={14} className="animate-spin" /> : <Save size={14} />}
          Save
        </PrimaryButton>
      </div>
    </Surface>
  )
}
