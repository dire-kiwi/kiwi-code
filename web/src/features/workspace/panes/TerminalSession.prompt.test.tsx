import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { renderWithStore } from '@/store/testing'
import { ThemeProvider } from '@/theme'
import { TerminalSession } from './TerminalSession'
import {
  FakeTerminalSocket,
  installTerminalSessionDoubles,
  restoreTerminalSessionDoubles,
} from './terminalSessionTesting'

vi.mock('@xterm/xterm', async () => ({ Terminal: (await import('./terminalSessionTesting')).FakeTerminal }))
vi.mock('@xterm/addon-fit', async () => ({ FitAddon: (await import('./terminalSessionTesting')).FakeFitAddon }))
vi.mock('@xterm/addon-canvas', async () => ({ CanvasAddon: (await import('./terminalSessionTesting')).FakeCanvasAddon }))
vi.mock('@xterm/addon-web-links', async () => ({ WebLinksAddon: (await import('./terminalSessionTesting')).FakeWebLinksAddon }))

const prompt = 'Delete the generated fixtures and push'

function renderAgentTerminal(onInitialPromptSent = vi.fn()) {
  renderWithStore(
    <ThemeProvider>
      <TerminalSession
        projectId="project"
        threadId="thread"
        threadTitle="Thread"
        tool="pi"
        codingAgent="claude"
        initialPrompt={prompt}
        onInitialPromptSent={onInitialPromptSent}
        active
        onStatusChange={() => {}}
      />
    </ThemeProvider>,
  )
  return onInitialPromptSent
}

async function socketNumber(count: number) {
  await waitFor(() => expect(FakeTerminalSocket.instances).toHaveLength(count))
  return FakeTerminalSocket.instances[count - 1]
}

beforeEach(installTerminalSessionDoubles)

afterEach(() => {
  cleanup()
  vi.useRealTimers()
  vi.unstubAllGlobals()
  restoreTerminalSessionDoubles()
})

describe('TerminalSession initial prompt', () => {
  it('does not send the delivered prompt to a restarted coding agent', async () => {
    const onInitialPromptSent = renderAgentTerminal()
    const first = await socketNumber(1)
    expect(first.url.searchParams.get('prompt')).toBe(prompt)

    act(() => {
      first.open()
      first.ready()
    })
    expect(onInitialPromptSent).toHaveBeenCalledTimes(1)

    act(() => first.serverClose(1000, 'Coding agent ended'))
    fireEvent.click(await screen.findByRole('button', { name: 'Start Claude Code' }))

    const restarted = await socketNumber(2)
    expect(restarted.url.searchParams.get('restart')).toBe('1')
    expect(restarted.url.searchParams.has('prompt')).toBe(false)
  })

  it('does not resend the delivered prompt when the socket reconnects', async () => {
    renderAgentTerminal()
    const first = await socketNumber(1)
    act(() => {
      first.open()
      first.ready()
      first.serverClose(1006)
    })

    const reconnected = await socketNumber(2)
    expect(reconnected.url.searchParams.has('restart')).toBe(false)
    expect(reconnected.url.searchParams.has('prompt')).toBe(false)
  })

  it('keeps the prompt for a reconnect before the agent was ready', async () => {
    const onInitialPromptSent = renderAgentTerminal()
    const first = await socketNumber(1)
    act(() => first.serverClose(1006))

    const reconnected = await socketNumber(2)
    expect(reconnected.url.searchParams.get('prompt')).toBe(prompt)
    expect(onInitialPromptSent).not.toHaveBeenCalled()
  })
})
