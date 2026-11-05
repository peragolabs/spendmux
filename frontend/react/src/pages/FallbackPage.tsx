import { type FormEvent, useCallback, useEffect, useState } from 'react'
import { ArrowRight, Loader2, Plus } from 'lucide-react'

import { Drawer, DrawerCard } from '@/components/Drawer'
import { ModelPicker } from '@/components/ModelPicker'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { ApiError, getJSON, postForm } from '@/lib/api'

type Step = { Index: number; Position: number; Provider: string; Name: string; Model: string }
type Payload = { BaseURL: string; FallbackSteps: Step[] | null; SavedProviders: { ID: string; Name: string }[] | null }

export default function FallbackPage() {
  const [data, setData] = useState<Payload | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [provider, setProvider] = useState('')
  const [model, setModel] = useState('')
  const [models, setModels] = useState<string[]>([])
  const [modelsStatus, setModelsStatus] = useState('')
  const [modelsLive, setModelsLive] = useState(false)

  const load = useCallback(async () => {
    try {
      const d = await getJSON<Payload>('/api/fallback')
      setData(d)
      setProvider(p => p || d.SavedProviders?.[0]?.ID || '')
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Could not load the ladder')
    }
  }, [])
  useEffect(() => { void load() }, [load])

  useEffect(() => {
    if (!provider) return
    let alive = true
    setModels([])
    setModel('')
    setModelsStatus(`Fetching models from ${provider}…`)
    getJSON<{ data: { id: string }[]; meta?: { source?: string; upstream_error?: string } }>('/api/models?provider=' + encodeURIComponent(provider))
      .then(j => {
        if (!alive) return
        const ids = (j.data || []).map(m => m.id)
        setModels(ids)
        setModelsLive(j.meta?.source === 'upstream' && ids.length > 0)
        setModelsStatus(ids.length
          ? `${ids.length} model${ids.length === 1 ? '' : 's'}${j.meta?.source === 'upstream' ? ' live from ' + provider : ''}`
          : j.meta?.upstream_error ? `Live list unavailable — ${j.meta.upstream_error}. Type a model id.` : 'No models returned. Type a model id.')
      })
      .catch(() => { if (alive) { setModelsLive(false); setModelsStatus('Could not load models. Type a model id.') } })
    return () => { alive = false }
  }, [provider])

  const mutate = useCallback(async (url: string, fields: Record<string, string>) => {
    setBusy(true); setError('')
    try { await postForm(url, fields); await load(); return true }
    catch (e) { setError(e instanceof ApiError ? e.message : 'Request failed'); return false }
    finally { setBusy(false) }
  }, [load])

  async function add(e: FormEvent) {
    e.preventDefault()
    if (await mutate('/fallback', { provider, model: model.trim() })) setModel('')
  }

  if (!data) {
    return (
      <div className="mx-auto max-w-6xl px-5 py-24 sm:px-8">
        {error ? <Alert variant="destructive"><AlertDescription>{error}</AlertDescription></Alert> : <div className="flex items-center gap-2 text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" /> Loading…</div>}
      </div>
    )
  }
  const steps = data.FallbackSteps ?? []
  const saved = data.SavedProviders ?? []

  return (
    <div className="mx-auto max-w-4xl px-5 pb-24 pt-12 sm:px-8">
      <header className="mb-8">
        <p className="text-xs font-semibold uppercase tracking-[0.2em] text-primary">Fallback</p>
        <h1 className="mt-2 font-display text-4xl">Fallback ladder</h1>
        <p className="mt-2 text-muted-foreground">
          When a request sends <code>"fallback": true</code> and the primary provider answers 429/502/503/504 (or is unreachable, or has no key), Spendmux tries these hops in order. Without the flag the ladder is ignored.
        </p>
      </header>

      {error && <Alert variant="destructive" className="mb-6"><AlertDescription>{error}</AlertDescription></Alert>}

      <Card id="ladder">
        <CardHeader><CardTitle>Your ladder</CardTitle></CardHeader>
        <CardContent>
          <ol className="divide-y divide-border">
            <li className="flex items-center gap-3 py-3">
              <span className="grid h-8 w-8 shrink-0 place-items-center rounded-full bg-muted text-xs font-bold text-muted-foreground"><ArrowRight className="h-4 w-4" /></span>
              <div><strong>Primary</strong> <span className="ml-2 font-mono text-xs text-muted-foreground">whatever provider + model the request names</span></div>
            </li>
            {steps.map(s => (
              <li key={s.Index} className="flex items-center gap-3 py-3">
                <span className="grid h-8 w-8 shrink-0 place-items-center rounded-full bg-primary/10 font-mono text-xs font-bold text-primary">{s.Position}</span>
                <div className="min-w-0 flex-1"><strong>{s.Name}</strong> <span className="ml-2 font-mono text-xs text-muted-foreground">{s.Model}</span></div>
                <Button size="sm" variant="outline" className="border-destructive/40 text-destructive hover:bg-destructive/10" onClick={() => void mutate(`/fallback/${s.Index}/delete`, {})}>Remove</Button>
              </li>
            ))}
          </ol>
          {steps.length === 0 && <p className="mt-2 text-sm text-muted-foreground">No hops yet. A request with <code>fallback: true</code> gets <code>fallback_not_configured</code> until you add one.</p>}

          {saved.length ? (
            <Drawer className="mt-4" summary={<><Plus className="h-4 w-4" /> Add a hop</>}>
              <form onSubmit={add} className="grid gap-3" autoComplete="off">
                <div className="grid gap-1.5">
                  <Label className="text-sm font-semibold normal-case tracking-normal text-muted-foreground">Provider <span className="font-normal">(only providers you’ve keyed)</span></Label>
                  <select value={provider} onChange={e => setProvider(e.target.value)} className="h-10 rounded-lg border border-input bg-background px-3 text-sm">
                    {saved.map(p => <option key={p.ID} value={p.ID}>{p.Name}</option>)}
                  </select>
                </div>
                <div className="grid gap-1.5">
                  <Label className="text-sm font-semibold normal-case tracking-normal text-muted-foreground">Model <span className="font-normal">(pick from the live list, or type any id the provider accepts)</span></Label>
                  <ModelPicker models={models} value={model} onChange={setModel} status={modelsStatus} allowOther={!modelsLive} />
                </div>
                <Button type="submit" disabled={busy || !model.trim()}>{busy && <Loader2 className="h-4 w-4 animate-spin" />} Add hop</Button>
              </form>
            </Drawer>
          ) : (
            <p className="mt-4 text-sm text-muted-foreground">Save at least one provider key on <a className="text-primary underline" href="/keys">/keys</a> before building a ladder.</p>
          )}

          {steps.length > 0 && (
            <button type="button" className="mt-3 text-xs font-semibold text-muted-foreground hover:text-destructive hover:underline" onClick={() => { if (window.confirm('Clear the whole fallback ladder?')) void mutate('/fallback/clear', {}) }}>
              Clear ladder…
            </button>
          )}
        </CardContent>
      </Card>

      <div className="mt-6">
        <DrawerCard title="How to use it">
          <CardDescription className="mb-3">
            Send <code>"fallback": true</code> on any <code>/v1/chat/completions</code> call, or flip "Fallback on" in the playground. The response’s <code>meta.attempts</code> lists every hop tried; each attempt is logged on <a className="text-primary underline" href="/usage">Usage</a>. Hops outside a token’s provider/model scope are skipped, so a token pinned to one provider never fails over to another account.
          </CardDescription>
          <pre className="overflow-x-auto rounded-xl bg-zinc-950 p-4 font-mono text-xs leading-relaxed text-zinc-200">{`curl ${data.BaseURL}/v1/chat/completions \\
  -H "Authorization: Bearer YOUR_TOKEN" \\
  -H "Content-Type: application/json" \\
  -d '{"provider":"cerebras","model":"gpt-oss-120b","fallback":true,
       "messages":[{"role":"user","content":"hi"}]}'`}</pre>
        </DrawerCard>
      </div>
    </div>
  )
}
