import { forwardRef, type ComponentPropsWithoutRef } from 'react'
import { classNames } from '../../lib/classNames'

export type BaseButtonProps = ComponentPropsWithoutRef<'button'>

/** The unstyled native boundary used by every button component. */
export const BaseButton = forwardRef<HTMLButtonElement, BaseButtonProps>(
  function BaseButton(props, ref) {
    return <button ref={ref} {...props} />
  },
)

export type ButtonVariant =
  | 'plain'
  | 'primary'
  | 'primary-static'
  | 'ghost'
  | 'text'
  | 'subtle'
  | 'subtle-white'
  | 'danger'
  | 'bordered'
  | 'accent-outline'

const variantStyles: Record<ButtonVariant, string | undefined> = {
  plain: undefined,
  primary: 'bg-ghost-green font-semibold text-ghost-black transition hover:bg-ghost-bright-green disabled:cursor-not-allowed disabled:opacity-40',
  'primary-static': 'bg-ghost-green font-semibold text-ghost-black disabled:opacity-40',
  ghost: 'text-ghost-muted transition hover:bg-ghost-raised hover:text-ghost-bright-white',
  text: 'text-ghost-muted transition hover:text-ghost-bright-white',
  subtle: 'text-ghost-dim transition hover:bg-ghost-raised hover:text-ghost-bright-white',
  'subtle-white': 'text-ghost-dim transition hover:bg-ghost-raised hover:text-ghost-foreground',
  danger: 'text-ghost-dim transition hover:bg-ghost-red/15 hover:text-ghost-bright-red',
  bordered: 'border border-ghost-border/80 bg-ghost-raised font-medium text-ghost-white transition hover:border-ghost-green/45 hover:bg-ghost-green/[0.1] hover:text-ghost-bright-white',
  'accent-outline': 'border border-ghost-border/80 text-ghost-muted transition hover:border-ghost-green/45 hover:bg-ghost-green/[0.1] hover:text-ghost-green',
}

export type ButtonProps = BaseButtonProps & {
  variant?: ButtonVariant
}

/** A styled button that extends BaseButton through reusable visual variants. */
export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = 'plain', className, ...props },
  ref,
) {
  return (
    <BaseButton
      ref={ref}
      className={classNames(variantStyles[variant], className)}
      {...props}
    />
  )
})

export type VariantButtonProps = Omit<ButtonProps, 'variant'>
export type ActionButtonSize = 'none' | 'xs' | 'sm' | 'md'

const primarySizeStyles: Record<ActionButtonSize, string | undefined> = {
  none: undefined,
  xs: 'h-7 rounded-md px-2 text-[10px]',
  sm: 'h-8 rounded-md px-3 text-[10px]',
  md: 'h-9 rounded-lg px-4 text-xs',
}

const ghostSizeStyles: Record<ActionButtonSize, string | undefined> = {
  none: undefined,
  xs: 'h-7 rounded-md px-2 text-[10px] font-medium',
  sm: 'h-8 rounded-md px-3 text-[10px] font-medium',
  md: 'h-9 rounded-lg text-xs font-medium',
}

export type ActionButtonProps = VariantButtonProps & {
  size?: ActionButtonSize
}

export const PrimaryButton = forwardRef<HTMLButtonElement, ActionButtonProps>(
  function PrimaryButton({ size = 'none', className, ...props }, ref) {
    return (
      <Button
        ref={ref}
        variant="primary"
        className={classNames(primarySizeStyles[size], className)}
        {...props}
      />
    )
  },
)

export const GhostButton = forwardRef<HTMLButtonElement, ActionButtonProps>(
  function GhostButton({ size = 'none', className, ...props }, ref) {
    return (
      <Button
        ref={ref}
        variant="ghost"
        className={classNames(ghostSizeStyles[size], className)}
        {...props}
      />
    )
  },
)
