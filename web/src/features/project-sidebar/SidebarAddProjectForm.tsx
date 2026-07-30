import { useState, type FormEvent } from 'react'
import { FolderGit2, LoaderCircle, X } from 'lucide-react'
import { createProject } from '@/api'
import type { Profile, Project } from '@/types'
import { Button } from '@/ui/buttons'
import { ProjectPathAutocomplete, TextInput } from '@/ui/inputs'

export type SidebarAddProjectFormProps = {
  activeProfile: Profile | undefined
  activeProfileId: string
  onProjectCreated: (project: Project) => void
  onCancel: () => void
}

export function SidebarAddProjectForm({
  activeProfile,
  activeProfileId,
  onProjectCreated,
  onCancel,
}: SidebarAddProjectFormProps) {
  const [name, setName] = useState('')
  const [path, setPath] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setSubmitting(true)
    setError('')
    try {
      onProjectCreated(await createProject({ name, path, profileId: activeProfileId }))
      setName('')
      setPath('')
      onCancel()
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not add that project.')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <form onSubmit={handleSubmit} className="relative z-10 mx-2 mt-2 rounded-lg border border-ghost-border/70 bg-ghost-panel p-3">
      <div className="mb-3 flex items-center justify-between">
        <p className="text-[10px] font-semibold text-ghost-bright-white">
          Add local project{activeProfile ? ` to ${activeProfile.name}` : ''}
        </p>
        <Button type="button" onClick={onCancel} aria-label="Cancel" className="text-ghost-dim">
          <X size={12} />
        </Button>
      </div>
      <TextInput
        value={name}
        onChange={(event) => setName(event.target.value)}
        maxLength={80}
        placeholder="Project name (optional)"
      />
      <ProjectPathAutocomplete
        value={path}
        disabled={submitting}
        onChange={setPath}
      />
      {error && <p role="alert" className="mt-2 text-[11px] text-ghost-bright-red">{error}</p>}
      <Button
        type="submit"
        variant="primary-static"
        disabled={submitting || !path.trim()}
        className="mt-3 flex h-8 w-full items-center justify-center gap-2 rounded-md text-[10px]"
      >
        {submitting ? <LoaderCircle size={12} className="animate-spin" /> : <FolderGit2 size={12} />}
        Add project
      </Button>
    </form>
  )
}
