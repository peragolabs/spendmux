import { useEffect, useState } from 'react'
import { Loader2 } from 'lucide-react'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { getJSON } from '@/lib/api'
import { cn } from '@/lib/utils'

/* ── GET /api/usage ──────────────────────────────────────────────────── */

type ProviderRow = { Provider: string; Requests: number; PromptTokens: number; Completion: number; Cost: string; AvgLatency: string }
type RecentRow = { When: string; Provider: string; Model: string; Endpoint: string; Tokens: number; Cost: string; Latency: string; Status: number }
type Payload = {
  MonthLabel: string
  MonthCost: string
  MonthReqs: number
  MonthTokens: number
  ByProvider: ProviderRow[]
  Recent: RecentRow[]
}

function Th({ children, right }: { children: React.ReactNode; right?: boolean }) {
  return <th className={cn('px-2 py-2 text-left text-[11px] font-semibold uppercase tracking-wider text-muted-foreground', right && 'text-right')}>{children}</th>
}
function Td({ children, right, mono }: { children: React.ReactNode; right?: boolean; mono?: boolean }) {
  return <td className={cn('whitespace-nowrap border-t border-border px-2 py-2.5 text-sm', right && 'text-right', mono && 'font-mono text-xs text-primary')}>{children}</td>
}

export default function UsagePage() {
  const [data, setData] = useState<Payload | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    getJSON<Payload>('/api/usage').then(setData).catch(e => setError(e instanceof Error ? e.message : 'Could not load usage'))
  }, [])

  if (!data) {
    return (
      <div className="mx-auto max-w-6xl px-5 py-24 sm:px-8">
        {error ? <Alert variant="destructive"><AlertDescription>{error}</AlertDescription></Alert> : <div className="flex items-center gap-2 text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" /> Loading…</div>}
      </div>
    )
  }

  return (
    <div className="mx-auto max-w-6xl px-5 pb-24 pt-12 sm:px-8">
      <header className="mb-8">
        <p className="text-xs font-semibold uppercase tracking-[0.2em] text-primary">Usage</p>
        <h1 className="mt-2 font-display text-4xl">Usage log</h1>
        <p className="mt-2 text-muted-foreground">Est. spend from your proxied calls this month ({data.MonthLabel}), at list price. Prompts are never stored.</p>
      </header>

      <div className="grid gap-4 sm:grid-cols-3">
        {[['Est. spend', data.MonthCost], ['Requests', data.MonthReqs.toLocaleString()], ['Tokens', data.MonthTokens.toLocaleString()]].map(([k, v]) => (
          <Card key={k}><CardContent className="pt-6"><p className="text-sm text-muted-foreground">{k}</p><p className="mt-1 font-display text-3xl">{v}</p></CardContent></Card>
        ))}
      </div>

      <Card className="mt-6">
        <CardHeader><CardTitle>By provider</CardTitle></CardHeader>
        <CardContent>
          {data.ByProvider.length ? (
            <div className="overflow-x-auto">
              <table className="w-full border-collapse">
                <thead><tr><Th>Provider</Th><Th right>Reqs</Th><Th right>Prompt</Th><Th right>Completion</Th><Th right>Est. cost</Th><Th right>Avg latency</Th></tr></thead>
                <tbody>{data.ByProvider.map(r => (
                  <tr key={r.Provider}><Td>{r.Provider}</Td><Td right>{r.Requests}</Td><Td right>{r.PromptTokens.toLocaleString()}</Td><Td right>{r.Completion.toLocaleString()}</Td><Td right>{r.Cost}</Td><Td right>{r.AvgLatency}</Td></tr>
                ))}</tbody>
              </table>
            </div>
          ) : <p className="text-sm text-muted-foreground">No usage yet. Call <code>/v1/chat/completions</code> with a bearer token, or use the <a className="text-primary underline" href="/chat">playground</a>.</p>}
        </CardContent>
      </Card>

      <Card className="mt-6" id="recent">
        <CardHeader><CardTitle>Recent requests</CardTitle></CardHeader>
        <CardContent>
          {data.Recent.length ? (
            <div className="overflow-x-auto">
              <table className="w-full border-collapse">
                <thead><tr><Th>When</Th><Th>Provider</Th><Th>Model</Th><Th>Endpoint</Th><Th right>Tokens</Th><Th right>Cost</Th><Th right>Latency</Th><Th>Status</Th></tr></thead>
                <tbody>{data.Recent.map((r, i) => (
                  <tr key={i}>
                    <Td>{r.When}</Td><Td>{r.Provider}</Td><Td mono>{r.Model}</Td><Td>{r.Endpoint}</Td><Td right>{r.Tokens}</Td><Td right>{r.Cost}</Td><Td right>{r.Latency}</Td>
                    <Td>{r.Status >= 400 ? <span className="font-bold text-destructive">{r.Status}</span> : r.Status}</Td>
                  </tr>
                ))}</tbody>
              </table>
            </div>
          ) : <p className="text-sm text-muted-foreground">No requests logged yet.</p>}
        </CardContent>
      </Card>
    </div>
  )
}
