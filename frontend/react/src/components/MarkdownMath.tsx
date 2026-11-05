import { useMemo } from 'react'
import DOMPurify from 'dompurify'
import katex from 'katex'
import { marked } from 'marked'
import 'katex/dist/katex.min.css'

marked.setOptions({ breaks: true, gfm: true })

function escapeHtml(s: string) {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

/**
 * Markdown + LaTeX ($$…$$ display, $…$ inline) → sanitized HTML.
 * Math is lifted out before markdown parsing so underscores/asterisks inside
 * formulas don't get eaten, then rendered with KaTeX and spliced back.
 */
export function renderMarkdownMath(text: string): string {
  if (!text) return ''
  const blocks: { math: string; display: boolean }[] = []
  let processed = text.replace(/\$\$([\s\S]+?)\$\$/g, (_, m: string) => {
    blocks.push({ math: m.trim(), display: true })
    return `XMATHX${blocks.length - 1}XENDX`
  })
  processed = processed.replace(/\$([^$\n]+?)\$/g, (_, m: string) => {
    blocks.push({ math: m.trim(), display: false })
    return `XMATHX${blocks.length - 1}XENDX`
  })
  let html = marked.parse(processed) as string
  html = html.replace(/XMATHX(\d+)XENDX/g, (_, id: string) => {
    const b = blocks[Number(id)]
    try {
      return katex.renderToString(b.math, { displayMode: b.display, throwOnError: false })
    } catch {
      return escapeHtml(b.math)
    }
  })
  return DOMPurify.sanitize(html)
}

export function MarkdownMath({ text, streaming }: { text: string; streaming?: boolean }) {
  const html = useMemo(
    () => (streaming ? escapeHtml(text).replace(/\n/g, '<br>') : renderMarkdownMath(text)),
    [text, streaming],
  )
  return <div className="prose-chat" dangerouslySetInnerHTML={{ __html: html }} />
}
