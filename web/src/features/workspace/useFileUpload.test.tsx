import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { useFileUpload } from './useFileUpload'
const upload = vi.hoisted(() => vi.fn())
vi.mock('@/api', () => ({ uploadFile: upload }))
const copy = vi.fn()
function Harness() {
  const files = useFileUpload('project', 'thread')
  return <div data-testid="pane" onPasteCapture={files.onPasteCapture} onDropCapture={files.onDropCapture}>{files.control}</div>
}
beforeEach(() => {
  upload.mockReset().mockResolvedValue({ path: '/tmp/upload/my file.txt' })
  copy.mockReset().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: copy } })
})
afterEach(cleanup)
for (const source of ['picker', 'paste', 'drop']) {
  it(`uploads a document from ${source} and copies its quoted path`, async () => {
    render(<Harness />)
    const file = new File(['hello'], 'my file.txt', { type: 'text/plain' })
    if (source === 'picker') fireEvent.change(screen.getByLabelText('Upload files and copy paths'), { target: { files: [file] } })
    if (source === 'paste') fireEvent.paste(screen.getByTestId('pane'), { clipboardData: { items: [], files: [file] } })
    if (source === 'drop') fireEvent.drop(screen.getByTestId('pane'), { dataTransfer: { files: [file] } })
    await waitFor(() => expect(copy).toHaveBeenCalledWith("'/tmp/upload/my file.txt'"))
    expect(upload).toHaveBeenCalledWith('project', file, expect.any(AbortSignal))
  })
}
it('retains uploaded paths when clipboard access fails and allows a retry', async () => {
  copy.mockRejectedValueOnce(new Error('denied'))
  render(<Harness />)
  fireEvent.change(screen.getByLabelText('Upload files and copy paths'), { target: { files: [new File(['x'], 'x.txt')] } })
  await screen.findByText('Files uploaded. Copy paths below.', { selector: 'summary' })
  expect((screen.getByLabelText('Uploaded file paths') as HTMLTextAreaElement).value).toContain('my file.txt')
  fireEvent.click(screen.getByLabelText('Copy uploaded file paths'))
  await screen.findByText('File paths copied', { selector: 'summary' })
  expect(upload).toHaveBeenCalledTimes(1)
})
