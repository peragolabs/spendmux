import { type FormEvent, type KeyboardEvent, useCallback, useEffect, useRef, useState } from 'react'
import { Loader2, SendHorizonal } from 'lucide-react'

import { MarkdownMath } from '@/components/MarkdownMath'
import { ModelPicker } from '@/components/ModelPicker'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { getJSON } from '@/lib/api'
import { cn } from '@/lib/utils'

/* ── GET /api/playground/bootstrap ───────────────────────────────────── */

type ProviderOpt = { id: string; name: string; suggested: string[] }
type Conversation = { ID: string; Title: string }
type Bootstrap = {
  Providers: ProviderOpt[]
  RecentConversations: Conversation[]
  ActiveConversationID: string
  ActiveConversationTitle: string
  InitialMessages: { role: string; content: string; model: string }[] | null
}

type Msg = {
  role: 'user' | 'assistant'
  content: string
  streaming?: boolean
  fallbackTo?: string
  fallbackReason?: string
}

/* ── Toggle pill (same look as the old Save / Don't save switch) ─────── */

function Toggle({ on, onLabel, offLabel, onChange, title }: { on: boolean; onLabel: string; offLabel: string; onChange: (v: boolean) => void; title?: string }) {
  const btn = (active: boolean, label: string, v: boolean) => (
    <button
      type="button"
      onClick={() => onChange(v)}
      className={cn('rounded-full px-3 py-1 text-xs font-semibold transition-colors', active ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:text-foreground')}
    >
      {label}
    </button>
  )
  return (
    <div className="flex items-center gap-0.5 rounded-full border border-border bg-card p-0.5" title={title} role="group">
      {btn(on, onLabel, true)}
      {btn(!on, offLabel, false)}
    </div>
  )
}

/* ── Page ────────────────────────────────────────────────────────────── */

export default function PlaygroundPage() {
  const [boot, setBoot] = useState<Bootstrap | null>(null)
  const [error, setError] = useState('')
  const [provider, setProvider] = useState('')
  const [model, setModel] = useState('')
  const [liveModels, setLiveModels] = useState<string[]>([])
  const [modelsStatus, setModelsStatus] = useState('')
  const [modelsLive, setModelsLive] = useState(false)
  const [messages, setMessages] = useState<Msg[]>([])
  const [prompt, setPrompt] = useState('')
  const [sending, setSending] = useState(false)
  const [save, setSave] = useState(() => localStorage.getItem('spendmux_playground_save') !== '0')
  const [fallback, setFallback] = useState(() => localStorage.getItem('spendmux_playground_fallback') === '1')
  const [conversationId, setConversationId] = useState('')
  const wsRef = useRef<WebSocket | null>(null)
  const bottomRef = useRef<HTMLDivElement | null>(null)

  const loadModels = useCallback(async (p: string) => {
    if (!p) return
    setModelsStatus('Loading models…')
    try {
      const j = await getJSON<{ data: { id: string }[]; meta?: { source?: string } }>('/api/models?provider=' + encodeURIComponent(p))
      const ids = (j.data || []).map(m => m.id)
      setLiveModels(ids)
      // Prefer the provider's live list over the static suggestion once it arrives.
      if (ids.length) setModel(cur => (cur && ids.includes(cur) ? cur : ids[0]))
      setModelsLive(j.meta?.source === 'upstream' && ids.length > 0)
      setModelsStatus(ids.length ? `${ids.length} models${j.meta?.source === 'upstream' ? ' (live from provider)' : ' (suggested)'}` : 'No models — type an id')
    } catch {
      setLiveModels([])
      setModelsLive(false)
      setModelsStatus('Using suggested models')
    }
  }, [])

  useEffect(() => {
    getJSON<Bootstrap>('/api/playground/bootstrap')
      .then(b => {
        setBoot(b)
        const first = b.Providers[0]?.id ?? ''
        setProvider(first)
        setConversationId(b.ActiveConversationID || '')
        setMessages((b.InitialMessages || []).map(m => ({ role: m.role === 'assistant' || m.role === 'ai' ? 'assistant' : 'user', content: m.content })))
        void loadModels(first)
      })
      .catch(e => setError(e instanceof Error ? e.message : 'Could not load the playground'))
  }, [loadModels])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: 'end' })
  }, [messages])

  const currentProvider = boot?.Providers.find(p => p.id === provider)
  const suggestions = liveModels.length ? liveModels : currentProvider?.suggested ?? []

  function setSaveMode(v: boolean) {
    setSave(v)
    localStorage.setItem('spendmux_playground_save', v ? '1' : '0')
    if (!v) {
      setConversationId('')
      window.history.replaceState({}, '', '/chat')
    }
  }
  function setFallbackMode(v: boolean) {
    setFallback(v)
    localStorage.setItem('spendmux_playground_fallback', v ? '1' : '0')
  }

  function send(e?: FormEvent) {
    e?.preventDefault()
    const text = prompt.trim()
    if (!text || sending || !provider || !model.trim()) return
    const history = messages.filter(m => m.content).map(m => ({ role: m.role, content: m.content }))
    setMessages(m => [...m, { role: 'user', content: text }, { role: 'assistant', content: '', streaming: true }])
    setPrompt('')
    setSending(true)

    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    const ws = new WebSocket(`${proto}://${location.host}/ws/chat`)
    wsRef.current = ws
    const patchLast = (fn: (last: Msg) => void) =>
      setMessages(m => {
        const copy = [...m]
        const last = copy[copy.length - 1]
        if (last) fn(last)
        return copy
      })

    ws.onopen = () =>
      ws.send(JSON.stringify({ provider, model: model.trim(), prompt: text, conversation_id: save ? conversationId : '', history, save, fallback }))
    ws.onmessage = ev => {
      const data = String(ev.data)
      try {
        const j = JSON.parse(data)
        if (j.conversation_id) {
          if (save) {
            setConversationId(j.conversation_id)
            window.history.replaceState({}, '', `/chat?c=${j.conversation_id}`)
          }
          return
        }
        if (j.type === 'fallback') return patchLast(l => { l.fallbackTo = j.to; l.fallbackReason = j.reason })
        if (j.type === 'replace') return patchLast(l => { l.content = j.content; l.streaming = false })
        if (j.error) { patchLast(l => { l.content = `Error: ${j.error}`; l.streaming = false }); setSending(false); return }
      } catch { /* plain token */ }
      if (data.startsWith('⚠️')) { patchLast(l => { l.content = data; l.streaming = false }); setSending(false); return }
      patchLast(l => { if (l.streaming) l.content += data })
    }
    ws.onerror = () => { patchLast(l => { l.content = l.content || '⚠️ Connection failed'; l.streaming = false }); setSending(false) }
    ws.onclose = () => { patchLast(l => { l.streaming = false }); setSending(false) }
  }

  function onKey(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send() }
  }

  if (!boot) {
    return (
      <div className="mx-auto max-w-6xl px-5 py-24 sm:px-8">
        {error ? <Alert variant="destructive"><AlertDescription>{error}</AlertDescription></Alert> : <div className="flex items-center gap-2 text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" /> Loading…</div>}
      </div>
    )
  }

  return (
    <div className="mx-auto flex max-w-6xl flex-col px-5 pb-16 pt-10 sm:px-8">
      <header className="mb-5 flex flex-wrap items-end justify-between gap-4">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.2em] text-primary">Playground</p>
          <h1 className="mt-2 font-display text-4xl">Chat with your keys</h1>
          <p className="mt-1 text-muted-foreground">Pick a provider you’ve keyed, a model, and chat. Every turn is logged in Usage like an API call.</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Toggle on={fallback} onLabel="Fallback on" offLabel="Off" onChange={setFallbackMode} title="On 429/502/503/504 or an unreachable provider, try the hops on /fallback" />
          <Toggle on={save} onLabel="Save" offLabel="Don’t save" onChange={setSaveMode} />
          <Button variant="outline" size="sm" onClick={() => { setMessages([]); setConversationId(''); window.history.replaceState({}, '', '/chat') }}>New chat</Button>
        </div>
      </header>

      {save && boot.RecentConversations.length > 0 && (
        <div className="mb-4 flex flex-wrap gap-2">
          {boot.RecentConversations.map(c => (
            <a key={c.ID} href={`/chat?c=${c.ID}`} className={cn('rounded-full border border-border px-3 py-1 text-xs', c.ID === conversationId ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:text-foreground')}>
              {c.Title || 'Chat'}
            </a>
          ))}
        </div>
      )}

      <Card className="mb-4">
        <CardContent className="grid gap-3 pt-6 sm:grid-cols-[220px_1fr]">
          {boot.Providers.length ? (
            <>
              <label className="grid gap-1.5 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                Provider
                <select value={provider} onChange={e => { setProvider(e.target.value); setModel(''); void loadModels(e.target.value) }} className="h-10 rounded-lg border border-input bg-background px-3 text-sm normal-case tracking-normal">
                  {boot.Providers.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
                </select>
              </label>
              <div className="grid gap-1.5 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                <span>Model <span className="font-normal normal-case tracking-normal text-muted-foreground/70">· {modelsStatus}</span></span>
                <ModelPicker models={suggestions} value={model} onChange={setModel} allowOther={!modelsLive} className="normal-case tracking-normal" />
              </div>
            </>
          ) : (
            <p className="text-sm text-muted-foreground sm:col-span-2">Add a provider key on <a className="text-primary underline" href="/keys">/keys</a> to use the playground.</p>
          )}
        </CardContent>
      </Card>

      <Card className="flex min-h-[420px] flex-1 flex-col">
        <CardContent className="flex flex-1 flex-col gap-3 overflow-y-auto pt-6">
          {messages.length === 0 && <p className="m-auto text-sm text-muted-foreground">Send a message to try your vault keys end-to-end.</p>}
          {messages.map((m, i) => (
            <div key={i} className={cn('flex', m.role === 'user' ? 'justify-end' : 'justify-start')}>
              <div className={cn('max-w-[85%] rounded-2xl px-4 py-3 text-sm', m.role === 'user' ? 'bg-primary text-primary-foreground' : 'border border-border bg-card')}>
                {m.fallbackTo && (
                  <div className="mb-2 rounded-lg bg-primary/10 px-2 py-1 text-xs text-muted-foreground" title={m.fallbackReason}>
                    ↳ fell back to <code className="text-primary">{m.fallbackTo}</code>{m.fallbackReason ? ` — ${m.fallbackReason}` : ''}
                  </div>
                )}
                {m.role === 'user' ? <span className="whitespace-pre-wrap">{m.content}</span> : <MarkdownMath text={m.content} streaming={m.streaming} />}
                {m.streaming && <span className="ml-1 inline-block h-4 w-1.5 animate-pulse bg-foreground/60 align-middle" />}
              </div>
            </div>
          ))}
          <div ref={bottomRef} />
        </CardContent>
        <form onSubmit={send} className="flex items-end gap-2 border-t border-border p-3">
          <textarea
            value={prompt}
            onChange={e => setPrompt(e.target.value)}
            onKeyDown={onKey}
            rows={1}
            placeholder={boot.Providers.length ? 'Ask something… (Enter to send, Shift+Enter for newline)' : 'Add keys first'}
            disabled={!boot.Providers.length || sending}
            className="max-h-40 min-h-[44px] flex-1 resize-y rounded-xl border border-input bg-background px-3 py-2.5 text-sm"
          />
          <Button type="submit" disabled={sending || !prompt.trim()}>{sending ? <Loader2 className="h-4 w-4 animate-spin" /> : <SendHorizonal className="h-4 w-4" />}</Button>
        </form>
      </Card>

      <p className="mt-3 text-xs text-muted-foreground">
        {save ? 'Save is on — chats are stored so you can reopen them.' : 'Don’t save is on — nothing is written to the database.'}
        {fallback && <> · Fallback is on — same as sending <code>"fallback": true</code> to <code>/v1</code>.</>}
      </p>
    </div>
  )
}
