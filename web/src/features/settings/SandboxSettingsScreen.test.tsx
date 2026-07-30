import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { SandboxConfigState } from '@/types'
import type { SubscriptionResult } from '@/wire/react'
import { SandboxSettingsScreen } from './SandboxSettingsScreen'

const subscription = vi.hoisted(() => ({
  current: {
    state: 'loading',
    retry: vi.fn(),
  } as SubscriptionResult<SandboxConfigState>,
}))

vi.mock('@/wire/react', () => ({
  useSubscription: () => subscription.current,
}))

const baseState: SandboxConfigState = {
  scope: 'global',
  path: '/tmp/kiwi-sandbox.json',
  exists: true,
  config: {},
  inherited: {
    defaults: { read: ['$CWD'], write: ['$CWD', '$TMPDIR'] },
    commands: [],
    network: false,
    pty: false,
    shell: '/bin/zsh',
    relatedProjects: [],
  },
  effective: {
    defaults: { read: ['$CWD'], write: ['$CWD', '$TMPDIR'] },
    commands: [],
    network: false,
    pty: false,
    shell: '/bin/zsh',
    relatedProjects: [],
  },
}

const retry = vi.fn()

afterEach(() => {
  retry.mockReset()
})

describe('SandboxSettingsScreen', () => {
  it('preserves a dirty draft across reconnect snapshots and external updates', async () => {
    subscription.current = { state: 'ready', data: baseState, retry }
    const view = render(<SandboxSettingsScreen scope="global" embedded />)

    const shell = await screen.findByRole('textbox', { name: 'Shell' })
    fireEvent.change(shell, { target: { value: '/bin/fish' } })
    expect((shell as HTMLInputElement).value).toBe('/bin/fish')

    subscription.current = { state: 'loading', retry }
    view.rerender(<SandboxSettingsScreen scope="global" embedded />)
    expect((screen.getByRole('textbox', { name: 'Shell' }) as HTMLInputElement).value).toBe('/bin/fish')

    subscription.current = {
      state: 'ready',
      data: { ...baseState },
      retry,
    }
    view.rerender(<SandboxSettingsScreen scope="global" embedded />)
    await waitFor(() => {
      expect((screen.getByRole('textbox', { name: 'Shell' }) as HTMLInputElement).value).toBe('/bin/fish')
    })

    subscription.current = {
      state: 'ready',
      data: {
        ...baseState,
        config: { shell: '/bin/bash' },
        effective: { ...baseState.effective, shell: '/bin/bash' },
      },
      retry,
    }
    view.rerender(<SandboxSettingsScreen scope="global" embedded />)
    await waitFor(() => {
      expect((screen.getByRole('textbox', { name: 'Shell' }) as HTMLInputElement).value).toBe('/bin/fish')
      expect(screen.getByText(/stored sandbox configuration changed while you were editing/i)).toBeTruthy()
    })

    view.unmount()
  })

  it('requires an explicit pseudo-terminal override', async () => {
    subscription.current = { state: 'ready', data: baseState, retry }
    render(<SandboxSettingsScreen scope="global" embedded />)

    const pty = await screen.findByLabelText('Pseudo-terminal access')
    expect(pty.textContent).toContain('Inherit (disabled)')
    fireEvent.click(pty)
    fireEvent.click(await screen.findByRole('option', { name: 'Enabled' }))
    expect(pty.textContent).toContain('Enabled')
    expect((screen.getByRole('button', { name: 'Save sandbox config' }) as HTMLButtonElement).disabled).toBe(false)
  })
})
