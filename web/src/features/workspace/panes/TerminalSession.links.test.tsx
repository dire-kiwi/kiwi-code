import { cleanup, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { renderWithStore } from '@/store/testing'
import { ThemeProvider } from '@/theme'
import { TerminalSession } from './TerminalSession'
import {
  FakeWebLinksAddon,
  installTerminalSessionDoubles,
  restoreTerminalSessionDoubles,
} from './terminalSessionTesting'

vi.mock('@xterm/xterm', async () => ({ Terminal: (await import('./terminalSessionTesting')).FakeTerminal }))
vi.mock('@xterm/addon-fit', async () => ({ FitAddon: (await import('./terminalSessionTesting')).FakeFitAddon }))
vi.mock('@xterm/addon-canvas', async () => ({ CanvasAddon: (await import('./terminalSessionTesting')).FakeCanvasAddon }))
vi.mock('@xterm/addon-web-links', async () => ({ WebLinksAddon: (await import('./terminalSessionTesting')).FakeWebLinksAddon }))

beforeEach(installTerminalSessionDoubles)

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  restoreTerminalSessionDoubles()
})

describe('TerminalSession links', () => {
  it('opens a clicked link by URL so the desktop app can hand it to the system browser', async () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    renderWithStore(
      <ThemeProvider>
        <TerminalSession
          projectId="project"
          threadId="thread"
          threadTitle="Thread"
          tool="terminal"
          active
          onStatusChange={() => {}}
        />
      </ThemeProvider>,
    )
    await waitFor(() => expect(FakeWebLinksAddon.instances).toHaveLength(1))

    // The addon's default handler is window.open() with no URL, then a
    // navigation of that blank window, which Electron's popup handler denies.
    const [links] = FakeWebLinksAddon.instances
    expect(links.handler).toBeTypeOf('function')
    links.handler?.(new MouseEvent('click'), 'https://example.com/docs?page=1')

    expect(open).toHaveBeenCalledWith('https://example.com/docs?page=1', '_blank', 'noopener,noreferrer')
  })
})
