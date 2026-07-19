import { useId } from 'react'
import { isHexColor } from '../../lib/validation'
import { BaseInput } from '../atoms/Input'

export function ThemeColorInput({
  label,
  value,
  onChange,
}: {
  label: string
  value: string
  onChange: (value: string) => void
}) {
  const id = useId()
  const valid = isHexColor(value)

  return (
    <div className="min-w-0">
      <label htmlFor={id} className="block truncate text-[9px] font-medium text-ghost-muted" title={label}>
        {label}
      </label>
      <div className={`mt-1 flex h-9 min-w-0 items-center rounded-lg border bg-ghost-black/45 transition focus-within:ring-2 ${
        valid
          ? 'border-ghost-border/75 focus-within:border-ghost-green/60 focus-within:ring-ghost-green/10'
          : 'border-ghost-bright-red/70 focus-within:ring-ghost-bright-red/10'
      }`}>
        <BaseInput
          type="color"
          value={valid ? value : '#000000'}
          onChange={(event) => onChange(event.target.value)}
          aria-label={`${label} color picker`}
          className="ml-1.5 size-6 shrink-0 cursor-pointer rounded border-0 bg-transparent p-0"
        />
        <BaseInput
          id={id}
          type="text"
          value={value}
          onChange={(event) => onChange(event.target.value)}
          maxLength={7}
          pattern="#[0-9a-fA-F]{6}"
          autoComplete="off"
          spellCheck={false}
          aria-invalid={!valid}
          className="h-full min-w-0 flex-1 border-0 bg-transparent px-2 font-mono text-[10px] text-ghost-bright-white outline-none"
        />
      </div>
    </div>
  )
}
