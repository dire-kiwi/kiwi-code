import { useEffect, useMemo, useState, type ReactNode } from 'react'
import katex from 'katex'
import 'katex/dist/katex.min.css'
import './agent-markdown.css'

const codeBlockStyles = 'mb-[17px] mt-[13px] overflow-auto whitespace-pre rounded-[10px] border border-ghost-border/75 bg-[color-mix(in_srgb,var(--theme-color-canvas)_56%,var(--theme-color-panel))] px-[15px] py-[13px] font-mono text-[11px] leading-[1.65] text-ghost-white [&_code]:border-0 [&_code]:bg-transparent [&_code]:p-0 [&_code]:text-[inherit] [&_code]:text-inherit'

type PendingDisplayMath = {
  closingDelimiter: '\\]' | '$$'
  expressionLines: string[]
  sourceLines: string[]
}

type DisplayMathOpening =
  | { complete: true; expression: string; source: string }
  | { complete: false; pending: PendingDisplayMath }

type MarkdownTableAlignment = 'left' | 'right' | 'center' | 'default'

type ParsedMarkdownTable = {
  header: string[]
  rows: string[][]
  alignments: MarkdownTableAlignment[]
  linesConsumed: number
}

const tableAlignmentStyles: Record<MarkdownTableAlignment, string> = {
  left: 'text-left',
  right: 'text-right',
  center: 'text-center',
  default: 'text-left',
}

export function AgentMarkdown({ text }: { text: string }) {
  const blocks: ReactNode[] = []
  const lines = text.replace(/\r\n/g, '\n').split('\n')
  let paragraph: string[] = []
  let code: string[] | null = null
  let codeLanguage = ''
  let list: { ordered: boolean; items: string[] } | null = null
  let displayMath: PendingDisplayMath | null = null

  const flushParagraph = () => {
    if (paragraph.length === 0) return
    blocks.push(<p key={`p:${blocks.length}`}>{renderInlineMarkdown(paragraph.join('\n'))}</p>)
    paragraph = []
  }
  const flushList = () => {
    if (!list) return
    const Tag = list.ordered ? 'ol' : 'ul'
    blocks.push(
      <Tag key={`list:${blocks.length}`}>
        {list.items.map((item, index) => <li key={`${item}:${index}`}>{renderInlineMarkdown(item)}</li>)}
      </Tag>,
    )
    list = null
  }
  const pushDisplayMath = (expression: string, source: string) => {
    blocks.push(
      <MathExpression
        displayMode
        expression={expression}
        fallback={source}
        key={`math:${blocks.length}`}
      />,
    )
  }

  for (let lineIndex = 0; lineIndex < lines.length; lineIndex += 1) {
    const line = lines[lineIndex]

    if (displayMath) {
      const closingIndex = line.indexOf(displayMath.closingDelimiter)
      const hasCleanClosing = closingIndex >= 0
        && !line.slice(closingIndex + displayMath.closingDelimiter.length).trim()
      if (hasCleanClosing) {
        displayMath.expressionLines.push(line.slice(0, closingIndex))
        displayMath.sourceLines.push(line)
        pushDisplayMath(displayMath.expressionLines.join('\n').trim(), displayMath.sourceLines.join('\n'))
        displayMath = null
      } else {
        displayMath.expressionLines.push(line)
        displayMath.sourceLines.push(line)
      }
      continue
    }

    const fence = line.match(/^```\s*([^\s]*)/)
    if (fence) {
      flushParagraph()
      flushList()
      if (code) {
        if (codeLanguage.toLowerCase() === 'mermaid') {
          blocks.push(<MermaidDiagram code={code.join('\n')} key={`mermaid:${blocks.length}`} />)
        } else {
          blocks.push(
            <pre className={codeBlockStyles} key={`code:${blocks.length}`} data-language={codeLanguage || undefined}>
              <code>{code.join('\n')}</code>
            </pre>,
          )
        }
        code = null
        codeLanguage = ''
      } else {
        code = []
        codeLanguage = fence[1] ?? ''
      }
      continue
    }
    if (code) {
      code.push(line)
      continue
    }

    const mathOpening = displayMathOpening(line)
    if (mathOpening) {
      flushParagraph()
      flushList()
      if (mathOpening.complete) {
        pushDisplayMath(mathOpening.expression, mathOpening.source)
      } else {
        displayMath = mathOpening.pending
      }
      continue
    }

    const table = parseMarkdownTable(lines, lineIndex)
    if (table) {
      flushParagraph()
      flushList()
      blocks.push(
        <MarkdownTable
          key={`table:${blocks.length}`}
          header={table.header}
          rows={table.rows}
          alignments={table.alignments}
        />,
      )
      lineIndex += table.linesConsumed - 1
      continue
    }

    if (!line.trim()) {
      flushParagraph()
      flushList()
      continue
    }
    const heading = line.match(/^(#{1,4})\s+(.+)$/)
    if (heading) {
      flushParagraph()
      flushList()
      const level = Math.min(4, heading[1]?.length ?? 1)
      const content = renderInlineMarkdown(heading[2] ?? '')
      const key = `heading:${blocks.length}`
      if (level === 1) blocks.push(<h1 key={key}>{content}</h1>)
      else if (level === 2) blocks.push(<h2 key={key}>{content}</h2>)
      else if (level === 3) blocks.push(<h3 key={key}>{content}</h3>)
      else blocks.push(<h4 key={key}>{content}</h4>)
      continue
    }
    const unordered = line.match(/^\s*[-*+]\s+(.+)$/)
    const ordered = line.match(/^\s*\d+[.)]\s+(.+)$/)
    if (unordered || ordered) {
      flushParagraph()
      const isOrdered = Boolean(ordered)
      if (list && list.ordered !== isOrdered) flushList()
      list ??= { ordered: isOrdered, items: [] }
      list.items.push((ordered?.[1] ?? unordered?.[1] ?? '').trim())
      continue
    }
    if (line.startsWith('> ')) {
      flushParagraph()
      flushList()
      blocks.push(<blockquote key={`quote:${blocks.length}`}>{renderInlineMarkdown(line.slice(2))}</blockquote>)
      continue
    }
    flushList()
    paragraph.push(line)
  }
  if (displayMath) {
    blocks.push(
      <p key={`math-fallback:${blocks.length}`}>
        {renderInlineMarkdown(displayMath.sourceLines.join('\n'))}
      </p>,
    )
  }
  if (code) {
    blocks.push(<pre className={codeBlockStyles} key={`code:${blocks.length}`}><code>{code.join('\n')}</code></pre>)
  }
  flushParagraph()
  flushList()
  return <div className="agent-markdown">{blocks}</div>
}

function parseMarkdownTable(lines: string[], startIndex: number): ParsedMarkdownTable | null {
  const header = splitMarkdownTableRow(lines[startIndex])
  if (!header || header.length === 0 || startIndex + 1 >= lines.length) return null

  const alignments = parseMarkdownTableAlignment(lines[startIndex + 1], header.length)
  if (!alignments) return null

  const rows: string[][] = []
  let lineIndex = startIndex + 2
  for (; lineIndex < lines.length; lineIndex += 1) {
    const row = splitMarkdownTableRow(lines[lineIndex])
    if (!row || row.length !== header.length) break
    rows.push(row)
  }

  return {
    header,
    rows,
    alignments,
    linesConsumed: lineIndex - startIndex,
  }
}

function splitMarkdownTableRow(line: string): string[] | null {
  const trimmed = line.trim()
  if (!trimmed.includes('|')) return null
  const cells = trimmed
    .replace(/^\|/, '')
    .replace(/\|$/, '')
    .split('|')
    .map((cell) => cell.trim())
  return cells.length > 0 ? cells : null
}

function parseMarkdownTableAlignment(line: string, expectedColumns: number): MarkdownTableAlignment[] | null {
  const columns = splitMarkdownTableRow(line)
  if (!columns || columns.length !== expectedColumns) return null

  const alignments: MarkdownTableAlignment[] = []
  for (const column of columns) {
    if (!/^\s*:?-{3,}:?\s*$/.test(column)) return null
    if (column.startsWith(':') && column.endsWith(':')) alignments.push('center')
    else if (column.startsWith(':')) alignments.push('left')
    else if (column.endsWith(':')) alignments.push('right')
    else alignments.push('default')
  }
  return alignments
}

function MarkdownTable({
  header,
  rows,
  alignments,
}: {
  header: string[]
  rows: string[][]
  alignments: MarkdownTableAlignment[]
}) {
  return (
    <div className="agent-markdown-table-wrap">
      <table className="agent-markdown-table" aria-label="Rendered table">
        <thead>
          <tr>
            {header.map((headerCell, index) => (
              <th key={`${headerCell}:${index}`} className={tableAlignmentStyles[alignments[index] ?? 'default']}>
                {renderInlineMarkdown(headerCell)}
              </th>
            ))}
          </tr>
        </thead>
        {rows.length > 0 && (
          <tbody>
            {rows.map((row, rowIndex) => (
              <tr key={`row:${rowIndex}`}>
                {row.map((cell, cellIndex) => (
                  <td key={`${cell}:${cellIndex}`} className={tableAlignmentStyles[alignments[cellIndex] ?? 'default']}>
                    {renderInlineMarkdown(cell)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        )}
      </table>
    </div>
  )
}

function displayMathOpening(line: string): DisplayMathOpening | null {
  const source = line.trim()
  const openingDelimiter = source.startsWith('\\[')
    ? '\\['
    : source.startsWith('$$')
      ? '$$'
      : null
  if (!openingDelimiter) return null

  const closingDelimiter = openingDelimiter === '\\[' ? '\\]' : '$$'
  const remainder = source.slice(openingDelimiter.length)
  const closingIndex = remainder.indexOf(closingDelimiter)
  if (closingIndex >= 0) {
    if (remainder.slice(closingIndex + closingDelimiter.length).trim()) return null
    return {
      complete: true,
      expression: remainder.slice(0, closingIndex).trim(),
      source,
    }
  }

  return {
    complete: false,
    pending: {
      closingDelimiter,
      expressionLines: remainder ? [remainder] : [],
      sourceLines: [line],
    },
  }
}

function renderInlineMarkdown(text: string): ReactNode[] {
  const parts = text.split(/(`[^`\n]+`|\\\((?:(?!\\\))[\s\S])*?\\\)|(?<![\\$])\$(?![$\s])(?:\\.|[^$\n])*?(?<![\\\s])\$(?!\$)|\*\*[^*\n]+\*\*|\[[^\]\n]+\]\([^\s)]+\))/g)
  return parts.filter(Boolean).map((part, index) => {
    if (part.startsWith('`') && part.endsWith('`')) {
      return <code key={`${part}:${index}`}>{part.slice(1, -1)}</code>
    }
    if (part.startsWith('\\(') && part.endsWith('\\)')) {
      return (
        <MathExpression
          expression={part.slice(2, -2).trim()}
          fallback={part}
          key={`${part}:${index}`}
        />
      )
    }
    if (part.startsWith('$') && part.endsWith('$')) {
      return (
        <MathExpression
          expression={part.slice(1, -1).trim()}
          fallback={part}
          key={`${part}:${index}`}
        />
      )
    }
    if (part.startsWith('**') && part.endsWith('**')) {
      return <strong key={`${part}:${index}`}>{part.slice(2, -2)}</strong>
    }
    const link = part.match(/^\[([^\]]+)\]\((https?:\/\/[^)]+)\)$/)
    if (link) {
      return <a href={link[2]} key={`${part}:${index}`} rel="noreferrer" target="_blank">{link[1]}</a>
    }
    return <span key={`${part}:${index}`}>{part}</span>
  })
}

function MathExpression({
  displayMode = false,
  expression,
  fallback,
}: {
  displayMode?: boolean
  expression: string
  fallback: string
}) {
  const html = useMemo(() => {
    if (!expression) return null
    try {
      // KaTeX escapes TeX input, while trust=false disables commands that could emit arbitrary HTML or URLs.
      return katex.renderToString(expression, {
        displayMode,
        output: 'htmlAndMathml',
        strict: false,
        throwOnError: true,
        trust: false,
      })
    } catch {
      return null
    }
  }, [displayMode, expression])
  const Tag = displayMode ? 'div' : 'span'
  const className = `agent-markdown-math agent-markdown-math--${displayMode ? 'display' : 'inline'}`

  if (!html) {
    return <Tag className={`${className} agent-markdown-math--fallback`}>{fallback}</Tag>
  }
  return <Tag className={className} dangerouslySetInnerHTML={{ __html: html }} />
}

let mermaidRenderSequence = 0

function mermaidThemeVariables() {
  const styles = getComputedStyle(document.documentElement)
  const themeColor = (name: string, fallback: string) =>
    styles.getPropertyValue(name).trim() || fallback
  return {
    darkMode: true,
    background: themeColor('--theme-color-panel', '#24272e'),
    fontFamily: styles.getPropertyValue('--theme-font-family').trim() || 'monospace',
    fontSize: '12px',
    primaryColor: themeColor('--theme-color-raised', '#30343d'),
    primaryTextColor: themeColor('--theme-color-bright-white', '#eaeaea'),
    primaryBorderColor: themeColor('--theme-color-border', '#454b57'),
    secondaryColor: themeColor('--theme-color-selected', '#30343d'),
    secondaryTextColor: themeColor('--theme-color-white', '#c5c8c6'),
    secondaryBorderColor: themeColor('--theme-color-border', '#454b57'),
    tertiaryColor: themeColor('--theme-color-canvas', '#1d1f21'),
    tertiaryTextColor: themeColor('--theme-color-muted', '#a5a8a8'),
    tertiaryBorderColor: themeColor('--theme-color-border', '#454b57'),
    lineColor: themeColor('--theme-color-dim', '#81858a'),
    textColor: themeColor('--theme-color-white', '#c5c8c6'),
    edgeLabelBackground: themeColor('--theme-color-panel', '#24272e'),
    noteBkgColor: themeColor('--theme-color-raised', '#30343d'),
    noteTextColor: themeColor('--theme-color-bright-white', '#eaeaea'),
    noteBorderColor: themeColor('--theme-color-border', '#454b57'),
  }
}

function MermaidDiagram({ code }: { code: string }) {
  const [svg, setSvg] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    setSvg(null)
    const source = code.trim()
    if (!source) return
    const renderId = `agent-markdown-mermaid-${mermaidRenderSequence += 1}`
    void import('mermaid')
      .then(async ({ default: mermaid }) => {
        mermaid.initialize({
          securityLevel: 'strict',
          startOnLoad: false,
          suppressErrorRendering: true,
          theme: 'base',
          themeVariables: mermaidThemeVariables(),
        })
        const rendered = await mermaid.render(renderId, source)
        if (!cancelled) setSvg(rendered.svg)
      })
      .catch(() => {
        // Invalid or partially streamed diagrams keep the code-block fallback.
        document.getElementById(renderId)?.remove()
      })
    return () => {
      cancelled = true
    }
  }, [code])

  if (!svg) {
    return (
      <pre className={codeBlockStyles} data-language="mermaid">
        <code>{code}</code>
      </pre>
    )
  }
  return <div className="agent-markdown-mermaid" dangerouslySetInnerHTML={{ __html: svg }} />
}
