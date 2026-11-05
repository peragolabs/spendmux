import { type ReactNode } from 'react'
import { cn } from '@/lib/utils'

type Block =
  | { type: 'code'; lang: string; body: string }
  | { type: 'text'; body: string }

function splitBlocks(content: string): Block[] {
  const blocks: Block[] = []
  const parts = content.split(/(```[\s\S]*?```)/g)

  for (const part of parts) {
    if (!part) continue
    const fence = part.match(/^```(\w*)\n?([\s\S]*?)```$/)
    if (fence) {
      blocks.push({ type: 'code', lang: fence[1] || '', body: fence[2].replace(/\n$/, '') })
    } else {
      blocks.push({ type: 'text', body: part })
    }
  }
  return blocks
}

function formatInline(text: string, keyPrefix: string): ReactNode[] {
  const nodes: ReactNode[] = []
  let rest = text
  let i = 0

  const tryMatch = (re: RegExp) => {
    const m = rest.match(re)
    if (!m || m.index === undefined) return null
    return { m, index: m.index }
  }

  while (rest.length > 0) {
    const candidates = [
      { re: /`([^`]+)`/, kind: 'code' as const },
      { re: /\*\*([^*]+)\*\*/, kind: 'bold' as const },
      { re: /\*([^*]+)\*/, kind: 'italic' as const },
      { re: /\[([^\]]+)\]\(([^)]+)\)/, kind: 'link' as const },
    ]

    let best: { index: number; m: RegExpMatchArray; kind: string } | null = null
    for (const c of candidates) {
      const hit = tryMatch(c.re)
      if (hit && (!best || hit.index < best.index)) {
        best = { index: hit.index, m: hit.m, kind: c.kind }
      }
    }

    if (!best) {
      nodes.push(rest)
      break
    }

    if (best.index > 0) nodes.push(rest.slice(0, best.index))

    const key = `${keyPrefix}-${i++}`
    switch (best.kind) {
      case 'code':
        nodes.push(
          <code key={key} className="rounded bg-muted px-1.5 py-0.5 text-[0.85em] text-foreground">
            {best.m[1]}
          </code>,
        )
        break
      case 'bold':
        nodes.push(<strong key={key} className="font-semibold">{best.m[1]}</strong>)
        break
      case 'italic':
        nodes.push(<em key={key}>{best.m[1]}</em>)
        break
      case 'link':
        nodes.push(
          <a key={key} href={best.m[2]} target="_blank" rel="noopener noreferrer" className="text-primary underline underline-offset-2">
            {best.m[1]}
          </a>,
        )
        break
    }

    rest = rest.slice(best.index + best.m[0].length)
  }

  return nodes
}

function renderTextBlock(body: string, blockKey: number): ReactNode[] {
  const trimmed = body.trim()
  if (!trimmed) return []

  const nodes: ReactNode[] = []
  const paragraphs = trimmed.split(/\n{2,}/)

  paragraphs.forEach((para, pi) => {
    const lines = para.split('\n')
    const listMatch = lines.every(l => /^[-*]\s+/.test(l.trim()))
    const orderedMatch = lines.every(l => /^\d+\.\s+/.test(l.trim()))

    if (listMatch) {
      nodes.push(
        <ul key={`${blockKey}-ul-${pi}`} className="my-2 list-disc space-y-1 pl-5">
          {lines.map((line, li) => (
            <li key={li}>{formatInline(line.trim().replace(/^[-*]\s+/, ''), `${blockKey}-${pi}-${li}`)}</li>
          ))}
        </ul>,
      )
      return
    }

    if (orderedMatch) {
      nodes.push(
        <ol key={`${blockKey}-ol-${pi}`} className="my-2 list-decimal space-y-1 pl-5">
          {lines.map((line, li) => (
            <li key={li}>{formatInline(line.trim().replace(/^\d+\.\s+/, ''), `${blockKey}-${pi}-${li}`)}</li>
          ))}
        </ol>,
      )
      return
    }

    lines.forEach((line, li) => {
      const t = line.trim()
      if (!t) return
      const heading = t.match(/^(#{1,3})\s+(.+)$/)
      if (heading) {
        const level = heading[1].length
        const Tag = (`h${level}` as 'h1' | 'h2' | 'h3')
        nodes.push(
          <Tag key={`${blockKey}-h-${pi}-${li}`} className="mt-3 mb-1 font-display text-lg">
            {formatInline(heading[2], `${blockKey}-h-${pi}-${li}`)}
          </Tag>,
        )
        return
      }
      nodes.push(
        <p key={`${blockKey}-p-${pi}-${li}`} className="my-1 leading-relaxed">
          {formatInline(t, `${blockKey}-p-${pi}-${li}`)}
        </p>,
      )
    })
  })

  return nodes
}

type Props = {
  content: string
  streaming?: boolean
  className?: string
}

export function ChatMessageContent({ content, streaming, className }: Props) {
  if (!content.trim()) {
    if (streaming) {
      return (
        <div className={cn('flex items-center gap-1 py-1', className)} aria-label="Assistant is typing">
          <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-muted-foreground [animation-delay:0ms]" />
          <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-muted-foreground [animation-delay:150ms]" />
          <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-muted-foreground [animation-delay:300ms]" />
        </div>
      )
    }
    return null
  }

  const blocks = splitBlocks(content)

  return (
    <div className={cn('space-y-1 text-sm', className)}>
      {blocks.map((block, i) => {
        if (block.type === 'code') {
          return (
            <div key={i} className="my-2 overflow-hidden rounded-lg border border-border bg-foreground/95 text-background">
              {block.lang && (
                <div className="border-b border-border/30 px-3 py-1 font-mono text-[0.65rem] uppercase tracking-[0.15em] opacity-60">
                  {block.lang}
                </div>
              )}
              <pre className="overflow-x-auto p-3 text-xs leading-relaxed">
                <code>{block.body}</code>
              </pre>
            </div>
          )
        }
        return <div key={i}>{renderTextBlock(block.body, i)}</div>
      })}
      {streaming && (
        <span className="ml-0.5 inline-block h-4 w-[2px] translate-y-[3px] animate-pulse bg-primary" aria-hidden />
      )}
    </div>
  )
}
