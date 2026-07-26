import { useEffect, useState } from 'react'
import { Check, Download, LoaderCircle, Sparkles } from 'lucide-react'
import { installAgentSkill } from '../../../../api'
import type { AgentSkillStatus } from '../../../../types'
import { PrimaryButton } from '../../../atoms/Button'
import { StatusBadge } from '../../../atoms/StatusBadge'
import { Surface } from '../../../atoms/Surface'
import { LoadErrorPanel, LoadingPanel } from '../../../molecules/AsyncStatePanel'
import { FeedbackMessage } from '../../../molecules/FeedbackMessage'
import { InfoCallout } from '../../../molecules/InfoCallout'
import { SectionHeader } from '../../../molecules/SectionHeader'
import { useSubscription } from '../../../../wire/react'
import { AgentSkillsTopic } from '../../../../wire/topics'

export function SkillsSection() {
  const subscription = useSubscription(AgentSkillsTopic, undefined)
  const [agentSkill, setAgentSkill] = useState<AgentSkillStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [installing, setInstalling] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')

  useEffect(() => {
    if (subscription.state === 'loading') {
      setLoading(true)
      return
    }
    setLoading(false)
    if (subscription.state === 'error') {
      setAgentSkill(null)
      setLoadError(subscription.error.message)
      return
    }
    setLoadError('')
    setAgentSkill(subscription.data as AgentSkillStatus)
  }, [subscription])

  async function handleInstall() {
    if (installing) return
    setInstalling(true)
    setError('')
    setMessage('')
    try {
      const next = await installAgentSkill()
      setAgentSkill(next)
      setMessage('Agent skills installed. Start a new Pi session or use /reload to load them.')
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not install the agent skills.')
    } finally {
      setInstalling(false)
    }
  }

  if (loading) return <LoadingPanel label="Loading agent skill status" />
  if (!agentSkill) {
    return (
      <LoadErrorPanel
        message={loadError || 'Could not load the agent skill status.'}
        onRetry={subscription.retry}
      />
    )
  }

  const bundledSkills = agentSkill.skills?.length ? agentSkill.skills : [agentSkill]

  return (
    <Surface as="section" variant="elevated-panel" className="overflow-hidden">
      <SectionHeader
        icon={<Sparkles size={16} />}
        title="Agent skills"
        description="Install global Kiwi Code thread-control and process-management skills for Agent Skills-compatible coding agents."
        tone="blue"
        badge={(
          <StatusBadge tone={agentSkill.upToDate ? 'success' : agentSkill.installed ? 'warning' : 'neutral'}>
            {agentSkill.upToDate ? 'Installed' : agentSkill.installed ? 'Update available' : 'Not installed'}
          </StatusBadge>
        )}
      />

      <div className="p-4 sm:p-5">
        <div className="rounded-xl border border-ghost-border/55 bg-ghost-black/25 px-3.5 py-3">
          <p className="text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-faint">Install locations</p>
          <div className="mt-1.5 space-y-1">
            {bundledSkills.map((skill) => (
              <p key={skill.name} className="break-all font-mono text-[10px] leading-4 text-ghost-muted">
                {skill.path}
              </p>
            ))}
          </div>
        </div>

        <InfoCallout className="mt-4">
          The dependency-free Node.js helpers can create, rename, archive, restore, inspect, and close threads; read Pi,
          Claude, shell, tool, and process output; and manage persistent process shells. Claude Code launched through
          Kiwi Code already receives the process skill from its bundled plugin. Use{' '}
          <span className="font-mono text-ghost-blue">/reload</span> in an existing Pi session after installation.
        </InfoCallout>

        {error && (
          <FeedbackMessage role="alert" tone="error" className="mt-4">
            {error}
          </FeedbackMessage>
        )}
        {message && (
          <FeedbackMessage
            role="status"
            tone="success"
            className="mt-4 flex items-center gap-2"
          >
            <Check size={13} className="shrink-0" />
            {message}
          </FeedbackMessage>
        )}
      </div>

      <div className="flex items-center justify-end border-t border-ghost-border/60 bg-ghost-black/15 px-4 py-3 sm:px-5">
        <PrimaryButton
          type="button"
          size="md"
          onClick={() => void handleInstall()}
          disabled={installing}
          className="flex min-w-32 items-center justify-center gap-2"
        >
          {installing ? <LoaderCircle size={14} className="animate-spin" /> : <Download size={14} />}
          {agentSkill.upToDate ? 'Reinstall skills' : agentSkill.installed ? 'Update skills' : 'Install skills'}
        </PrimaryButton>
      </div>
    </Surface>
  )
}
