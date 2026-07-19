import {
  forwardRef,
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ComponentPropsWithoutRef,
  type CSSProperties,
  type ForwardedRef,
  type KeyboardEvent,
  type ReactNode,
} from 'react'
import { createPortal } from 'react-dom'
import { Check, ChevronDown } from 'lucide-react'
import { classNames } from '../../lib/classNames'

export type SelectOption = {
  value: string
  label: ReactNode
  /** Plain text used for keyboard search and the option tooltip. */
  textValue?: string
  disabled?: boolean
}

export type SelectVariant = 'code' | 'inline' | 'icon'

export type SelectProps = Omit<
  ComponentPropsWithoutRef<'button'>,
  'children' | 'defaultValue' | 'onChange' | 'role' | 'value'
> & {
  value: string
  options: readonly SelectOption[]
  onChange: (value: string) => void
  variant?: SelectVariant
  leadingIcon?: ReactNode
  placeholder?: ReactNode
  menuAlign?: 'start' | 'end'
  rootClassName?: string
  menuClassName?: string
  optionClassName?: string
}

const rootStyles: Record<SelectVariant, string> = {
  code: 'block w-full',
  inline: 'inline-flex min-w-0 max-w-full align-middle',
  icon: 'block h-full w-full',
}

const triggerStyles: Record<SelectVariant, string> = {
  code: 'relative flex h-9 w-full items-center rounded-lg border border-ghost-border/80 bg-ghost-black/55 py-0 pr-9 text-left font-mono text-[10px] text-ghost-bright-white outline-none transition hover:border-ghost-border focus:border-ghost-green/60 focus:ring-2 focus:ring-ghost-green/10 disabled:cursor-not-allowed disabled:opacity-55',
  inline: 'relative flex h-6 min-w-0 max-w-[11.25rem] items-center rounded-[5px] border-0 bg-transparent py-0 pl-0.5 pr-1 text-left font-mono text-[9px] text-ghost-muted outline-none transition hover:bg-ghost-raised/70 hover:text-ghost-bright-white focus:bg-ghost-raised/70 focus:text-ghost-bright-white disabled:cursor-not-allowed disabled:opacity-55',
  icon: 'grid h-full w-full place-items-center border-0 bg-transparent text-ghost-faint outline-none transition hover:bg-ghost-green/10 hover:text-ghost-muted disabled:cursor-default disabled:opacity-55',
}

const menuTextStyles: Record<SelectVariant, string> = {
  code: 'font-mono text-[10px]',
  inline: 'font-mono text-[9px]',
  icon: 'font-sans text-[10px]',
}

const menuMaximumHeight = 240
const menuViewportMargin = 8
const menuGap = 4

function setForwardedRef<T>(ref: ForwardedRef<T>, value: T | null) {
  if (typeof ref === 'function') ref(value)
  else if (ref) ref.current = value
}

function optionText(option: SelectOption) {
  if (option.textValue !== undefined) return option.textValue
  if (typeof option.label === 'string' || typeof option.label === 'number') {
    return String(option.label)
  }
  return option.value
}

/**
 * A reusable HTML select control built from a button and ARIA listbox.
 *
 * The menu is portalled to the document body so it is not clipped by scrollable
 * panels. Focus remains on the trigger while arrow keys move the active option.
 */
export const Select = forwardRef<HTMLButtonElement, SelectProps>(function Select(
  {
    id,
    value,
    options,
    onChange,
    variant = 'code',
    leadingIcon,
    placeholder = 'Select…',
    menuAlign = variant === 'icon' ? 'end' : 'start',
    rootClassName,
    menuClassName,
    optionClassName,
    className,
    disabled,
    type = 'button',
    onClick: onTriggerClick,
    onKeyDown: onTriggerKeyDown,
    'aria-label': ariaLabel,
    'aria-labelledby': ariaLabelledBy,
    ...buttonProps
  },
  forwardedRef,
) {
  const generatedId = useId()
  const triggerId = id ?? `html-select-${generatedId}`
  const listboxId = `${triggerId}-listbox`
  const rootRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef('')
  const searchTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const [open, setOpen] = useState(false)
  const [activeIndex, setActiveIndex] = useState(-1)
  const [menuStyle, setMenuStyle] = useState<CSSProperties>({})

  const selectedIndex = options.findIndex((option) => option.value === value)
  const selectedOption = selectedIndex >= 0 ? options[selectedIndex] : undefined

  const assignTriggerRef = useCallback((node: HTMLButtonElement | null) => {
    triggerRef.current = node
    setForwardedRef(forwardedRef, node)
  }, [forwardedRef])

  const firstEnabledIndex = useCallback((fromEnd = false) => {
    if (fromEnd) {
      for (let index = options.length - 1; index >= 0; index -= 1) {
        if (!options[index]?.disabled) return index
      }
      return -1
    }
    return options.findIndex((option) => !option.disabled)
  }, [options])

  const closeMenu = useCallback(() => {
    setOpen(false)
    searchRef.current = ''
    if (searchTimerRef.current) {
      clearTimeout(searchTimerRef.current)
      searchTimerRef.current = null
    }
  }, [])

  const openMenu = useCallback(() => {
    if (disabled) return
    const initialIndex = selectedIndex >= 0 && !options[selectedIndex]?.disabled
      ? selectedIndex
      : firstEnabledIndex()
    if (initialIndex < 0) return
    setActiveIndex(initialIndex)
    setOpen(true)
  }, [disabled, firstEnabledIndex, options, selectedIndex])

  const moveActive = useCallback((direction: 1 | -1) => {
    const enabledIndices = options.flatMap((option, index) => option.disabled ? [] : [index])
    if (enabledIndices.length === 0) return
    const currentPosition = enabledIndices.indexOf(activeIndex)
    const nextPosition = currentPosition < 0
      ? direction === 1 ? 0 : enabledIndices.length - 1
      : (currentPosition + direction + enabledIndices.length) % enabledIndices.length
    setActiveIndex(enabledIndices[nextPosition]!)
  }, [activeIndex, options])

  const chooseOption = useCallback((index: number) => {
    const option = options[index]
    if (!option || option.disabled) return
    closeMenu()
    triggerRef.current?.focus()
    if (option.value !== value) onChange(option.value)
  }, [closeMenu, onChange, options, value])

  function handleTypeahead(key: string) {
    if (searchTimerRef.current) clearTimeout(searchTimerRef.current)
    searchRef.current += key.toLocaleLowerCase()
    searchTimerRef.current = setTimeout(() => {
      searchRef.current = ''
      searchTimerRef.current = null
    }, 500)

    const query = searchRef.current
    const startIndex = activeIndex >= 0 ? activeIndex : selectedIndex
    for (let offset = 1; offset <= options.length; offset += 1) {
      const index = (startIndex + offset + options.length) % options.length
      const option = options[index]
      if (!option || option.disabled) continue
      if (optionText(option).toLocaleLowerCase().startsWith(query)) {
        setActiveIndex(index)
        setOpen(true)
        return
      }
    }
  }

  function handleSelectKeyDown(event: KeyboardEvent<HTMLButtonElement>) {
    if (event.nativeEvent.isComposing) return

    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault()
        if (open) moveActive(1)
        else openMenu()
        return
      case 'ArrowUp':
        event.preventDefault()
        if (open) moveActive(-1)
        else openMenu()
        return
      case 'Home':
        if (!open) return
        event.preventDefault()
        setActiveIndex(firstEnabledIndex())
        return
      case 'End':
        if (!open) return
        event.preventDefault()
        setActiveIndex(firstEnabledIndex(true))
        return
      case 'Enter':
      case ' ':
        event.preventDefault()
        if (open && activeIndex >= 0) chooseOption(activeIndex)
        else openMenu()
        return
      case 'Escape':
        if (!open) return
        event.preventDefault()
        closeMenu()
        return
      case 'Tab':
        closeMenu()
        return
      default:
        if (
          event.key.length === 1
          && !event.altKey
          && !event.ctrlKey
          && !event.metaKey
          && options.length > 0
          && !disabled
        ) {
          event.preventDefault()
          handleTypeahead(event.key)
        }
    }
  }

  const updateMenuPosition = useCallback(() => {
    const trigger = triggerRef.current
    if (!trigger) return

    const bounds = trigger.getBoundingClientRect()
    const viewportWidth = document.documentElement.clientWidth
    const viewportHeight = document.documentElement.clientHeight
    const minimumWidth = variant === 'icon'
      ? 176
      : variant === 'inline'
        ? Math.max(bounds.width, 160)
        : bounds.width
    const width = Math.max(0, Math.min(minimumWidth, viewportWidth - menuViewportMargin * 2))
    const desiredHeight = Math.min(menuMaximumHeight, options.length * 32 + 8)
    const spaceBelow = viewportHeight - bounds.bottom - menuGap - menuViewportMargin
    const spaceAbove = bounds.top - menuGap - menuViewportMargin
    const openUpward = spaceBelow < desiredHeight && spaceAbove > spaceBelow
    const availableHeight = Math.max(0, openUpward ? spaceAbove : spaceBelow)
    const maxHeight = Math.min(menuMaximumHeight, availableHeight)
    const preferredLeft = menuAlign === 'end' ? bounds.right - width : bounds.left
    const left = Math.min(
      Math.max(menuViewportMargin, preferredLeft),
      Math.max(menuViewportMargin, viewportWidth - menuViewportMargin - width),
    )

    setMenuStyle({
      left,
      width,
      maxHeight,
      ...(openUpward
        ? { bottom: viewportHeight - bounds.top + menuGap, top: 'auto' }
        : { bottom: 'auto', top: bounds.bottom + menuGap }),
    })
  }, [menuAlign, options.length, variant])

  useLayoutEffect(() => {
    if (!open) return
    updateMenuPosition()
    window.addEventListener('resize', updateMenuPosition)
    window.addEventListener('scroll', updateMenuPosition, true)
    return () => {
      window.removeEventListener('resize', updateMenuPosition)
      window.removeEventListener('scroll', updateMenuPosition, true)
    }
  }, [open, updateMenuPosition])

  useLayoutEffect(() => {
    if (!open || activeIndex < 0) return
    const frame = requestAnimationFrame(() => {
      const option = document.getElementById(`${listboxId}-option-${activeIndex}`)
      option?.scrollIntoView({ block: 'nearest' })
    })
    return () => cancelAnimationFrame(frame)
  }, [activeIndex, listboxId, open])

  useEffect(() => {
    if (!open) return

    function handleOutsidePointer(event: PointerEvent) {
      const target = event.target
      if (!(target instanceof Node)) return
      if (rootRef.current?.contains(target) || menuRef.current?.contains(target)) return
      closeMenu()
    }

    function handleOutsideFocus(event: FocusEvent) {
      const target = event.target
      if (!(target instanceof Node)) return
      if (rootRef.current?.contains(target) || menuRef.current?.contains(target)) return
      closeMenu()
    }

    document.addEventListener('pointerdown', handleOutsidePointer, true)
    document.addEventListener('focusin', handleOutsideFocus, true)
    return () => {
      document.removeEventListener('pointerdown', handleOutsidePointer, true)
      document.removeEventListener('focusin', handleOutsideFocus, true)
    }
  }, [closeMenu, open])

  useEffect(() => {
    if (!disabled) return
    closeMenu()
  }, [closeMenu, disabled])

  useEffect(() => () => {
    if (searchTimerRef.current) clearTimeout(searchTimerRef.current)
  }, [])

  useEffect(() => {
    if (!open) return
    setActiveIndex((current) => {
      if (current >= 0 && current < options.length && !options[current]?.disabled) return current
      if (selectedIndex >= 0 && !options[selectedIndex]?.disabled) return selectedIndex
      return firstEnabledIndex()
    })
  }, [firstEnabledIndex, open, options, selectedIndex])

  const selectedText = selectedOption ? optionText(selectedOption) : undefined
  const associatedLabel = Array.from(triggerRef.current?.labels ?? [])
    .map((label) => label.textContent?.trim())
    .filter(Boolean)
    .join(' ') || undefined
  const menuLabel = ariaLabel ?? (ariaLabelledBy ? undefined : associatedLabel)
  const menu = open && typeof document !== 'undefined'
    ? createPortal(
        <div
          ref={menuRef}
          id={listboxId}
          role="listbox"
          aria-label={menuLabel}
          aria-labelledby={menuLabel ? undefined : ariaLabelledBy ?? triggerId}
          className={classNames(
            'fixed z-[100] overflow-y-auto overscroll-contain rounded-lg border border-ghost-border/90 bg-ghost-panel p-1 shadow-[0_16px_42px_color-mix(in_srgb,var(--theme-color-canvas)_70%,transparent)]',
            menuTextStyles[variant],
            menuClassName,
          )}
          style={menuStyle}
        >
          {options.map((option, index) => {
            const selected = option.value === value
            const active = index === activeIndex
            const text = optionText(option)
            return (
              <div
                key={`${option.value}-${index}`}
                id={`${listboxId}-option-${index}`}
                role="option"
                aria-selected={selected}
                aria-disabled={option.disabled || undefined}
                className={classNames(
                  'flex min-h-7 w-full select-none items-center gap-2 rounded-md px-2 py-1.5 text-left transition-colors',
                  option.disabled
                    ? 'cursor-not-allowed text-ghost-faint opacity-55'
                    : active
                      ? 'cursor-pointer bg-ghost-raised text-ghost-bright-white'
                      : 'cursor-pointer text-ghost-muted hover:bg-ghost-raised/65 hover:text-ghost-bright-white',
                  selected && !option.disabled && 'text-ghost-green',
                  optionClassName,
                )}
                title={text || undefined}
                onPointerMove={() => {
                  if (!option.disabled) setActiveIndex(index)
                }}
                onPointerDown={(event) => {
                  if (event.pointerType === 'mouse') event.preventDefault()
                }}
                onClick={() => chooseOption(index)}
              >
                <span className="grid size-3 shrink-0 place-items-center" aria-hidden="true">
                  {selected && <Check size={11} strokeWidth={2.25} />}
                </span>
                <span className="min-w-0 flex-1 truncate">{option.label}</span>
              </div>
            )
          })}
        </div>,
        document.body,
      )
    : null

  return (
    <>
      <div
        ref={rootRef}
        className={classNames(rootStyles[variant], rootClassName)}
        data-select-root=""
      >
        <button
          {...buttonProps}
          ref={assignTriggerRef}
          id={triggerId}
          type={type}
          role="combobox"
          aria-label={ariaLabel}
          aria-labelledby={ariaLabelledBy}
          aria-haspopup="listbox"
          aria-expanded={open}
          aria-controls={listboxId}
          aria-activedescendant={open && activeIndex >= 0
            ? `${listboxId}-option-${activeIndex}`
            : undefined}
          disabled={disabled}
          className={classNames(
            triggerStyles[variant],
            variant === 'code' && (leadingIcon ? 'pl-8' : 'pl-3'),
            className,
          )}
          data-select-trigger=""
          onClick={(event) => {
            onTriggerClick?.(event)
            if (event.defaultPrevented) return
            if (open) closeMenu()
            else openMenu()
          }}
          onKeyDown={(event) => {
            onTriggerKeyDown?.(event)
            if (!event.defaultPrevented) handleSelectKeyDown(event)
          }}
        >
          {variant === 'icon' ? (
            <ChevronDown
              size={11}
              className={classNames('transition-transform', open && 'rotate-180')}
              aria-hidden="true"
            />
          ) : (
            <>
              {leadingIcon && (
                <span
                  className={classNames(
                    'pointer-events-none grid place-items-center text-ghost-green',
                    variant === 'code'
                      ? 'absolute left-3 top-1/2 -translate-y-1/2'
                      : 'mr-1 shrink-0',
                  )}
                  aria-hidden="true"
                >
                  {leadingIcon}
                </span>
              )}
              <span
                className={classNames(
                  'min-w-0 flex-1 truncate',
                  !selectedOption && 'text-ghost-faint',
                )}
                title={selectedText || undefined}
              >
                {selectedOption?.label ?? placeholder}
              </span>
              <ChevronDown
                size={variant === 'inline' ? 11 : 13}
                className={classNames(
                  'ml-1 shrink-0 text-ghost-faint transition-transform',
                  open && 'rotate-180 text-ghost-muted',
                )}
                aria-hidden="true"
              />
            </>
          )}
        </button>
      </div>
      {menu}
    </>
  )
})
