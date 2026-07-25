import { useState, type FormEvent } from 'react'
import { Check, LoaderCircle, Network, Save, Workflow } from 'lucide-react'
import { updateSettings } from '../../../../api'
import type { AppSettings } from '../../../../types'
import { PrimaryButton } from '../../../atoms/Button'
import { TextInput } from '../../../atoms/Input'
import { Select } from '../../../atoms/Select'
import { StatusBadge } from '../../../atoms/StatusBadge'
import { Surface } from '../../../atoms/Surface'
import { FeedbackMessage } from '../../../molecules/FeedbackMessage'
import { InfoCallout } from '../../../molecules/InfoCallout'
import { SectionHeader } from '../../../molecules/SectionHeader'

type AgentsSectionProps = {
  settings: AppSettings
  onSettingsUpdated: (settings: AppSettings) => void
}

export function AgentsSection({ settings, onSettingsUpdated }: AgentsSectionProps) {
  const [subAgentNestingDepth, setSubAgentNestingDepth] = useState(String(settings.subAgentNestingDepth))
  const [nestingSaving, setNestingSaving] = useState(false)
  const [nestingError, setNestingError] = useState('')
  const [nestingMessage, setNestingMessage] = useState('')

  const [disableWorkflows, setDisableWorkflows] = useState(settings.disableWorkflows)
  const [workflowKeywordTrigger, setWorkflowKeywordTrigger] = useState(settings.workflowKeywordTriggerEnabled)
  const [workflowSizeGuideline, setWorkflowSizeGuideline] = useState<AppSettings['workflowSizeGuideline']>(
    settings.workflowSizeGuideline,
  )
  const [workflowsSaving, setWorkflowsSaving] = useState(false)
  const [workflowsError, setWorkflowsError] = useState('')
  const [workflowsMessage, setWorkflowsMessage] = useState('')

  const parsedNestingDepth = Number(subAgentNestingDepth)
  const nestingValueValid = subAgentNestingDepth.trim() !== ''
    && Number.isInteger(parsedNestingDepth)
    && parsedNestingDepth >= 0
    && parsedNestingDepth <= settings.maxSubAgentNestingDepth
  const nestingDirty = nestingValueValid && parsedNestingDepth !== settings.subAgentNestingDepth
  const workflowsDirty = disableWorkflows !== settings.disableWorkflows
    || workflowKeywordTrigger !== settings.workflowKeywordTriggerEnabled
    || workflowSizeGuideline !== settings.workflowSizeGuideline

  async function handleNestingSave(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (nestingSaving) return
    if (!nestingValueValid) {
      setNestingError(`Depth must be a whole number from 0 to ${settings.maxSubAgentNestingDepth}.`)
      return
    }

    setNestingSaving(true)
    setNestingError('')
    setNestingMessage('')
    try {
      const next = await updateSettings({ subAgentNestingDepth: parsedNestingDepth })
      onSettingsUpdated(next)
      setSubAgentNestingDepth(String(next.subAgentNestingDepth))
      setNestingMessage('Sub-agent nesting depth saved.')
    } catch (reason) {
      setNestingError(reason instanceof Error ? reason.message : 'Could not save sub-agent nesting depth.')
    } finally {
      setNestingSaving(false)
    }
  }

  async function handleWorkflowsSave(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (workflowsSaving) return
    setWorkflowsSaving(true)
    setWorkflowsError('')
    setWorkflowsMessage('')
    try {
      const next = await updateSettings({
        disableWorkflows,
        workflowKeywordTriggerEnabled: workflowKeywordTrigger,
        workflowSizeGuideline,
      })
      onSettingsUpdated(next)
      setDisableWorkflows(next.disableWorkflows)
      setWorkflowKeywordTrigger(next.workflowKeywordTriggerEnabled)
      setWorkflowSizeGuideline(next.workflowSizeGuideline)
      setWorkflowsMessage('Dynamic workflow settings saved.')
    } catch (reason) {
      setWorkflowsError(reason instanceof Error ? reason.message : 'Could not save workflow settings.')
    } finally {
      setWorkflowsSaving(false)
    }
  }

  return (
    <>
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
              generation. Projects can override this value in their project settings.
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
            disabled={!nestingDirty || !nestingValueValid || nestingSaving}
            className="flex min-w-28 items-center justify-center gap-2"
          >
            {nestingSaving ? <LoaderCircle size={14} className="animate-spin" /> : <Save size={14} />}
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
          description="Configure Kiwi Code workflows exposed through Pi sessions."
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
                Disabling blocks new and resumed Kiwi Code runs, saved commands, and Pi ultracode activation. Retained runs remain visible.
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
            disabled={!workflowsDirty || workflowsSaving}
            className="flex min-w-28 items-center justify-center gap-2"
          >
            {workflowsSaving ? <LoaderCircle size={14} className="animate-spin" /> : <Save size={14} />}
            Save workflows
          </PrimaryButton>
        </div>
      </Surface>
    </>
  )
}
