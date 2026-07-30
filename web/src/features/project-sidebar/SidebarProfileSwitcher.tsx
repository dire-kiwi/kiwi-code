import { useState } from 'react'
import { createProfile } from '@/api'
import type { Profile } from '@/types'
import { Select } from '@/ui/inputs'

// A sentinel option rather than a separate button: the picker is the only place
// a profile is chosen, so creating one belongs in the same list.
const newProfileValue = '__new-profile__'

export type SidebarProfileSwitcherProps = {
  profiles: Profile[]
  activeProfileId: string
  onSelectProfile: (profileId: string) => void
  onProfileCreated: (profile: Profile) => void
}

export function SidebarProfileSwitcher({
  profiles,
  activeProfileId,
  onSelectProfile,
  onProfileCreated,
}: SidebarProfileSwitcherProps) {
  const [creating, setCreating] = useState(false)

  async function handleSelection(profileId: string) {
    if (profileId !== newProfileValue) {
      onSelectProfile(profileId)
      return
    }

    const name = window.prompt('Name the new profile')?.trim()
    if (!name) return
    setCreating(true)
    try {
      const profile = await createProfile(name)
      onProfileCreated(profile)
    } catch (reason) {
      window.alert(reason instanceof Error ? reason.message : 'Could not create that profile.')
    } finally {
      setCreating(false)
    }
  }

  return (
    <label className="inline-flex min-w-0 shrink-0 items-center">
      <span className="sr-only">Current profile</span>
      <Select
        variant="inline"
        value={activeProfileId}
        options={[
          ...profiles.map((profile) => ({ value: profile.id, label: profile.name })),
          { value: newProfileValue, label: '＋ New profile…' },
        ]}
        onChange={(profileId) => void handleSelection(profileId)}
        disabled={creating}
        className="min-w-0 max-w-36"
        aria-label="Current profile"
      />
    </label>
  )
}
