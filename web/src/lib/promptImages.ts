export const PI_IMAGE_MIME_TYPES = [
  'image/png',
  'image/jpeg',
  'image/gif',
  'image/webp',
] as const

export const PI_IMAGE_ACCEPT = PI_IMAGE_MIME_TYPES.join(',')
export const MAX_PI_IMAGE_BYTES = 50 * 1024 * 1024
export const MAX_PI_NATIVE_PROMPT_IMAGES = 20

const supportedImageTypes = new Set<string>(PI_IMAGE_MIME_TYPES)

export function isSupportedPiImageType(value: string): value is typeof PI_IMAGE_MIME_TYPES[number] {
  return supportedImageTypes.has(value)
}

export type ImageValidationPolicy = {
  maxFiles?: number
  maxTotalBytes?: number
}

export type ImageValidationResult = {
  accepted: File[]
  error: string
}

export function imageFilesFromClipboard(data: DataTransfer): File[] {
  const itemImages = Array.from(data.items)
    .filter((item) => item.kind === 'file' && item.type.startsWith('image/'))
    .map((item) => item.getAsFile())
    .filter((image): image is File => image !== null)
  return itemImages.length > 0
    ? itemImages
    : Array.from(data.files).filter((file) => file.type.startsWith('image/'))
}

export function validateImageAdditions(
  existing: readonly File[],
  additions: readonly File[],
  policy: ImageValidationPolicy = {},
): ImageValidationResult {
  const accepted: File[] = []
  let totalBytes = existing.reduce((total, file) => total + file.size, 0)
  let error = ''

  for (const file of additions) {
    if (!isSupportedPiImageType(file.type)) {
      error ||= 'Attach PNG, JPEG, GIF, or WebP images.'
      continue
    }
    if (file.size === 0) {
      error ||= 'Attached images cannot be empty.'
      continue
    }
    if (file.size > MAX_PI_IMAGE_BYTES) {
      error ||= 'Each image must be 50 MB or smaller.'
      continue
    }
    if (policy.maxFiles !== undefined && existing.length + accepted.length >= policy.maxFiles) {
      error ||= `Attach at most ${policy.maxFiles} images to one Pi prompt.`
      continue
    }
    if (policy.maxTotalBytes !== undefined && totalBytes + file.size > policy.maxTotalBytes) {
      error ||= 'Images in one Pi prompt must total 50 MB or smaller.'
      continue
    }
    accepted.push(file)
    totalBytes += file.size
  }

  return { accepted, error }
}

export function formatImageSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${Math.ceil(bytes / 1024)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

export const piNativePromptImagePolicy: ImageValidationPolicy = {
  maxFiles: MAX_PI_NATIVE_PROMPT_IMAGES,
  maxTotalBytes: MAX_PI_IMAGE_BYTES,
}
