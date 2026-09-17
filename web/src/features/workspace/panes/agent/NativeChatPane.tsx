import { isSupportedPiImageType, promptWithFiles } from '@/lib/promptImages'
import { memo, useCallback, useEffect, useReducer, useRef, useState } from 'react'
import {
  ArrowDown,
  ArrowUp,
  Check,
  ChevronRight,
  ImagePlus,
  LoaderCircle,
  Square,
  X,
} from 'lucide-react'
import { uploadPiImage, uploadFile } from '@/api'
import { apiWebSocketUrl } from '@/apiUrl'
import { fallbackCodingAgentConfigs, thinkingChoicesForModel } from '@/codingAgents'
import { AgentMarkdown } from '@/ui/markdown'
import { classNames } from '@/lib/classNames'
import { useImageAttachments } from '@/lib/useImageAttachments'
import {
  filesFromClipboard,
  promptFilePolicy,
} from '@/lib/promptImages'
import type { CodingAgentConfig, ConnectionStatus } from '@/types'
import { CodingAgentsTopic } from '@/wire/topics'
import { useSubscription } from '@/wire/react'
import {
  emptyChat,
  nativeChatProviders,
  reduceChat,
  type ChatEvent,
  type ChatItem,
  type ChatRequest,
} from './nativeChat'

type Props = {
  provider: keyof typeof nativeChatProviders
  projectId: string
  threadId: string
  threadTitle: string
  active: boolean
  initialModel?: string
  initialThinkingLevel?: string
  initialPrompt?: string
  initialImagePaths?: string[]
  onInitialPromptSent?: () => void
  onStatusChange: (status: ConnectionStatus) => void
}
const control =
  'rounded-lg px-2 py-1.5 text-xs text-ghost-muted hover:bg-ghost-raised hover:text-ghost-white disabled:opacity-40'

const ChatEntry = memo(function ChatEntry({ item }: { item: ChatItem }) {
  if (item.kind === 'user')
    return (
      <article className="ml-auto max-w-[88%] whitespace-pre-wrap rounded-2xl border border-ghost-border/60 bg-ghost-raised px-4 py-3 text-sm leading-6">
        {item.text}
      </article>
    )
  if (item.kind === 'assistant')
    return (
      <article className="min-w-0 text-sm leading-7">
        <AgentMarkdown text={item.text} />
      </article>
    )
  return (
    <details className="group rounded-lg text-xs text-ghost-muted">
      <summary className="flex cursor-pointer list-none items-center gap-2 py-2 hover:text-ghost-white">
        <ChevronRight size={13} className="shrink-0 transition-transform group-open:rotate-90" />
        {item.status === 'inProgress' ? (
          <LoaderCircle size={13} className="shrink-0 animate-spin" />
        ) : (
          <Check size={13} className="shrink-0 text-ghost-dim" />
        )}
        <span className="truncate font-mono">{item.title || 'Tool activity'}</span>
        {item.status === 'failed' && <span className="text-ghost-red">Failed</span>}
      </summary>
      <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-ghost-border bg-ghost-panel p-3 text-[11px] leading-5">
        {item.text || 'Waiting for output…'}
      </pre>
    </details>
  )
})

function PendingRequest({
  request,
  disabled,
  send,
}: {
  request: ChatRequest
  disabled: boolean
  send: (command: object) => boolean
}) {
  const [answers, setAnswers] = useState<Record<string, string>>({})
  const [submitted, setSubmitted] = useState(false)
  function respond(value: object) {
    if (send({ type: 'respond', requestId: request.id, ...value })) setSubmitted(true)
  }
  return (
    <form
      className="border-b border-ghost-border p-4"
      onSubmit={(event) => {
        event.preventDefault()
        respond({
          answers: Object.fromEntries(
            (request.questions ?? []).map((q) => [q.id, { answers: [answers[q.id] ?? ''] }]),
          ),
        })
      }}
    >
      <h3 className="text-sm font-medium">{request.title}</h3>
      {request.details && (
        <pre className="mt-2 max-h-36 overflow-auto whitespace-pre-wrap break-words text-xs text-ghost-muted">
          {request.details}
        </pre>
      )}
      {request.kind === 'approval' ? (
        <div className="mt-3 flex justify-end gap-2">
          <button
            type="button"
            className={control}
            disabled={disabled || submitted}
            onClick={() => respond({ decision: 'decline' })}
          >
            Decline
          </button>
          <button
            type="button"
            className={`${control} bg-ghost-green/15 text-ghost-green`}
            disabled={disabled || submitted}
            onClick={() => respond({ decision: 'accept' })}
          >
            Allow once
          </button>
        </div>
      ) : (
        <>
          {request.questions?.map((question) => (
            <fieldset key={question.id} className="mt-3 min-w-0">
              <legend className="mb-2 text-xs">{question.question}</legend>
              {question.options?.map((option) => (
                <label
                  key={option.label}
                  className="mb-1 flex cursor-pointer items-start gap-2 rounded-lg p-2 text-xs hover:bg-ghost-raised"
                >
                  <input
                    type="radio"
                    name={question.id}
                    value={option.label}
                    checked={answers[question.id] === option.label}
                    onChange={() => setAnswers((old) => ({ ...old, [question.id]: option.label }))}
                  />
                  <span>
                    {option.label}
                    <span className="mt-1 block text-ghost-dim">{option.description}</span>
                  </span>
                </label>
              ))}
              <input
                aria-label={question.header || question.question}
                type={question.isSecret ? 'password' : 'text'}
                className="mt-1 w-full rounded-lg border border-ghost-border bg-transparent p-2 text-xs"
                placeholder="Your answer…"
                value={answers[question.id] ?? ''}
                onChange={(event) =>
                  setAnswers((old) => ({ ...old, [question.id]: event.target.value }))
                }
              />
            </fieldset>
          ))}
          <div className="mt-2 text-right">
            <button
              className={control}
              disabled={
                disabled || submitted || request.questions?.some((q) => !answers[q.id]?.trim())
              }
            >
              Submit answers
            </button>
          </div>
        </>
      )}
    </form>
  )
}

export function NativeChatPane(props: Props) {
  const { projectId, threadId, provider, active } = props
  const agentName = nativeChatProviders[provider].name
  const [state, dispatch] = useReducer(reduceChat, emptyChat)
  const [status, setStatus] = useState<ConnectionStatus>('connecting')
  const [attempt, setAttempt] = useState(0)
  const [model, setModel] = useState(props.initialModel ?? '')
  const [effort, setEffort] = useState(props.initialThinkingLevel ?? '')
  const draftKey = `kiwi-code:native-chat-draft:${provider}:${projectId}:${threadId}`
  const [draft, setDraft] = useState(() => {
    try {
      return localStorage.getItem(draftKey) ?? ''
    } catch {
      return ''
    }
  })
  const [uploading, setUploading] = useState(false)
  const [jump, setJump] = useState(false)
  const socket = useRef<WebSocket | null>(null)
  const callbacks = useRef(props)
  callbacks.current = props
  const initialSent = useRef(false)
  const initialPending = useRef(false)
  const initialImages = useRef(props.initialImagePaths ?? [])
  const ready = useRef(false)
  const submitting = useRef(false)
  const pendingDraft = useRef<string | null>(null)
  const draftRef = useRef(draft)
  draftRef.current = draft
  const clearImagesRef = useRef<() => void>(() => {})
  const textarea = useRef<HTMLTextAreaElement>(null)
  const timeline = useRef<HTMLDivElement>(null)
  const atBottom = useRef(true)
  const abortUpload = useRef<AbortController | null>(null)
  const images = useImageAttachments()
  clearImagesRef.current = images.clearAttachments
  const configSubscription = useSubscription(CodingAgentsTopic, { projectId })
  const configs =
    configSubscription.state === 'ready'
      ? (configSubscription.data as CodingAgentConfig[])
      : fallbackCodingAgentConfigs
  const config =
    configs.find((candidate) => candidate.id === provider) ??
    fallbackCodingAgentConfigs.find((candidate) => candidate.id === provider)!
  const selectedModel = config.models.find((candidate) => candidate.id === model)
  const efforts = thinkingChoicesForModel(selectedModel?.reasoningLevels, config.thinkingLevels)
  const error = useCallback((message: string) => dispatch({ type: 'chat_error', message }), [])
  const send = useCallback(
    (command: object) => {
      if (!ready.current || socket.current?.readyState !== WebSocket.OPEN) {
        error('The conversation is disconnected. Reconnect to continue.')
        return false
      }
      socket.current.send(JSON.stringify(command))
      return true
    },
    [error],
  )

  useEffect(() => {
    let disposed = false
    ready.current = false
    setStatus('connecting')
    callbacks.current.onStatusChange('connecting')
    const url = apiWebSocketUrl(
      `/api/projects/${encodeURIComponent(projectId)}/threads/${encodeURIComponent(threadId)}/${provider}/native`,
    )
    const connection = new WebSocket(url)
    socket.current = connection
    connection.onmessage = (event) => {
      if (disposed) return
      try {
        const update = JSON.parse(event.data) as ChatEvent
        dispatch(update)
        if (update.type === 'chat_sent') {
          initialPending.current = false
          initialImages.current = []
          if (pendingDraft.current !== null && draftRef.current === pendingDraft.current) {
            setDraft('')
            clearImagesRef.current()
          }
          pendingDraft.current = null
          submitting.current = false
          if (initialSent.current) callbacks.current.onInitialPromptSent?.()
        }
        if (update.type === 'chat_error') {
          submitting.current = false
          pendingDraft.current = null
          if (initialPending.current) {
            setDraft(callbacks.current.initialPrompt ?? '')
            initialPending.current = false
          }
        }
        if (update.type === 'chat_snapshot') {
          ready.current = true
          if (update.value.model && !callbacks.current.initialModel) setModel(update.value.model)
          if (update.value.effort && !callbacks.current.initialThinkingLevel)
            setEffort(update.value.effort)
          setStatus('open')
          callbacks.current.onStatusChange('open')
          const initial = callbacks.current
          if (
            !initialSent.current &&
            (initial.initialPrompt || initial.initialImagePaths?.length)
          ) {
            initialSent.current = true
            initialPending.current = !update.value.items?.length
            // A previous connection may have delivered the initial turn before
            // its acknowledgement was lost. The resumed history wins.
            if (!update.value.items?.length)
              connection.send(
                JSON.stringify({
                  type: 'prompt',
                  message: initial.initialPrompt ?? '',
                  images: initial.initialImagePaths?.map((path) => ({ path })),
                  model: initial.initialModel,
                  effort: initial.initialThinkingLevel,
                }),
              )
            if (update.value.items?.length) {
              initialImages.current = []
              initial.onInitialPromptSent?.()
            }
          }
        }
      } catch {
        error('Could not read a chat update.')
      }
    }
    connection.onerror = () => {
      if (!disposed) error(`Could not connect to ${agentName}.`)
    }
    connection.onclose = () => {
      if (disposed) return
      ready.current = false
      submitting.current = false
      pendingDraft.current = null
      setStatus('closed')
      callbacks.current.onStatusChange('closed')
    }
    return () => {
      disposed = true
      ready.current = false
      connection.close()
      if (socket.current === connection) socket.current = null
    }
  }, [projectId, threadId, provider, agentName, attempt, error])
  useEffect(() => () => abortUpload.current?.abort(), [])
  useEffect(() => {
    try {
      if (draft) localStorage.setItem(draftKey, draft)
      else localStorage.removeItem(draftKey)
    } catch {
      /* Storage can be disabled. */
    }
  }, [draft, draftKey])
  useEffect(() => {
    if (!atBottom.current) return
    const frame = requestAnimationFrame(() => {
      if (timeline.current) timeline.current.scrollTop = timeline.current.scrollHeight
    })
    return () => cancelAnimationFrame(frame)
  }, [state.items, state.working])
  useEffect(() => {
    if (active) textarea.current?.focus()
  }, [active])
  useEffect(() => {
    if (textarea.current) {
      textarea.current.style.height = 'auto'
      textarea.current.style.height = `${Math.min(220, Math.max(72, textarea.current.scrollHeight))}px`
    }
  }, [draft])

  async function submit() {
    if (submitting.current || (!draft.trim() && !images.attachments.length)) return
    submitting.current = true
    const controller = new AbortController()
    abortUpload.current = controller
    setUploading(true)
    try {
      const uploaded = await Promise.all(
        images.attachments.map((image) => (isSupportedPiImageType(image.file.type) ? uploadPiImage : uploadFile)(projectId, image.file, controller.signal)),
      )
      if (controller.signal.aborted) return
      pendingDraft.current = draft
      if (
        send({
          type: 'prompt',
          message: promptWithFiles(draft.trim(), uploaded.filter((_, i) => !isSupportedPiImageType(images.attachments[i].file.type)).map(({ path }) => path)),
          images: [
            ...initialImages.current.map((path) => ({ path })),
            ...uploaded.filter((_, i) => isSupportedPiImageType(images.attachments[i].file.type)).map(({ path }) => ({ path })),
          ],
          model,
          effort,
        })
      ) {
        atBottom.current = true
      } else {
        submitting.current = false
        pendingDraft.current = null
      }
    } catch (reason) {
      submitting.current = false
      pendingDraft.current = null
      if (!controller.signal.aborted)
        error(reason instanceof Error ? reason.message : 'Could not upload files.')
    } finally {
      if (!controller.signal.aborted) setUploading(false)
    }
  }
  function addImages(files: File[]) {
    const problem = images.addFiles(files, promptFilePolicy)
    if (problem) error(problem)
  }
  const connected = status === 'open'
  return (
    <section
      role="tabpanel"
      aria-label={`${props.threadTitle} native ${agentName} conversation`}
      aria-hidden={!active}
      className={classNames(
        'absolute inset-0 flex min-w-0 flex-col bg-ghost-background text-ghost-white',
        !active && 'hidden',
      )}
    >
      <div
        ref={timeline}
        className="min-h-0 flex-1 overflow-y-auto px-4 sm:px-8"
        onScroll={() => {
          const el = timeline.current!
          atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 64
          setJump(!atBottom.current)
        }}
      >
        <div
          className="mx-auto flex w-full max-w-3xl flex-col gap-5 pb-8 pt-8"
          data-testid="native-chat-timeline"
        >
          {state.items.length === 0 && (
            <div className="flex min-h-[30vh] flex-col items-center justify-center text-center">
              <h2 className="text-xl font-medium tracking-tight">What would you like to build?</h2>
              <p className="mt-2 text-sm text-ghost-dim">Start a conversation with {agentName}.</p>
            </div>
          )}
          {state.items.map((item) => (
            <ChatEntry key={item.id} item={item} />
          ))}
          {state.working && (
            <div role="status" className="flex items-center gap-2 text-xs text-ghost-muted">
              <LoaderCircle size={14} className="animate-spin text-ghost-green" />
              {state.requests.length ? 'Waiting for your response' : 'Working…'}
            </div>
          )}
        </div>
      </div>
      <div className="relative shrink-0 px-3 pb-3 sm:px-6 sm:pb-5">
        {jump && (
          <button
            className="absolute -top-10 left-1/2 flex -translate-x-1/2 items-center gap-1 rounded-full border border-ghost-border bg-ghost-panel px-3 py-1.5 text-xs shadow-lg"
            onClick={() => {
              atBottom.current = true
              setJump(false)
              timeline.current?.scrollTo({ top: timeline.current.scrollHeight, behavior: 'smooth' })
            }}
          >
            <ArrowDown size={13} />
            Latest
          </button>
        )}
        <div className="mx-auto w-full max-w-3xl overflow-hidden rounded-[22px] border border-ghost-border bg-ghost-panel shadow-[0_8px_24px_rgba(0,0,0,0.12)]">
          {state.requests.length > 0 && (
            <div className="max-h-[45dvh] overflow-y-auto">
              {state.requests.map((request) => (
                <PendingRequest
                  key={request.id}
                  request={request}
                  disabled={!connected}
                  send={send}
                />
              ))}
            </div>
          )}
          {(state.error || !connected) && (
            <div
              role="status"
              className="flex items-center justify-between gap-2 border-b border-ghost-border px-4 py-2 text-xs text-ghost-muted"
            >
              <span>
                {state.error ||
                  (status === 'connecting'
                    ? `Connecting to ${agentName}…`
                    : 'Disconnected. Your conversation is saved.')}
              </span>
              {status !== 'connecting' && !connected && (
                <button className={control} onClick={() => setAttempt((value) => value + 1)}>
                  Reconnect
                </button>
              )}
            </div>
          )}
          {!!state.queuedMessages?.length && (
            <div
              aria-label="Queued prompts"
              className="max-h-28 overflow-auto border-b border-ghost-border px-4 py-2 text-xs text-ghost-muted"
            >
              <span className="font-medium text-ghost-green">Queued</span>
              {state.queuedMessages.map((message, index) => (
                <p key={index} className="truncate">{message}</p>
              ))}
              {!state.working && (
                <button
                  className={control}
                  disabled={!connected}
                  onClick={() => send({ type: 'retry_queue' })}
                >
                  Retry queued messages
                </button>
              )}
            </div>
          )}
          {images.attachments.length > 0 && (
            <div className="flex gap-2 overflow-x-auto px-4 pt-3">
              {images.attachments.map((image) => (
                <div className="relative shrink-0" key={image.id}>
                  {isSupportedPiImageType(image.file.type) ? <img
                    className="h-16 w-20 rounded-lg object-cover"
                    src={image.previewUrl}
                    alt={image.file.name}
                  /> : <span className="block max-w-48 truncate p-3 text-xs">{image.file.name}</span>}
                  <button
                    aria-label={`Remove ${image.file.name}`}
                    className="absolute right-0 top-0 rounded-full bg-ghost-panel p-1"
                    onClick={() => images.removeAttachment(image.id)}
                  >
                    <X size={12} />
                  </button>
                </div>
              ))}
            </div>
          )}
          <textarea
            ref={textarea}
            aria-label={`Message ${agentName}`}
            placeholder={`Ask ${agentName} anything…`}
            className="block max-h-[220px] min-h-[72px] w-full resize-none bg-transparent px-4 pb-2 pt-4 text-sm leading-6 outline-none placeholder:text-ghost-dim"
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            onPaste={(event) => addImages(filesFromClipboard(event.clipboardData))}
            onDragOver={(event) => {
              if (event.dataTransfer.types.includes('Files')) event.preventDefault()
            }}
            onDrop={(event) => {
              event.preventDefault()
              addImages(Array.from(event.dataTransfer.files))
            }}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
                event.preventDefault()
                void submit()
              }
            }}
          />
          <div className="flex min-w-0 items-center gap-1 px-3 pb-3">
            <label className={`${control} cursor-pointer`} title="Attach files">
              <ImagePlus size={16} />
              <span className="sr-only">Attach files</span>
              <input
                className="sr-only"
                type="file"
                multiple
                disabled={uploading}
                onChange={(event) => {
                  addImages(Array.from(event.target.files ?? []))
                  event.target.value = ''
                }}
              />
            </label>
            <select
              aria-label={`${agentName} model`}
              className={`${control} min-w-0 max-w-[48%] bg-transparent`}
              value={model}
              disabled={state.working}
              onChange={(event) => {
                setModel(event.target.value)
                setEffort('')
              }}
            >
              {config.models.map((choice) => (
                <option value={choice.id} key={choice.id}>
                  {choice.id ? choice.label : `${agentName} default`}
                </option>
              ))}
              {model && !selectedModel && <option value={model}>{model}</option>}
            </select>
            <select
              aria-label="Reasoning effort"
              className={`${control} min-w-0 max-w-[26%] bg-transparent`}
              value={effort}
              disabled={state.working}
              onChange={(event) => setEffort(event.target.value)}
            >
              {efforts.map((choice) => (
                <option value={choice.id} key={choice.id}>
                  {choice.id ? choice.label : 'Default effort'}
                </option>
              ))}
            </select>
            {state.working && (
              <button
                aria-label="Queue message"
                className={`${control} ml-auto`}
                disabled={!connected || uploading || (!draft.trim() && !images.attachments.length)}
                onClick={() => void submit()}
              >
                <ArrowUp size={18} />
              </button>
            )}
            <button
              aria-label={state.working ? `Stop ${agentName}` : 'Send message'}
              className={classNames(
                'flex size-8 shrink-0 items-center justify-center rounded-full bg-ghost-green text-ghost-black disabled:opacity-30',
                !state.working && 'ml-auto',
              )}
              disabled={
                !connected ||
                uploading ||
                (!state.working && !draft.trim() && !images.attachments.length)
              }
              onClick={() => (state.working ? send({ type: 'abort' }) : void submit())}
            >
              {uploading ? (
                <LoaderCircle size={16} className="animate-spin" />
              ) : state.working ? (
                <Square size={13} fill="currentColor" />
              ) : (
                <ArrowUp size={18} />
              )}
            </button>
          </div>
        </div>
        <p className="mx-auto mt-2 max-w-3xl px-3 text-center text-[10px] text-ghost-dim">
          {state.working
            ? 'Enter to queue · Shift+Enter for a new line'
            : 'Enter to send · Shift+Enter for a new line'}
          {state.usage && (
            <span className="ml-2">· {state.usage.totalTokens.toLocaleString()} tokens</span>
          )}
        </p>
      </div>
    </section>
  )
}
