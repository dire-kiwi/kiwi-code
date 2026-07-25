import { useEffect, useState, type FormEvent } from 'react'
import { Check, Folder, FolderGit2, LoaderCircle, Save } from 'lucide-react'
import { updateProjectWorktreeBranchPrefix } from '../../../../api'
import type { Project } from '../../../../types'
import { PrimaryButton } from '../../../atoms/Button'
import { TextInput } from '../../../atoms/Input'
import { Surface } from '../../../atoms/Surface'
import { FeedbackMessage } from '../../../molecules/FeedbackMessage'
import { InfoCallout } from '../../../molecules/InfoCallout'
import { SectionHeader } from '../../../molecules/SectionHeader'

type ProjectBranchesSectionProps = {
  project: Project
  onProjectUpdated: (project: Project) => void
}

export function ProjectBranchesSection({ project, onProjectUpdated }: ProjectBranchesSectionProps) {
  const [branchPrefix, setBranchPrefix] = useState(project.worktreeBranchPrefix)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')

  useEffect(() => {
    setBranchPrefix(project.worktreeBranchPrefix)
    setError('')
    setMessage('')
  }, [project.id, project.worktreeBranchPrefix])

  const normalizedBranchPrefix = branchPrefix.trim()
  const dirty = normalizedBranchPrefix.length > 0
    && normalizedBranchPrefix !== project.worktreeBranchPrefix

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!dirty || saving) return

    setSaving(true)
    setError('')
    setMessage('')
    try {
      const updated = await updateProjectWorktreeBranchPrefix(project.id, normalizedBranchPrefix)
      onProjectUpdated(updated)
      setBranchPrefix(updated.worktreeBranchPrefix)
      setMessage('Branch prefix saved.')
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not update the branch prefix.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <Surface
        as="form"
        variant="elevated-panel"
        onSubmit={(event) => void handleSubmit(event)}
        className="overflow-hidden"
      >
        <SectionHeader
          icon={<FolderGit2 size={16} />}
          title="Worktree branches"
          description="Choose the branch prefix used for new managed worktree threads."
          tone="green"
        />

        <div className="p-4 sm:p-5">
          <label
            htmlFor="project-worktree-branch-prefix"
            className="block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim"
          >
            Branch prefix
            <TextInput
              id="project-worktree-branch-prefix"
              variant="code-large"
              value={branchPrefix}
              onChange={(event) => {
                setBranchPrefix(event.target.value)
                setError('')
                setMessage('')
              }}
              maxLength={100}
              disabled={saving}
              required
              autoComplete="off"
              spellCheck={false}
              placeholder="kiwi-code/"
              aria-describedby={error
                ? 'project-worktree-branch-prefix-error'
                : 'project-worktree-branch-prefix-help'}
              className="mt-2.5"
            />
          </label>

          <InfoCallout className="mt-4">
            Used for new managed worktree branches, including their automatic rename after the first prompt.
            Include separators such as <span className="font-mono text-ghost-blue">ivan/</span>. Existing
            branches are not renamed.
          </InfoCallout>

          {error && (
            <FeedbackMessage id="project-worktree-branch-prefix-error" role="alert" tone="error" className="mt-4">
              {error}
            </FeedbackMessage>
          )}
          {message && (
            <FeedbackMessage role="status" tone="success" size="status" className="mt-4 flex items-center gap-2">
              <Check size={13} />
              {message}
            </FeedbackMessage>
          )}
        </div>

        <div className="flex items-center justify-end border-t border-ghost-border/60 bg-ghost-black/15 px-4 py-3 sm:px-5">
          <PrimaryButton
            type="submit"
            size="md"
            disabled={!dirty || saving}
            className="flex min-w-28 items-center justify-center gap-2"
          >
            {saving ? <LoaderCircle size={14} className="animate-spin" /> : <Save size={14} />}
            Save prefix
          </PrimaryButton>
        </div>
      </Surface>

      <Surface as="section" variant="elevated-panel" className="overflow-hidden">
        <SectionHeader
          icon={<Folder size={16} />}
          title="Paths"
          description="Where this project lives on disk."
          tone="yellow"
        />

        <div className="p-4 sm:p-5">
          <div className="rounded-xl border border-ghost-border/55 bg-ghost-black/25 px-3.5 py-3">
            <p className="text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-faint">Project root</p>
            <p className="mt-1.5 break-all font-mono text-[10px] leading-4 text-ghost-muted" title={project.path}>
              {project.path}
            </p>
          </div>
        </div>
      </Surface>
    </>
  )
}
