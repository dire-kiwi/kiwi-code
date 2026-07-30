import { useState, type FormEvent } from 'react'
import { LoaderCircle, Palette, RotateCcw, Save } from 'lucide-react'
import { updateSettings } from '../../../../api'
import { useAsyncFeedback } from '../../../../lib/useAsyncFeedback'
import { isHexColor } from '../../../../lib/validation'
import { themesEqual, useTheme } from '../../../../theme'
import type { AppSettings, ThemeColors, ThemeSettings } from '../../../../types'
import { GhostButton, PrimaryButton } from '@/ui/buttons'
import { TextInput } from '@/ui/inputs'
import { ActionFeedback, InfoCallout, StatusBadge } from '@/ui/feedback'
import { SectionHeader, Surface } from '@/ui/layout'
import { ThemeColorInput } from '../../../molecules/ThemeColorInput'

type AppearanceSectionProps = {
  settings: AppSettings
  onSettingsUpdated: (settings: AppSettings) => void
}

type ThemeColorGroup = {
  title: string
  description: string
  colors: Array<{ key: keyof ThemeColors; label: string }>
}

const themeColorGroups: ThemeColorGroup[] = [
  {
    title: 'Interface',
    description: 'Application surfaces and text',
    colors: [
      { key: 'canvas', label: 'Canvas' },
      { key: 'sidebar', label: 'Sidebar' },
      { key: 'background', label: 'Terminal' },
      { key: 'panel', label: 'Panel' },
      { key: 'raised', label: 'Raised' },
      { key: 'selected', label: 'Selected' },
      { key: 'border', label: 'Border' },
      { key: 'foreground', label: 'Foreground' },
      { key: 'muted', label: 'Muted text' },
      { key: 'dim', label: 'Dim text' },
    ],
  },
  {
    title: 'Cursor & selection',
    description: 'Terminal interaction colors',
    colors: [
      { key: 'cursor', label: 'Cursor' },
      { key: 'selectionBackground', label: 'Selection' },
      { key: 'selectionForeground', label: 'Selected text' },
    ],
  },
  {
    title: 'Normal palette',
    description: 'ANSI terminal colors 0–7',
    colors: [
      { key: 'black', label: 'Black' },
      { key: 'red', label: 'Red' },
      { key: 'green', label: 'Green' },
      { key: 'yellow', label: 'Yellow' },
      { key: 'blue', label: 'Blue' },
      { key: 'magenta', label: 'Magenta' },
      { key: 'cyan', label: 'Cyan' },
      { key: 'white', label: 'White' },
    ],
  },
  {
    title: 'Bright palette',
    description: 'ANSI terminal colors 8–15',
    colors: [
      { key: 'brightBlack', label: 'Bright black' },
      { key: 'brightRed', label: 'Bright red' },
      { key: 'brightGreen', label: 'Bright green' },
      { key: 'brightYellow', label: 'Bright yellow' },
      { key: 'brightBlue', label: 'Bright blue' },
      { key: 'brightMagenta', label: 'Bright magenta' },
      { key: 'brightCyan', label: 'Bright cyan' },
      { key: 'brightWhite', label: 'Bright white' },
    ],
  },
]

export function AppearanceSection({ settings, onSettingsUpdated }: AppearanceSectionProps) {
  const { setTheme: applyTheme } = useTheme()
  const [theme, setTheme] = useState<ThemeSettings>(settings.theme)
  const action = useAsyncFeedback<'save' | 'reset'>()
  const saving = action.pendingAction

  const validTheme = theme.fontFamily.trim().length > 0
    && theme.fontFamily.trim().length <= 512
    && Number.isInteger(theme.fontSize)
    && theme.fontSize >= 6
    && theme.fontSize <= 72
    && Object.values(theme.colors).every(isHexColor)
  const dirty = !themesEqual(theme, settings.theme)
  const canReset = !settings.usingDefaultTheme || !themesEqual(theme, settings.defaultTheme)

  function updateThemeColor(key: keyof ThemeColors, value: string) {
    setTheme((current) => ({
      ...current,
      colors: { ...current.colors, [key]: value },
    }))
    action.clearFeedback()
  }

  async function handleSave(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (saving) return

    const next = await action.run(
      'save',
      () => updateSettings({
        theme: { ...theme, fontFamily: theme.fontFamily.trim() },
      }),
      {
        success: 'Appearance saved and applied.',
        failure: 'Could not save appearance settings.',
      },
    )
    if (!next) return
    onSettingsUpdated(next)
    setTheme(next.theme)
    applyTheme(next.theme)
  }

  async function handleReset() {
    if (saving) return
    const next = await action.run(
      'reset',
      () => updateSettings({ theme: settings.defaultTheme }),
      {
        success: 'Appearance reset to the default theme.',
        failure: 'Could not reset appearance settings.',
      },
    )
    if (!next) return
    onSettingsUpdated(next)
    setTheme(next.theme)
    applyTheme(next.theme)
  }

  return (
    <Surface
      as="form"
      variant="elevated-panel"
      onSubmit={(event) => void handleSave(event)}
      className="overflow-hidden"
    >
      <SectionHeader
        icon={<Palette size={16} />}
        title="Appearance"
        description="Set the terminal typeface and size, interface surfaces, and complete ANSI color palette."
        tone="magenta"
        badge={(
          <StatusBadge tone={settings.usingDefaultTheme ? 'neutral' : 'success'}>
            {settings.usingDefaultTheme ? 'Default' : 'Custom'}
          </StatusBadge>
        )}
      />

      <div className="p-4 sm:p-5">
        <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_8rem]">
          <label className="block text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-dim">
            Font family
            <TextInput
              variant="code"
              value={theme.fontFamily}
              onChange={(event) => {
                setTheme((current) => ({ ...current, fontFamily: event.target.value }))
                action.clearFeedback()
              }}
              maxLength={512}
              autoComplete="off"
              spellCheck={false}
              className="mt-1.5"
            />
          </label>
          <label className="block text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-dim">
            Font size
            <TextInput
              variant="code"
              type="number"
              min={6}
              max={72}
              step={1}
              value={theme.fontSize}
              onChange={(event) => {
                setTheme((current) => ({ ...current, fontSize: Number(event.target.value) }))
                action.clearFeedback()
              }}
              className="mt-1.5"
            />
          </label>
        </div>

        <div
          className="mt-4 overflow-hidden rounded-xl border border-ghost-border/70 px-4 py-3"
          style={{
            backgroundColor: theme.colors.background,
            color: theme.colors.foreground,
            fontFamily: theme.fontFamily,
            fontSize: `${Math.min(Math.max(theme.fontSize || 6, 6), 24)}px`,
          }}
          aria-label="Theme preview"
        >
          <p className="truncate leading-relaxed">The quick brown fox jumps over the lazy dog.</p>
          <p className="mt-1 truncate leading-relaxed">
            <span style={{ color: theme.colors.green }}>➜</span>{' '}
            <span style={{ color: theme.colors.blue }}>~/kiwi-code</span>{' '}
            <span style={{ color: theme.colors.muted }}>git:(</span>
            <span style={{ color: theme.colors.red }}>main</span>
            <span style={{ color: theme.colors.muted }}>)</span>
          </p>
        </div>

        <div className="mt-5 space-y-5">
          {themeColorGroups.map((group) => (
            <section key={group.title}>
              <div className="mb-2.5 flex flex-wrap items-baseline justify-between gap-1">
                <h3 className="text-[10px] font-semibold uppercase tracking-[0.12em] text-ghost-dim">
                  {group.title}
                </h3>
                <p className="text-[9px] text-ghost-faint">{group.description}</p>
              </div>
              <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-3">
                {group.colors.map((color) => (
                  <ThemeColorInput
                    key={color.key}
                    label={color.label}
                    value={theme.colors[color.key]}
                    onChange={(value) => updateThemeColor(color.key, value)}
                  />
                ))}
              </div>
            </section>
          ))}
        </div>

        <InfoCallout className="mt-5">
          Colors use six-digit hexadecimal values. Font sizes from 6 to 72 pixels are supported.
          Saving applies the theme to the interface and every terminal you open.
        </InfoCallout>

        <ActionFeedback feedback={action.feedback} className="mt-4" />
      </div>

      <div className="flex flex-wrap items-center justify-end gap-2 border-t border-ghost-border/60 bg-ghost-black/15 px-4 py-3 sm:px-5">
        <GhostButton
          type="button"
          size="md"
          onClick={() => void handleReset()}
          disabled={!canReset || Boolean(saving)}
          className="mr-auto flex items-center gap-2 px-3 disabled:cursor-not-allowed disabled:opacity-35"
        >
          {saving === 'reset' ? <LoaderCircle size={13} className="animate-spin" /> : <RotateCcw size={13} />}
          Reset to default
        </GhostButton>
        <PrimaryButton
          type="submit"
          size="md"
          disabled={!dirty || !validTheme || Boolean(saving)}
          className="flex min-w-28 items-center justify-center gap-2"
        >
          {saving === 'save' ? <LoaderCircle size={14} className="animate-spin" /> : <Save size={14} />}
          Save theme
        </PrimaryButton>
      </div>
    </Surface>
  )
}
