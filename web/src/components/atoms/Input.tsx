import { forwardRef, type ComponentPropsWithoutRef } from 'react'
import { classNames } from '../../lib/classNames'

export type BaseInputProps = ComponentPropsWithoutRef<'input'>
export type BaseTextAreaProps = ComponentPropsWithoutRef<'textarea'>

/** The unstyled native boundary used by every input component. */
export const BaseInput = forwardRef<HTMLInputElement, BaseInputProps>(
  function BaseInput(props, ref) {
    return <input ref={ref} {...props} />
  },
)

/** The unstyled native boundary used by every multiline input component. */
export const BaseTextArea = forwardRef<HTMLTextAreaElement, BaseTextAreaProps>(
  function BaseTextArea(props, ref) {
    return <textarea ref={ref} {...props} />
  },
)

export type TextInputVariant =
  | 'compact'
  | 'compact-code'
  | 'code'
  | 'code-large'
  | 'search'
  | 'title'

const textInputStyles: Record<TextInputVariant, string> = {
  compact: 'h-9 w-full rounded-lg border border-ghost-border bg-ghost-black/50 px-3 text-xs text-ghost-bright-white outline-none',
  'compact-code': 'h-9 w-full rounded-lg border border-ghost-border bg-ghost-black/50 px-3 font-mono text-[11px] text-ghost-bright-white outline-none',
  code: 'h-9 w-full rounded-lg border border-ghost-border/80 bg-ghost-black/55 px-3 font-mono text-[11px] font-normal normal-case tracking-normal text-ghost-bright-white outline-none placeholder:text-ghost-faint focus:border-ghost-green/60 focus:ring-2 focus:ring-ghost-green/10',
  'code-large': 'h-11 w-full rounded-xl border border-ghost-border/80 bg-ghost-black/55 px-3.5 font-mono text-[11px] font-normal normal-case tracking-normal text-ghost-bright-white outline-none transition placeholder:text-ghost-faint focus:border-ghost-green/65 focus:ring-2 focus:ring-ghost-green/10',
  search: 'h-9 w-full rounded-lg border border-ghost-border/75 bg-ghost-black/45 pl-8 pr-3 font-mono text-[10px] text-ghost-bright-white outline-none placeholder:text-ghost-faint focus:border-ghost-green/55 focus:ring-2 focus:ring-ghost-green/10',
  title: 'h-10 w-full rounded-lg border border-ghost-green/55 bg-ghost-black/55 px-3 text-xs font-medium text-ghost-bright-white outline-none transition focus:border-ghost-green focus:ring-2 focus:ring-ghost-green/10 disabled:opacity-60',
}

export type TextInputProps = BaseInputProps & {
  variant?: TextInputVariant
}

export const TextInput = forwardRef<HTMLInputElement, TextInputProps>(
  function TextInput({ variant = 'compact', className, ...props }, ref) {
    return (
      <BaseInput
        ref={ref}
        className={classNames(textInputStyles[variant], className)}
        {...props}
      />
    )
  },
)

export type TextAreaProps = BaseTextAreaProps

export const TextArea = forwardRef<HTMLTextAreaElement, TextAreaProps>(
  function TextArea({ className, ...props }, ref) {
    return (
      <BaseTextArea
        ref={ref}
        className={classNames(
          'min-h-28 w-full resize-y rounded-xl border border-ghost-border/80 bg-ghost-black/55 px-3.5 py-3 font-mono text-[11px] leading-5 text-ghost-bright-white outline-none transition placeholder:text-ghost-faint focus:border-ghost-green/65 focus:ring-2 focus:ring-ghost-green/10 disabled:cursor-not-allowed disabled:opacity-55',
          className,
        )}
        {...props}
      />
    )
  },
)

export type RadioInputProps = Omit<BaseInputProps, 'type'>

export const RadioInput = forwardRef<HTMLInputElement, RadioInputProps>(
  function RadioInput({ className, ...props }, ref) {
    return (
      <BaseInput
        ref={ref}
        type="radio"
        className={classNames('mt-0.5 size-4 shrink-0 accent-ghost-green', className)}
        {...props}
      />
    )
  },
)
