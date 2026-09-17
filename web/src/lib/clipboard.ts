function copyTextWithLegacyApi(text: string) {
  const previousFocus = document.activeElement instanceof HTMLElement
    ? document.activeElement
    : null
  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.readOnly = true
  textarea.style.cssText = 'position:fixed;left:-9999px;top:0;opacity:0;pointer-events:none'
  document.body.append(textarea)

  try {
    textarea.focus({ preventScroll: true })
    textarea.select()
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    textarea.remove()
    previousFocus?.focus({ preventScroll: true })
  }
}

export async function writeSystemClipboard(text: string) {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return
    }
  } catch {
    // Plain HTTP origins and restrictive browser policies can reject the
    // asynchronous API. The legacy path still works during a mouse gesture.
  }

  if (!copyTextWithLegacyApi(text)) {
    throw new Error('The browser denied clipboard access.')
  }
}
