import type { ReactNode } from 'react'
import { BrainCircuit, Cpu } from 'lucide-react'
import { Select } from '../atoms/Select'

export type AgentModelOption = {
  value: string
  label: string
}

type AgentModelControlsProps = {
  model: string
  modelOptions: AgentModelOption[]
  modelDisabled?: boolean
  onModelChange: (value: string) => void
  thinking: string
  thinkingOptions: AgentModelOption[]
  thinkingDisabled?: boolean
  onThinkingChange: (value: string) => void
  variant: 'form' | 'inline'
  className?: string
  modelId?: string
  thinkingId?: string
}

export function AgentModelControls({
  model,
  modelOptions,
  modelDisabled,
  onModelChange,
  thinking,
  thinkingOptions,
  thinkingDisabled,
  onThinkingChange,
  variant,
  className,
  modelId,
  thinkingId,
}: AgentModelControlsProps) {
  if (variant === 'inline') {
    return (
      <div className={className} aria-label="Agent model settings">
        <label>
          <span>Model</span>
          <Select
            variant="inline"
            aria-label="Model"
            value={model}
            options={model
              ? modelOptions
              : [{ value: '', label: 'Select model' }, ...modelOptions]}
            rootClassName="max-[700px]:w-full"
            className="max-[700px]:w-full max-[700px]:max-w-none"
            disabled={modelDisabled}
            onChange={onModelChange}
          />
        </label>
        <label>
          <span>Thinking</span>
          <Select
            variant="inline"
            aria-label="Thinking level"
            value={thinking}
            options={thinking
              ? thinkingOptions
              : [{ value: '', label: 'Default' }, ...thinkingOptions]}
            rootClassName="max-[700px]:w-full"
            className="max-w-[90px] max-[700px]:w-full max-[700px]:max-w-none"
            disabled={thinkingDisabled}
            onChange={onThinkingChange}
          />
        </label>
      </div>
    )
  }

  return (
    <div className={className}>
      <AgentFormSelect
        id={modelId}
        label="Model"
        value={model}
        options={modelOptions}
        disabled={modelDisabled}
        icon={<Cpu size={12} />}
        onChange={onModelChange}
      />
      <AgentFormSelect
        id={thinkingId}
        label="Thinking level"
        value={thinking}
        options={thinkingOptions}
        disabled={thinkingDisabled}
        icon={<BrainCircuit size={12} />}
        onChange={onThinkingChange}
      />
    </div>
  )
}

function AgentFormSelect({
  id,
  label,
  value,
  options,
  disabled,
  icon,
  onChange,
}: {
  id?: string
  label: string
  value: string
  options: AgentModelOption[]
  disabled?: boolean
  icon: ReactNode
  onChange: (value: string) => void
}) {
  return (
    <div>
      <label htmlFor={id} className="block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim">
        {label}
      </label>
      <div className="mt-2">
        <Select
          id={id}
          value={value}
          options={options}
          onChange={onChange}
          disabled={disabled}
          leadingIcon={icon}
        />
      </div>
    </div>
  )
}
