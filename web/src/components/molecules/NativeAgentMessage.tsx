import { AgentMarkdown } from './AgentMarkdown'

type NativeAgentMessageProps = {
  role: 'user' | 'assistant'
  text: string
  images?: Array<{ mimeType: string; data: string }>
}

const styles = {
  user: 'mb-[13px] mt-2.5 flex justify-end text-[13px] leading-[1.72]',
  bubble: 'max-w-[min(76%,680px)] rounded-[16px_16px_4px_16px] border border-ghost-border/80 bg-[color-mix(in_srgb,var(--theme-color-raised)_92%,var(--theme-color-background))] px-3.5 py-[11px] shadow-[0_8px_24px_color-mix(in_srgb,var(--theme-color-canvas)_16%,transparent)] max-[820px]:max-w-[88%]',
  images: 'mb-[9px] grid max-w-[520px] grid-cols-2 gap-[7px] last:mb-0 [&_img]:max-h-[280px] [&_img]:w-full [&_img]:rounded-[9px] [&_img]:border [&_img]:border-ghost-border/75 [&_img]:bg-ghost-black/40 [&_img]:object-contain [&_img:only-child]:col-span-full',
  assistant: 'mb-[23px] max-w-[810px] text-[13px] leading-[1.72] text-ghost-bright-white/95',
} as const

export function NativeAgentMessage({
  role,
  text,
  images = [],
}: NativeAgentMessageProps) {
  if (role === 'user') {
    return (
      <article className={styles.user}>
        <div className={styles.bubble}>
          {images.length > 0 && (
            <div className={styles.images}>
              {images.map((image, index) => (
                <img
                  src={`data:${image.mimeType};base64,${image.data}`}
                  alt={`Attached image ${index + 1}`}
                  key={`${image.mimeType}:${index}`}
                />
              ))}
            </div>
          )}
          {text.trim() && <AgentMarkdown text={text} />}
        </div>
      </article>
    )
  }

  return (
    <article className={styles.assistant}>
      <AgentMarkdown text={text} />
    </article>
  )
}
