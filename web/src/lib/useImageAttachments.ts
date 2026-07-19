import { useCallback, useEffect, useRef, useState } from 'react'
import {
  validateImageAdditions,
  type ImageValidationPolicy,
} from './promptImages'

export type ImageAttachment = {
  id: number
  file: File
  previewUrl: string
}

let nextImageAttachmentId = 0

export function useImageAttachments() {
  const [attachments, setAttachments] = useState<ImageAttachment[]>([])
  const attachmentsRef = useRef<ImageAttachment[]>([])

  const replaceAttachments = useCallback((next: ImageAttachment[]) => {
    attachmentsRef.current = next
    setAttachments(next)
  }, [])

  const addFiles = useCallback((files: readonly File[], policy?: ImageValidationPolicy) => {
    const current = attachmentsRef.current
    const result = validateImageAdditions(
      current.map(({ file }) => file),
      files,
      policy,
    )
    if (result.accepted.length > 0) {
      replaceAttachments([
        ...current,
        ...result.accepted.map((file) => ({
          id: ++nextImageAttachmentId,
          file,
          previewUrl: URL.createObjectURL(file),
        })),
      ])
    }
    return result.error
  }, [replaceAttachments])

  const removeAttachment = useCallback((id: number) => {
    const current = attachmentsRef.current
    const removed = current.find((attachment) => attachment.id === id)
    if (!removed) return
    URL.revokeObjectURL(removed.previewUrl)
    replaceAttachments(current.filter((attachment) => attachment.id !== id))
  }, [replaceAttachments])

  const clearAttachments = useCallback(() => {
    for (const attachment of attachmentsRef.current) {
      URL.revokeObjectURL(attachment.previewUrl)
    }
    replaceAttachments([])
  }, [replaceAttachments])

  useEffect(() => () => {
    for (const attachment of attachmentsRef.current) {
      URL.revokeObjectURL(attachment.previewUrl)
    }
    attachmentsRef.current = []
  }, [])

  return {
    attachments,
    addFiles,
    removeAttachment,
    clearAttachments,
  }
}
