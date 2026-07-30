import { afterEach, describe, expect, it, vi } from 'vitest'
import { memoryStorage } from '@/lib/memoryStorage'
import { createAppStore } from './index'
import { activeProfileSelected } from '@/store/slices/preferences'
import {
  moreThreadsToggled,
  projectCollapseToggled,
  sidebarViewChanged,
  sidebarWidthChanged,
} from '@/store/slices/sidebar'

// Longer than the 150ms persistence debounce.
const afterDebounce = 200

afterEach(() => {
  vi.useRealTimers()
})

describe('store persistence', () => {
  it('hydrates slices from storage before the first render', () => {
    const store = createAppStore({
      storage: memoryStorage({
        'kiwi-code-active-profile': 'work',
        'kiwi-code.sidebar.view': 'tree',
        'kiwi-code.sidebar.width': '320',
        'kiwi-code.sidebar.collapsed-projects': '["project-1"]',
        'kiwi-code.sidebar.web-servers-collapsed': 'true',
      }),
      persist: false,
    })

    expect(store.getState().preferences.activeProfileId).toBe('work')
    expect(store.getState().sidebar.view).toBe('tree')
    expect(store.getState().sidebar.width).toBe(320)
    expect(store.getState().sidebar.collapsedProjectIds).toEqual(['project-1'])
    expect(store.getState().sidebar.webServersCollapsed).toBe(true)
  })

  it('falls back for malformed values and clamps an out-of-range width', () => {
    const store = createAppStore({
      storage: memoryStorage({
        'kiwi-code.sidebar.view': 'grid',
        'kiwi-code.sidebar.width': '9000',
        'kiwi-code.sidebar.collapsed-projects': '{',
      }),
      persist: false,
    })

    expect(store.getState().sidebar.view).toBe('activity')
    expect(store.getState().sidebar.width).toBe(384)
    expect(store.getState().sidebar.collapsedProjectIds).toEqual([])
  })

  it('writes changes through with the historical key and encoding', async () => {
    vi.useFakeTimers()
    const storage = memoryStorage()
    const store = createAppStore({ storage })

    store.dispatch(projectCollapseToggled('project-1'))
    store.dispatch(activeProfileSelected('work'))
    await vi.advanceTimersByTimeAsync(afterDebounce)

    expect(storage.values.get('kiwi-code.sidebar.collapsed-projects')).toBe('["project-1"]')
    expect(storage.values.get('kiwi-code-active-profile')).toBe('work')
  })

  it('never writes back a value nobody changed', async () => {
    vi.useFakeTimers()
    const storage = memoryStorage({ 'kiwi-code.sidebar.width': '320' })
    const store = createAppStore({ storage })

    store.dispatch(sidebarViewChanged('tree'))
    await vi.advanceTimersByTimeAsync(afterDebounce)

    expect(storage.writes).toEqual(['kiwi-code.sidebar.view'])
    expect(storage.values.get('kiwi-code.sidebar.width')).toBe('320')
  })

  it('coalesces a burst of changes into a single write', async () => {
    vi.useFakeTimers()
    const storage = memoryStorage()
    const store = createAppStore({ storage })

    for (const width of [300, 320, 340, 360]) store.dispatch(sidebarWidthChanged(width))
    await vi.advanceTimersByTimeAsync(afterDebounce)

    expect(storage.writes).toEqual(['kiwi-code.sidebar.width'])
    expect(storage.values.get('kiwi-code.sidebar.width')).toBe('360')
  })

  it('skips writing when a change is reverted before the debounce elapses', async () => {
    vi.useFakeTimers()
    const storage = memoryStorage({ 'kiwi-code.sidebar.view': 'tree' })
    const store = createAppStore({ storage })

    store.dispatch(sidebarViewChanged('activity'))
    store.dispatch(sidebarViewChanged('tree'))
    await vi.advanceTimersByTimeAsync(afterDebounce)

    expect(storage.writes).toEqual([])
  })

  it('loads and stays usable when storage access throws', async () => {
    vi.useFakeTimers()
    const store = createAppStore({
      storage: {
        get length(): number {
          throw new Error('blocked')
        },
        key: () => {
          throw new Error('blocked')
        },
        getItem: () => {
          throw new Error('blocked')
        },
        setItem: () => {
          throw new Error('blocked')
        },
        removeItem: () => {
          throw new Error('blocked')
        },
      },
    })

    expect(store.getState().sidebar.view).toBe('activity')
    store.dispatch(sidebarViewChanged('tree'))
    await vi.advanceTimersByTimeAsync(afterDebounce)
    expect(store.getState().sidebar.view).toBe('tree')
  })

  it('cannot be starved by a steady stream of unrelated actions', async () => {
    vi.useFakeTimers()
    const storage = memoryStorage()
    const store = createAppStore({ storage })

    store.dispatch(sidebarWidthChanged(320))
    // Something else dispatching faster than the debounce would otherwise keep
    // resetting the timer and the width would never reach storage.
    for (let tick = 0; tick < 40; tick += 1) {
      await vi.advanceTimersByTimeAsync(50)
      store.dispatch(moreThreadsToggled(`project-${tick}`))
    }

    expect(storage.values.get('kiwi-code.sidebar.width')).toBe('320')
    vi.useRealTimers()
  })

  it('flushes pending writes when the page is hidden', async () => {
    vi.useFakeTimers()
    const storage = memoryStorage()
    const store = createAppStore({ storage })

    store.dispatch(sidebarWidthChanged(320))
    window.dispatchEvent(new Event('pagehide'))

    expect(storage.values.get('kiwi-code.sidebar.width')).toBe('320')
  })
})
