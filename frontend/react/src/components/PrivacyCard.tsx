import { useEffect, useState } from 'react'
import { Loader2, ShieldCheck } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

/**
 * "Privacy & data": what Spendmux stores, and the per-request history switch.
 * Reads the current value from /api/auth/me; saves via PATCH /api/auth/settings.
 */
export function PrivacyCard({ initial }: { initial?: boolean }) {
  const [keep, setKeep] = useState<boolean>(initial ?? true)
  const [saving, setSaving] = useState(false)
  const [note, setNote] = useState('')

  useEffect(() => {
    if (initial !== undefined) setKeep(initial)
  }, [initial])

  async function save(next: boolean) {
    setSaving(true)
    setNote('')
    try {
      const r = await fetch('/api/auth/settings', {
        method: 'PATCH',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify({ keep_request_history: next }),
      })
      if (!r.ok) throw new Error('save failed')
      setKeep(next)
      setNote('Saved.')
    } catch {
      setNote('Could not save. Try again.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Card id="privacy" data-testid="privacy-card">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><ShieldCheck className="h-5 w-5 text-primary" /> Privacy &amp; data</CardTitle>
        <CardDescription>What Spendmux stores about your traffic, in plain terms.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <ul className="grid gap-2 pl-5 text-sm text-muted-foreground [&_strong]:text-foreground [&_code]:font-mono [&_code]:text-primary">
          <li><strong>Request content:</strong> forwarded to your provider. Response caching stores encrypted responses for the configured cache duration; embedding caching is enabled by default unless you turn it off. In-flight processing and memory features may retain content in memory. Saved Playground chats are stored separately.</li>
          <li><strong>Per request:</strong> provider, model, token counts, list-price cost, latency, time-to-first-token, HTTP status, and any <code>X-Spendmux-Session</code> / <code>X-Spendmux-Tenant</code> label you send. These power caps and the usage log.</li>
          <li><strong>Loop detector:</strong> a SHA-256 fingerprint of the request body, kept only for the detection window.</li>
          <li><strong>Provider keys:</strong> sealed in your browser with the vault's public key before they are sent, so the plaintext never crosses the network. At rest the sealed envelope is encrypted again with a server secret; it is unwrapped in memory only to sign the outbound call, then dropped. Bearer tokens are stored as a SHA-256 hash.</li>
          <li><strong>Playground:</strong> chats are saved only while "Save" is on, and you can delete them.</li>
        </ul>
        <label className="flex cursor-pointer items-start gap-3 rounded-xl border border-border p-3">
          <input type="checkbox" className="mt-1 h-4 w-4 accent-[hsl(var(--primary))]" checked={keep} disabled={saving} onChange={e => void save(e.target.checked)} />
          <span>
            <strong className="text-sm">Keep per-request history</strong>
            <span className="mt-1 block text-xs leading-relaxed text-muted-foreground">
              On: the "Recent requests" table on <a className="text-primary underline" href="/usage">Usage</a> lists each call, including upstream error text.
              Off: only totals and per-provider / per-model spend are kept; error text is dropped. Token counts and cost are recorded either way so your caps keep working.
            </span>
          </span>
          {saving && <Loader2 className="ml-auto h-4 w-4 animate-spin text-muted-foreground" />}
        </label>
        {note && <p className="text-xs text-muted-foreground">{note}</p>}
        <p className="text-xs text-muted-foreground">Full details: <a className="text-primary underline" href="/privacy">Privacy Policy</a>.</p>
        <Button asChild variant="outline" size="sm"><a href="/usage#recent">Open request history</a></Button>
      </CardContent>
    </Card>
  )
}
