import { useState, type FormEvent } from 'react'
import { LoaderCircle, Network, Save, Workflow } from 'lucide-react'
import { updateSettings } from '../../../../api'
import { useAsyncFeedback } from '../../../../lib/useAsyncFeedback'
import type { AppSettings } from '../../../../types'
import { PrimaryButton } from '@/ui/buttons'
import { Select, TextInput } from '@/ui/inputs'
import { ActionFeedback, InfoCallout, StatusBadge } from '@/ui/feedback'
import { SectionHeader, Surface } from '@/ui/layout'

type AgentsSectionProps = {
  settings: AppSettings
  onSettingsUpdated: (settings: AppSettings) => void
}

export function AgentsSection({ settings, onSettingsUpdated }: AgentsSectionProps) {
  const [subAgentNestingDepth, setSubAgentNestingDepth] = useState(String(settings.subAgentNestingDepth))
  const nestingFeedback = useAsyncFeedback()

  const [disableWorkflows, setDisableWorkflows] = useState(settings.disableWorkflows)
  const [workflowKeywordTrigger, setWorkflowKeywordTrigger] = useState(settings.workflowKeywordTriggerEnabled)
  const [workflowSizeGuideline, setWorkflowSizeGuideline] = useState<AppSettings['workflowSizeGuideline']>(
    settings.workflowSizeGuideline,
  )
  const workflowsFeedback = useAsyncFeedback()

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
    if (nestingFeedback.pending) return
    if (!nestingValueValid) {
      nestingFeedback.showError(`Depth must be a whole number from 0 to ${settings.maxSubAgentNestingDepth}.`)
      return
    }

    const next = await nestingFeedback.run(
      'default',
      () => updateSettings({ subAgentNestingDepth: parsedNestingDepth }),
      {
        success: 'Sub-agent nesting depth saved.',
        failure: 'Could not save sub-agent nesting depth.',
      },
    )
    if (!next) return
    onSettingsUpdated(next)
    setSubAgentNestingDepth(String(next.subAgentNestingDepth))
  }

  async function handleWorkflowsSave(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (workflowsFeedback.pending) return
    const next = await workflowsFeedback.run(
      'default',
      () => updateSettings({
        disableWorkflows,
        workflowKeywordTriggerEnabled: workflowKeywordTrigger,
        workflowSizeGuideline,
      }),
      {
        success: 'Dynamic workflow settings saved.',
        failure: 'Could not save workflow settings.',
      },
    )
    if (!next) return
    onSettingsUpdated(next)
    setDisableWorkflows(next.disableWorkflows)
    setWorkflowKeywordTrigger(next.workflowKeywordTriggerEnabled)
    setWorkflowSizeGuideline(next.workflowSizeGuideline)
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
                  nestingFeedback.clearFeedback()
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

          <ActionFeedback feedback={nestingFeedback.feedback} className="mt-4" />
        </div>

        <div className="flex items-center justify-end border-t border-ghost-border/60 bg-ghost-black/15 px-4 py-3 sm:px-5">
          <PrimaryButton
            type="submit"
            size="md"
            disabled={!nestingDirty || !nestingValueValid || nestingFeedback.pending}
            className="flex min-w-28 items-center justify-center gap-2"
          >
            {nestingFeedback.pending ? <LoaderCircle size={14} className="animate-spin" /> : <Save size={14} />}
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
                workflowsFeedback.clearFeedback()
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
                workflowsFeedback.clearFeedback()
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
                  workflowsFeedback.clearFeedback()
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

          <ActionFeedback feedback={workflowsFeedback.feedback} />
        </div>

        <div className="flex items-center justify-end border-t border-ghost-border/60 bg-ghost-black/15 px-4 py-3 sm:px-5">
          <PrimaryButton
            type="submit"
            size="md"
            disabled={!workflowsDirty || workflowsFeedback.pending}
            className="flex min-w-28 items-center justify-center gap-2"
          >
            {workflowsFeedback.pending ? <LoaderCircle size={14} className="animate-spin" /> : <Save size={14} />}
            Save workflows
          </PrimaryButton>
        </div>
      </Surface>
    </>
  )
}
