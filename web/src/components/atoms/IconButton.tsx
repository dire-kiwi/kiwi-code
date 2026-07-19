import { forwardRef } from 'react'
import { classNames } from '../../lib/classNames'
import { Button, type ButtonProps } from './Button'

export type IconButtonSize = 'xs' | 'sm' | 'md' | 'lg'
export type IconButtonRadius = 'md' | 'lg'

const sizeStyles: Record<IconButtonSize, string> = {
  xs: 'grid size-6 place-items-center',
  sm: 'grid size-7 place-items-center',
  md: 'grid size-8 place-items-center',
  lg: 'grid size-9 place-items-center',
}

const radiusStyles: Record<IconButtonRadius, string> = {
  md: 'rounded-md',
  lg: 'rounded-lg',
}

export type IconButtonProps = ButtonProps & {
  size?: IconButtonSize
  radius?: IconButtonRadius
  shrink?: boolean
}

/** Square icon control built on top of the styled Button variants. */
export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(
  function IconButton({
    size = 'md',
    radius = size === 'xs' || size === 'sm' ? 'md' : 'lg',
    shrink = false,
    className,
    ...props
  }, ref) {
    return (
      <Button
        ref={ref}
        className={classNames(sizeStyles[size], shrink && 'shrink-0', radiusStyles[radius], className)}
        {...props}
      />
    )
  },
)
