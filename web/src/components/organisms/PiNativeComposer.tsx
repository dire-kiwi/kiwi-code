import type {
  ChangeEventHandler,
  ClipboardEventHandler,
  DragEventHandler,
  KeyboardEventHandler,
  ReactNode,
  RefObject,
} from 'react'
import {
  Activity,
  ArrowUp,
  CircleAlert,
  ImagePlus,
  LoaderCircle,
  Square,
  X,
} from 'lucide-react'
import { classNames } from '../../lib/classNames'
import {
  formatImageSize,
  PI_IMAGE_ACCEPT,
} from '../../lib/promptImages'
import type { ImageAttachment } from '../../lib/useImageAttachments'
import { AgentModelControls, type AgentModelOption } from '../molecules/AgentModelControls'
import type { PiStatusTone } from './PiNativeActivityPanel'
import {
  piNativeActivityToneStyles,
  piNativeCommandSourceStyles,
  piNativeStyles,
} from './piNativeStyles'

export type PiNativeComposerSuggestion = {
  id: string
  label: string
  description: string
  source: keyof typeof piNativeCommandSourceStyles
}

type PiNativeComposerProps = {
  agentName?: string
  readOnly: boolean
  monitorTone: PiStatusTone
  activityExpanded: boolean
  activityToggleLabel: string
  isStreaming: boolean
  queuedMessages: string[]
  notice: string
  error: string
  activityPanel: ReactNode
  suggestions: PiNativeComposerSuggestion[]
  selectedSuggestionIndex: number
  onToggleActivity: () => void
  onSelectSuggestion: (index: number) => void
  attachments: ImageAttachment[]
  isUploadingImages: boolean
  onRemoveAttachment: (id: number) => void
  textareaRef: RefObject<HTMLTextAreaElement | null>
  draft: string
  onDraftChange: (value: string) => void
  onPaste: ClipboardEventHandler<HTMLTextAreaElement>
  onDrop: DragEventHandler<HTMLTextAreaElement>
  onKeyDown: KeyboardEventHandler<HTMLTextAreaElement>
  model: string
  modelOptions: AgentModelOption[]
  modelDisabled: boolean
  onModelChange: (value: string) => void
  thinking: string
  thinkingOptions: AgentModelOption[]
  thinkingDisabled: boolean
  onThinkingChange: (value: string) => void
  onImageInput: ChangeEventHandler<HTMLInputElement>
  hint: string
  primaryActionIsStop: boolean
  canSend: boolean
  onPrimaryAction: () => void
}

export function PiNativeComposer({
  agentName = 'Pi',
  readOnly,
  monitorTone,
  activityExpanded,
  activityToggleLabel,
  isStreaming,
  queuedMessages,
  notice,
  error,
  activityPanel,
  suggestions,
  selectedSuggestionIndex,
  onToggleActivity,
  onSelectSuggestion,
  attachments,
  isUploadingImages,
  onRemoveAttachment,
  textareaRef,
  draft,
  onDraftChange,
  onPaste,
  onDrop,
  onKeyDown,
  model,
  modelOptions,
  modelDisabled,
  onModelChange,
  thinking,
  thinkingOptions,
  thinkingDisabled,
  onThinkingChange,
  onImageInput,
  hint,
  primaryActionIsStop,
  canSend,
  onPrimaryAction,
}: PiNativeComposerProps) {
  const selectedSuggestion = suggestions[selectedSuggestionIndex]

  return (
    <footer className={piNativeStyles.composerWrap}>
      <div className={piNativeStyles.composerStatus}>
        <button
          type="button"
          className={classNames(
            piNativeStyles.activityToggle,
            piNativeActivityToneStyles[monitorTone],
          )}
          aria-expanded={activityExpanded}
          data-testid="pi-native-activity-toggle"
          onClick={onToggleActivity}
        >
          {isStreaming
            ? <LoaderCircle size={11} className={piNativeStyles.spin} />
            : <Activity size={11} />}
          <span>{activityToggleLabel}</span>
        </button>
      </div>

      <div className={piNativeStyles.composer}>
        {queuedMessages.length > 0 && (
          <div className={piNativeStyles.queue} aria-label="Queued prompts">
            <span className={piNativeStyles.queueLabel}>Queued</span>
            {queuedMessages.map((message, index) => (
              <span className={piNativeStyles.queueMessage} key={`${message}:${index}`}>{message}</span>
            ))}
          </div>
        )}
        {notice && <div className={piNativeStyles.notice} role="status">{notice}</div>}
        {error && (
          <div className={piNativeStyles.error} role="alert">
            <CircleAlert size={13} />
            <span>{error}</span>
          </div>
        )}
        {activityPanel}

        {suggestions.length > 0 && (
          <div
            id="pi-native-command-menu"
            className={piNativeStyles.commandMenu}
            role="listbox"
            aria-label={`${agentName} commands`}
            data-testid="pi-native-command-menu"
          >
            {suggestions.map((suggestion, index) => (
              <button
                type="button"
                role="option"
                id={`pi-native-command-${suggestion.id}`}
                aria-selected={index === selectedSuggestionIndex}
                className={classNames(
                  piNativeStyles.commandOption,
                  index === selectedSuggestionIndex && piNativeStyles.commandOptionSelected,
                )}
                key={suggestion.id}
                disabled={readOnly}
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => onSelectSuggestion(index)}
              >
                <span className={piNativeStyles.commandLabel}>{suggestion.label}</span>
                <span className={piNativeStyles.commandDescription}>{suggestion.description}</span>
                <span className={classNames(
                  piNativeStyles.commandSource,
                  piNativeCommandSourceStyles[suggestion.source],
                )}>
                  {piSlashSourceLabel(suggestion.source)}
                </span>
              </button>
            ))}
          </div>
        )}

        {attachments.length > 0 && (
          <ul className={piNativeStyles.attachments} aria-label="Images attached to this prompt">
            {attachments.map((image) => (
              <li className={piNativeStyles.attachment} key={image.id}>
                <img src={image.previewUrl} alt="" />
                <span>
                  <strong title={image.file.name}>{image.file.name || 'Pasted image'}</strong>
                  <small>{formatImageSize(image.file.size)}</small>
                </span>
                <button
                  type="button"
                  disabled={readOnly || isUploadingImages}
                  aria-label={`Remove ${image.file.name || 'pasted image'}`}
                  onClick={() => onRemoveAttachment(image.id)}
                >
                  <X size={12} />
                </button>
              </li>
            ))}
          </ul>
        )}

        <textarea
          ref={textareaRef}
          value={draft}
          rows={2}
          aria-label={`Message ${agentName}`}
          aria-autocomplete={readOnly ? undefined : 'list'}
          aria-controls={!readOnly && suggestions.length > 0 ? 'pi-native-command-menu' : undefined}
          aria-expanded={!readOnly && suggestions.length > 0}
          aria-activedescendant={!readOnly && selectedSuggestion
            ? `pi-native-command-${selectedSuggestion.id}`
            : undefined}
          data-testid="pi-native-composer"
          data-read-only={readOnly || undefined}
          className={piNativeStyles.textarea}
          placeholder={readOnly
            ? 'This subagent is managed by its parent thread.'
            : `Ask ${agentName} to inspect the repo, paste an image, or continue this thread…`}
          readOnly={readOnly}
          disabled={isUploadingImages}
          onChange={readOnly ? undefined : (event) => onDraftChange(event.target.value)}
          onPaste={readOnly ? undefined : onPaste}
          onDragOver={readOnly ? undefined : (event) => event.preventDefault()}
          onDrop={readOnly ? undefined : onDrop}
          onKeyDown={readOnly ? undefined : onKeyDown}
        />

        <div className={piNativeStyles.composerFooter}>
          <AgentModelControls
            variant="inline"
            className={piNativeStyles.composerSettings}
            model={model}
            modelOptions={modelOptions}
            modelDisabled={readOnly || modelDisabled}
            onModelChange={onModelChange}
            thinking={thinking}
            thinkingOptions={thinkingOptions}
            thinkingDisabled={readOnly || thinkingDisabled}
            onThinkingChange={onThinkingChange}
          />
          <label
            className={piNativeStyles.attach}
            title={readOnly ? 'Subagent prompts are managed by the parent thread' : 'Attach images'}
            aria-label="Attach images"
          >
            <ImagePlus size={13} />
            <input
              type="file"
              aria-label="Attach images"
              accept={PI_IMAGE_ACCEPT}
              multiple
              disabled={readOnly || isUploadingImages}
              onChange={onImageInput}
            />
          </label>
          <span className={piNativeStyles.composerHint}>{hint}</span>
          <button
            type="button"
            className={classNames(
              piNativeStyles.primary,
              primaryActionIsStop ? piNativeStyles.primaryStop : piNativeStyles.primarySend,
            )}
            aria-label={isUploadingImages
              ? 'Uploading images'
              : primaryActionIsStop
                ? `Stop ${agentName}`
                : 'Send message'}
            data-testid="pi-native-send"
            title={readOnly ? 'Subagent controls are managed by the parent thread' : undefined}
            disabled={readOnly || isUploadingImages || (!primaryActionIsStop && !canSend)}
            onClick={onPrimaryAction}
          >
            {isUploadingImages
              ? <LoaderCircle size={13} className={piNativeStyles.spin} />
              : primaryActionIsStop
                ? <Square size={12} fill="currentColor" />
                : <ArrowUp size={15} />}
          </button>
        </div>
      </div>
    </footer>
  )
}

export function piSlashSourceLabel(source: PiNativeComposerSuggestion['source']): string {
  switch (source) {
    case 'native': return 'Native'
    case 'extension': return 'Extension'
    case 'prompt': return 'Prompt'
    case 'skill': return 'Skill'
    case 'model': return 'Model'
    case 'level': return 'Level'
  }
}
