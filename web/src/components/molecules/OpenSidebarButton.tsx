import { Menu } from 'lucide-react'
import { classNames } from '../../lib/classNames'
import { IconButton, type IconButtonProps } from '../atoms/IconButton'

type OpenSidebarButtonProps = Omit<
  IconButtonProps,
  'aria-label' | 'children' | 'radius' | 'size' | 'variant'
> & {
  responsive?: boolean
}

export function OpenSidebarButton({
  responsive = true,
  className,
  ...props
}: OpenSidebarButtonProps) {
  return (
    <IconButton
      type="button"
      size="lg"
      variant="ghost"
      aria-label="Open project navigation"
      className={classNames(
        'border border-ghost-border/80',
        responsive && 'md:hidden',
        className,
      )}
      {...props}
    >
      <Menu size={17} />
    </IconButton>
  )
}
