import type { ReactNode } from 'react'
import { Check } from 'lucide-react'
import { RadioInput } from '../atoms/Input'
import { SelectableCard } from '../atoms/SelectableCard'

type ThreadLocationOptionProps = {
  value: string
  selected: boolean
  icon: ReactNode
  iconClassName: string
  title: string
  children: ReactNode
  disabled?: boolean
  onSelect: () => void
}

export function ThreadLocationOption({
  value,
  selected,
  icon,
  iconClassName,
  title,
  children,
  disabled = false,
  onSelect,
}: ThreadLocationOptionProps) {
  return (
    <SelectableCard selected={selected}>
      <RadioInput
        name="thread-location"
        value={value}
        checked={selected}
        onChange={onSelect}
        disabled={disabled}
      />
      <span className={`grid size-8 shrink-0 place-items-center rounded-lg bg-ghost-raised ${iconClassName}`}>
        {icon}
      </span>
      <span className="min-w-0 flex-1">
        <span className="block text-xs font-semibold text-ghost-bright-white">{title}</span>
        {children}
      </span>
      {selected && (
        <span className="grid size-5 shrink-0 place-items-center rounded-full bg-ghost-green text-ghost-black">
          <Check size={12} strokeWidth={2.5} />
        </span>
      )}
    </SelectableCard>
  )
}
