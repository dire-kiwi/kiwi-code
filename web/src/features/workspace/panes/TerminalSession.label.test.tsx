import { cleanup, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { renderWithStore } from '@/store/testing'
import { ThemeProvider } from '@/theme'
import { TerminalSession } from './TerminalSession'
import {
  FakeTerminal,
  FakeTerminalSocket,
  installTerminalSessionDoubles,
  restoreTerminalSessionDoubles,
} from './terminalSessionTesting'

vi.mock('@xterm/xterm', async () => ({ Terminal: (await import('./terminalSessionTesting')).FakeTerminal }))
vi.mock('@xterm/addon-fit', async () => ({ FitAddon: (await import('./terminalSessionTesting')).FakeFitAddon }))
vi.mock('@xterm/addon-canvas', async () => ({ CanvasAddon: (await import('./terminalSessionTesting')).FakeCanvasAddon }))
vi.mock('@xterm/addon-web-links', async () => ({ WebLinksAddon: (await import('./terminalSessionTesting')).FakeWebLinksAddon }))

function agentTerminal(terminalLabel: string) {
  return (
    <ThemeProvider>
      <TerminalSession
        projectId="project"
        threadId="thread"
        threadTitle="Thread"
        tool="pi"
        codingAgent="claude-profile-personal"
        terminalLabel={terminalLabel}
        active
        onStatusChange={() => {}}
      />
    </ThemeProvider>
  )
}

beforeEach(installTerminalSessionDoubles)

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  restoreTerminalSessionDoubles()
})

describe('TerminalSession label', () => {
  it('relabels a live terminal without recreating it or its socket', async () => {
    // Before settings arrive, a Claude profile falls back to the first agent
    // choice's label; the real one replaces it a moment later.
    const view = renderWithStore(agentTerminal('Pi'))
    await waitFor(() => expect(FakeTerminalSocket.instances).toHaveLength(1))
    const [terminal] = FakeTerminal.instances
    expect(terminal.textarea?.getAttribute('aria-label')).toBe('Thread Pi terminal input')
    expect(screen.getByText('Starting Pi…')).toBeTruthy()

    view.rerender(agentTerminal('Claude Personal'))

    expect(screen.getByText('Starting Claude Personal…')).toBeTruthy()
    expect(terminal.textarea?.getAttribute('aria-label')).toBe('Thread Claude Personal terminal input')
    // Give a teardown's deferred re-initialisation a frame to show up.
    await new Promise((resolve) => requestAnimationFrame(resolve))
    expect(FakeTerminal.instances).toHaveLength(1)
    expect(terminal.disposed).toBe(false)
    expect(FakeTerminalSocket.instances).toHaveLength(1)
  })
})
