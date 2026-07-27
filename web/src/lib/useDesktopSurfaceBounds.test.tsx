import { act, cleanup, render } from '@testing-library/react'
import { useRef } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  desktopSurfaceBounds,
  useDesktopSurfaceBounds,
} from './useDesktopSurfaceBounds'

type TestResult = { status: string }
type HookOptions = Parameters<typeof useDesktopSurfaceBounds<TestResult>>[0]

const observers: TestResizeObserver[] = []

class TestResizeObserver implements ResizeObserver {
  constructor(private readonly callback: ResizeObserverCallback) {
    observers.push(this)
  }

  disconnect = vi.fn()
  observe = vi.fn()
  unobserve = vi.fn()

  trigger() {
    this.callback([], this)
  }
}

function rect({
  left = 10.4,
  top = 20.6,
  width = 300.2,
  height = 199.7,
}: Partial<DOMRect> = {}): DOMRect {
  return {
    x: left,
    y: top,
    left,
    top,
    right: left + width,
    bottom: top + height,
    width,
    height,
    toJSON: () => ({}),
  }
}

function SurfaceHarness(
  properties: Omit<HookOptions, 'surfaceRef'>,
) {
  const surfaceRef = useRef<HTMLDivElement>(null)
  useDesktopSurfaceBounds({ ...properties, surfaceRef })
  return <div ref={surfaceRef} />
}

beforeEach(() => {
  observers.length = 0
  vi.stubGlobal('ResizeObserver', TestResizeObserver)
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue(rect())
  vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
    callback(0)
    return 1
  })
  vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {})
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function options(overrides: Partial<HookOptions> = {}) {
  return {
    owner: {},
    identityKey: 'project/thread',
    enabled: true,
    show: vi.fn(() => ({ status: 'shown' })),
    setBounds: vi.fn(() => ({ status: 'resized' })),
    hide: vi.fn(),
    onBeforeShow: vi.fn(),
    onResult: vi.fn(),
    onError: vi.fn(),
    ...overrides,
  } satisfies Omit<HookOptions, 'surfaceRef'>
}

describe('desktop surface bounds', () => {
  it('rounds dimensions and clamps negative origins', () => {
    expect(desktopSurfaceBounds(rect({ left: -12.8, top: -1.2 }))).toEqual({
      x: 0,
      y: 0,
      width: 300,
      height: 200,
    })
    expect(desktopSurfaceBounds(rect({ width: 0.4 }))).toBeNull()
  })

  it('shows once, applies later bounds, and hides on unmount', async () => {
    const configuration = options()
    const view = render(<SurfaceHarness {...configuration} />)
    await act(async () => {})

    expect(configuration.onBeforeShow).toHaveBeenCalledOnce()
    expect(configuration.show).toHaveBeenCalledWith({
      x: 10,
      y: 21,
      width: 300,
      height: 200,
    })
    expect(configuration.onResult).toHaveBeenCalledWith({ status: 'shown' })

    await act(async () => observers[0].trigger())
    expect(configuration.setBounds).toHaveBeenCalledOnce()
    expect(configuration.onResult).toHaveBeenLastCalledWith({ status: 'resized' })

    view.unmount()
    expect(configuration.hide).toHaveBeenCalledOnce()
    expect(observers[0].disconnect).toHaveBeenCalledOnce()
  })

  it('hides while disabled and shows after becoming enabled', () => {
    const configuration = options({ enabled: false })
    const view = render(<SurfaceHarness {...configuration} />)

    expect(configuration.hide).toHaveBeenCalledOnce()
    expect(configuration.show).not.toHaveBeenCalled()

    view.rerender(<SurfaceHarness {...configuration} enabled />)
    expect(configuration.show).toHaveBeenCalledOnce()
  })

  it('reports an asynchronous failure and stops syncing when requested', async () => {
    const showFailure = Promise.reject(new Error('native surface failed'))
    const configuration = options({
      stopOnError: true,
      show: vi.fn(() => showFailure),
    })
    render(<SurfaceHarness {...configuration} />)

    await act(async () => {
      await showFailure.catch(() => {})
    })
    expect(configuration.onError).toHaveBeenCalledWith(new Error('native surface failed'))
    expect(configuration.hide).toHaveBeenCalledOnce()

    act(() => observers[0].trigger())
    expect(configuration.setBounds).not.toHaveBeenCalled()
  })

  it('ignores an operation result that settles after unmount', async () => {
    let resolve!: (result: TestResult) => void
    const pending = new Promise<TestResult>((next) => {
      resolve = next
    })
    const configuration = options({ show: vi.fn(() => pending) })
    const view = render(<SurfaceHarness {...configuration} />)
    view.unmount()

    await act(async () => {
      resolve({ status: 'late' })
      await pending
    })
    expect(configuration.onResult).not.toHaveBeenCalled()
  })
})
