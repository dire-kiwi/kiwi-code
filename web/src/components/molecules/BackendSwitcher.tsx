import { Server } from 'lucide-react'
import { useEffect, useState, type FormEvent, type KeyboardEvent } from 'react'
import { createPortal } from 'react-dom'
import {
  activeBackendOrigin,
  forgetBackendOrigin,
  listBackendChoices,
  rememberBackendOrigin,
  selectBackendOrigin,
} from '../../backends'
import { Button } from '../atoms/Button'
import { TextInput } from '../atoms/Input'
import { Select, type SelectOption } from '../atoms/Select'

const addBackendValue = '__kiwi_code_add_backend__'
const forgetBackendValue = '__kiwi_code_forget_backend__'

function reportDesktopBackend(origin: string) {
  const bridge = window.kiwiCodeDesktopBrowser ?? window.direMuxDesktopBrowser
  const result = bridge?.setBackendOrigin?.(origin)
  if (result && typeof result.then === 'function') {
    void result.catch((error) => console.warn('Could not update the desktop backend origin.', error))
  }
}

function reloadForBackend(origin: string) {
  reportDesktopBackend(origin)
  window.location.reload()
}

export function BackendSwitcher() {
  const [addingBackend, setAddingBackend] = useState(false)
  const [backendInput, setBackendInput] = useState('')
  const [backendError, setBackendError] = useState('')
  const backends = listBackendChoices()
  const activeBackend = backends.find((backend) => backend.origin === activeBackendOrigin) ?? backends[0]
  const options: SelectOption[] = backends.map((backend) => ({
    value: backend.origin,
    label: backend.isDefault ? `This server · ${backend.label}` : backend.label,
    textValue: `${backend.label} ${backend.origin}`,
  }))
  options.push({ value: addBackendValue, label: '＋ Add backend…' })
  if (activeBackend && !activeBackend.isDefault) {
    options.push({ value: forgetBackendValue, label: `− Forget ${activeBackend.label}` })
  }

  useEffect(() => {
    reportDesktopBackend(activeBackendOrigin)
  }, [])

  function handleSelection(value: string) {
    try {
      if (value === addBackendValue) {
        setBackendInput('')
        setBackendError('')
        setAddingBackend(true)
        return
      }

      if (value === forgetBackendValue) {
        if (activeBackend) reloadForBackend(forgetBackendOrigin(activeBackend.origin))
        return
      }

      if (value === activeBackendOrigin) return
      reloadForBackend(selectBackendOrigin(value))
    } catch (reason) {
      window.alert(reason instanceof Error ? reason.message : 'Could not save that backend.')
    }
  }

  function handleBackendSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setBackendError('')
    try {
      reloadForBackend(rememberBackendOrigin(backendInput))
    } catch (reason) {
      setBackendError(reason instanceof Error ? reason.message : 'Could not save that backend.')
    }
  }

  function handleDialogKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (event.key !== 'Escape') return
    event.preventDefault()
    setAddingBackend(false)
  }

  const dialog = addingBackend && typeof document !== 'undefined'
    ? createPortal(
        <div
          className="fixed inset-0 z-[110] grid place-items-center bg-ghost-black/80 p-4 backdrop-blur-sm"
          onPointerDown={(event) => {
            if (event.target === event.currentTarget) setAddingBackend(false)
          }}
          onKeyDown={handleDialogKeyDown}
        >
          <form
            role="dialog"
            aria-modal="true"
            aria-labelledby="add-backend-title"
            onSubmit={handleBackendSubmit}
            className="w-full max-w-md rounded-xl border border-ghost-border/90 bg-ghost-panel p-4 shadow-2xl"
          >
            <div className="flex items-center gap-2">
              <Server size={14} className="text-ghost-green" aria-hidden="true" />
              <h2 id="add-backend-title" className="text-xs font-semibold text-ghost-bright-white">
                Connect to backend
              </h2>
            </div>
            <p className="mt-2 text-[10px] leading-4 text-ghost-muted">
              Enter another Kiwi Code server URL. Bare machine names use HTTP port 4000.
            </p>
            <label className="mt-4 block">
              <span className="sr-only">Backend URL</span>
              <TextInput
                autoFocus
                type="text"
                inputMode="url"
                spellCheck={false}
                autoCapitalize="none"
                autoComplete="off"
                variant="code"
                value={backendInput}
                onChange={(event) => {
                  setBackendInput(event.target.value)
                  if (backendError) setBackendError('')
                }}
                placeholder="http://machine-name:4000"
                aria-label="Backend URL"
                aria-describedby={backendError ? 'backend-url-error' : undefined}
              />
            </label>
            {backendError && (
              <p id="backend-url-error" role="alert" className="mt-2 text-[10px] text-ghost-bright-red">
                {backendError}
              </p>
            )}
            <div className="mt-4 flex justify-end gap-2">
              <Button
                type="button"
                variant="subtle"
                onClick={() => setAddingBackend(false)}
                className="h-8 rounded-md px-3 text-[10px]"
              >
                Cancel
              </Button>
              <Button
                type="submit"
                variant="primary-static"
                disabled={!backendInput.trim()}
                className="h-8 rounded-md px-3 text-[10px]"
              >
                Connect
              </Button>
            </div>
          </form>
        </div>,
        document.body,
      )
    : null

  return (
    <>
      <div className="flex h-9 shrink-0 items-center gap-2 border-b border-ghost-border/70 bg-ghost-panel/20 px-3">
        <Server size={12} className="shrink-0 text-ghost-green" aria-hidden="true" />
        <span className="shrink-0 font-mono text-[8px] uppercase tracking-[0.12em] text-ghost-faint">
          Backend
        </span>
        <Select
          variant="inline"
          value={activeBackendOrigin}
          options={options}
          onChange={handleSelection}
          className="!max-w-none flex-1"
          rootClassName="min-w-0 flex-1"
          menuClassName="min-w-56"
          aria-label="Current backend"
          title={activeBackend?.origin}
        />
      </div>
      {dialog}
    </>
  )
}
