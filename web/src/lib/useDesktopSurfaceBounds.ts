import { useLayoutEffect, type RefObject } from 'react'

type OperationResult<T> = void | T | Promise<void | T>
type DesktopSurfaceOptions<T> = {
  surfaceRef: RefObject<HTMLElement | null>
  owner?: object
  identityKey: string
  enabled: boolean
  stopOnError?: boolean
  show: (bounds: KiwiCodeDesktopBrowserBounds) => OperationResult<T>
  setBounds: (bounds: KiwiCodeDesktopBrowserBounds) => OperationResult<T>
  hide: () => OperationResult<unknown>
  onBeforeShow?: () => void
  onResult?: (result: T) => void
  onError: (reason: unknown) => void
}

export function desktopSurfaceBounds(rect: DOMRect): KiwiCodeDesktopBrowserBounds | null {
  const width = Math.round(rect.width)
  const height = Math.round(rect.height)
  return width < 1 || height < 1 ? null : {
    x: Math.max(0, Math.round(rect.left)),
    y: Math.max(0, Math.round(rect.top)),
    width,
    height,
  }
}

export function useDesktopSurfaceBounds<T>(options: DesktopSurfaceOptions<T>) {
  const { surfaceRef, owner, identityKey, enabled, stopOnError = false } = options
  useLayoutEffect(() => {
    if (!owner) return
    let disposed = false
    let failed = false
    let shown = false
    let animationFrame = 0

    function hide() {
      shown = false
      try {
        void Promise.resolve(options.hide()).catch(() => {})
      } catch {
        // Hiding is best effort during teardown and overlay transitions.
      }
    }

    function fail(reason: unknown) {
      if (disposed || failed) return
      failed = stopOnError
      hide()
      options.onError(reason)
    }

    function run(operation: () => OperationResult<T>) {
      try {
        void Promise.resolve(operation()).then((result) => {
          if (!disposed && result !== undefined) options.onResult?.(result)
        }, fail)
      } catch (reason) {
        fail(reason)
      }
    }

    function sync() {
      animationFrame = 0
      if (disposed || failed) return
      const bounds = surfaceRef.current
        ? desktopSurfaceBounds(surfaceRef.current.getBoundingClientRect())
        : null
      if (!bounds) {
        if (shown) hide()
        return
      }
      if (shown) {
        run(() => options.setBounds(bounds))
      } else {
        shown = true
        options.onBeforeShow?.()
        run(() => options.show(bounds))
      }
    }

    function schedule() {
      if (animationFrame) window.cancelAnimationFrame(animationFrame)
      animationFrame = window.requestAnimationFrame(sync)
    }

    if (!enabled) {
      hide()
      return () => {
        disposed = true
        hide()
      }
    }
    const observer = new ResizeObserver(schedule)
    if (surfaceRef.current) observer.observe(surfaceRef.current)
    window.addEventListener('resize', schedule)
    // Passive: schedule() only queues a frame, so the listener must not be
    // allowed to delay the scroll it is tracking.
    window.addEventListener('scroll', schedule, { capture: true, passive: true })
    sync()
    return () => {
      disposed = true
      if (animationFrame) window.cancelAnimationFrame(animationFrame)
      observer.disconnect()
      window.removeEventListener('resize', schedule)
      window.removeEventListener('scroll', schedule, true)
      hide()
    }
  }, [enabled, identityKey, owner, stopOnError, surfaceRef])
}
