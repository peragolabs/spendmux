import { type FormEvent, useCallback, useEffect, useMemo, useState } from 'react'
import { Check, Copy, KeyRound, Loader2, Lock, Pause, Play, Plus, Snowflake } from 'lucide-react'

import { Drawer, DrawerCard } from '@/components/Drawer'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ApiError, getJSON, postForm } from '@/lib/api'
import { canSeal, sealProviderKey } from '@/lib/vault'

/* ── Shapes returned by GET /api/keys (Go field names) ────────────────── */

type ProviderConfig = {
  ID: string
  Name: string
  BaseURL: string
  Category: string
  CredMode: '' | 'aws' | 'vertex'
  RequiresBaseURL: boolean
}
type ProviderGroup = { Label: string; Items: ProviderConfig[] }
type ProviderKeyRow = { ID: string; Provider: string; Name: string; Hint: string; Label: string; BaseURL: string; Sealed: boolean; Weight: number; Cooling: string; LastError: string }
type TokenRow = {
  ID: string; Name: string; Prefix: string; Scope: string; Summary: string
  Status: 'active' | 'paused' | 'expired'; ExpiresAt: string
  DailyUSD: string; WeeklyUSD: string; MonthlyUSD: string; SessionUSD: string
  DailyLimit: number; WeeklyLimit: number; RPM: number; TPM: number; CacheTTL: number; AutoCache?: boolean
}
type TenantRow = { Tenant: string; Cap: string }

type Bootstrap = {
  User: { name: string; email: string }
  ProviderGroups: ProviderGroup[]
  ProviderCount: number
  ProviderKeys: ProviderKeyRow[]
  PlatformKeys: TokenRow[]
  TenantCaps: TenantRow[]
  BaseURL: string
}

/* ── Small helpers ───────────────────────────────────────────────────── */

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div className="grid gap-1.5">
      <Label className="text-sm font-semibold normal-case tracking-normal text-muted-foreground">
        {label} {hint && <span className="font-normal text-muted-foreground/70">({hint})</span>}
      </Label>
      {children}
    </div>
  )
}

function Select(props: React.SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      {...props}
      className="h-10 w-full rounded-lg border border-input bg-background px-3 text-sm focus:outline-none focus:ring-2 focus:ring-ring"
    />
  )
}

function CopyButton({ value }: { value: string }) {
  const [done, setDone] = useState(false)
  return (
    <Button
      type="button"
      size="sm"
      variant="outline"
      onClick={() => {
        void navigator.clipboard.writeText(value).then(() => {
          setDone(true)
          setTimeout(() => setDone(false), 1500)
        })
      }}
    >
      {done ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
      {done ? 'Copied' : 'Copy'}
    </Button>
  )
}

function RowList({ children }: { children: React.ReactNode }) {
  return <ul className="divide-y divide-border">{children}</ul>
}

function Row({ children, action }: { children: React.ReactNode; action?: React.ReactNode }) {
  return (
    <li className="flex items-center justify-between gap-4 py-3">
      <div className="min-w-0">{children}</div>
      {action}
    </li>
  )
}

function DangerButton({ onClick, label, confirm: msg }: { onClick: () => void; label: string; confirm: string }) {
  return (
    <Button
      type="button"
      size="sm"
      variant="outline"
      className="border-destructive/40 text-destructive hover:bg-destructive/10"
      onClick={() => {
        if (window.confirm(msg)) onClick()
      }}
    >
      {label}
    </Button>
  )
}

/* ── Page ────────────────────────────────────────────────────────────── */

export default function KeysPage() {
  const [data, setData] = useState<Bootstrap | null>(null)
  const [error, setError] = useState('')
  const [newKey, setNewKey] = useState('')
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    try {
      setData(await getJSON<Bootstrap>('/api/keys'))
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Could not load keys')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  /** Run a mutation, then reload the lists. Errors surface at the top. */
  const mutate = useCallback(
    async (url: string, fields: Record<string, string | number | boolean | undefined>) => {
      setBusy(true)
      setError('')
      try {
        const res = await postForm(url, fields)
        if (res.new_key) setNewKey(res.new_key)
        await load()
        return true
      } catch (e) {
        setError(e instanceof ApiError ? e.message : 'Request failed')
        return false
      } finally {
        setBusy(false)
      }
    },
    [load],
  )

  if (!data) {
    return (
      <div className="mx-auto max-w-6xl px-5 py-24 sm:px-8">
        {error ? (
          <Alert variant="destructive"><AlertDescription>{error}</AlertDescription></Alert>
        ) : (
          <div className="flex items-center gap-2 text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" /> Loading…</div>
        )}
      </div>
    )
  }

  return (
    <div className="mx-auto max-w-6xl px-5 pb-24 pt-12 sm:px-8">
      <header className="mb-8">
        <p className="text-xs font-semibold uppercase tracking-[0.2em] text-primary">Keys</p>
        <h1 className="mt-2 font-display text-4xl">Keys &amp; tokens</h1>
        <p className="mt-2 max-w-3xl text-muted-foreground">
          Add provider keys once, mint one token per app, put caps on it. Nothing can call the proxy until you mint a token.
          Proxy URL: <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-sm text-primary">{data.BaseURL}/v1</code>
        </p>
      </header>

      {error && (
        <Alert variant="destructive" className="mb-6"><AlertDescription>{error}</AlertDescription></Alert>
      )}

      <div className="grid gap-6 lg:grid-cols-2">
        <ProviderKeysCard data={data} busy={busy} mutate={mutate} />
        <TokensCard data={data} busy={busy} mutate={mutate} newKey={newKey} onDismissKey={() => setNewKey('')} />
        <TenantCapsCard data={data} busy={busy} mutate={mutate} />
        <ReceiptCard />
      </div>

      <div className="mt-6 grid gap-4">
        <DrawerCard title="Quick start — curl for every endpoint">
          <pre className="overflow-x-auto rounded-xl bg-zinc-950 p-4 font-mono text-xs leading-relaxed text-zinc-200">{quickStart(data.BaseURL)}</pre>
        </DrawerCard>
        <DrawerCard title="Why this is different">
          <ol className="grid gap-2 pl-5 text-sm text-muted-foreground [&_strong]:text-foreground">
            <li><strong>Session / run kill.</strong> Send <code>X-Spendmux-Session</code>. Cap this Cursor run at $3 — the next call 429s before the provider is billed.</li>
            <li><strong>One upstream key, many wallets.</strong> Cursor $20/day and prod unlimited share the same key. Hitting one cap does not kill the other.</li>
            <li><strong>BYOK spend always counts.</strong> Markup is 0%. If money leaves your provider account, it counts.</li>
            <li><strong>Loop detector.</strong> Identical prompts on one wallet 429 with <code>loop_detected</code>.</li>
            <li><strong>Rate-limit isolation.</strong> Per-wallet RPM/TPM. Providers rate-limit the account; Spendmux rate-limits the wallet.</li>
            <li><strong>Tenant cap on your key.</strong> <code>X-Spendmux-Tenant</code> cuts off that customer without taking down the app.</li>
            <li><strong>Pass-through, not rewrite.</strong> Your prompt reaches the provider as you sent it.</li>
            <li><strong>Receipt vs invoice.</strong> Wallet, session, and tenant dollars vs list price, including cached and reasoning tokens.</li>
          </ol>
        </DrawerCard>
      </div>
    </div>
  )
}

/* ── Provider keys ───────────────────────────────────────────────────── */

type MutateFn = (url: string, fields: Record<string, string | number | boolean | undefined>) => Promise<boolean>

function ProviderKeysCard({ data, busy, mutate }: { data: Bootstrap; busy: boolean; mutate: MutateFn }) {
  const all = useMemo(() => data.ProviderGroups.flatMap(g => g.Items), [data.ProviderGroups])
  const [provider, setProvider] = useState(all[0]?.ID ?? '')
  const cfg = all.find(p => p.ID === provider)
  const [form, setForm] = useState<Record<string, string>>({})
  const set = (k: string) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
    setForm(f => ({ ...f, [k]: e.target.value }))

  const [sealError, setSealError] = useState('')

  async function submit(e: FormEvent) {
    e.preventDefault()
    setSealError('')
    // Build the credential exactly as the server would (see providers.MarshalAWSCreds /
    // MarshalVertexCreds), seal it here, and send only the envelope.
    let secret = ''
    if (cfg?.CredMode === 'aws') {
      if (!form.aws_access_key_id || !form.aws_secret_access_key) return setSealError('AWS access key and secret are required')
      secret = JSON.stringify({ access_key_id: form.aws_access_key_id.trim(), secret_access_key: form.aws_secret_access_key.trim(), session_token: (form.aws_session_token || '').trim(), region: (form.aws_region || 'us-east-1').trim() })
    } else if (cfg?.CredMode === 'vertex') {
      if (!form.vertex_sa_json || !form.vertex_project) return setSealError('Service account JSON and GCP project are required')
      try { JSON.parse(form.vertex_sa_json) } catch { return setSealError('Service account must be valid JSON') }
      secret = JSON.stringify({ service_account_json: form.vertex_sa_json.trim(), project: form.vertex_project.trim(), location: (form.vertex_location || 'us-central1').trim() })
    } else {
      if (!form.api_key?.trim()) return setSealError('API key is required')
      secret = form.api_key.trim()
    }
    let sealed
    try {
      sealed = await sealProviderKey(secret)
    } catch (err) {
      return setSealError(err instanceof Error ? err.message : 'Could not encrypt the key in this browser')
    }
    // Hint for display: last 4 of the access key / project / api key.
    const hint = cfg?.CredMode === 'aws' ? form.aws_access_key_id.trim().slice(-4) : cfg?.CredMode === 'vertex' ? form.vertex_project.trim().slice(-4) : sealed.hint
    if (await mutate('/keys/provider', { provider, envelope: sealed.envelope, hint, label: form.label, weight: form.weight, base_url: cfg?.CredMode ? undefined : form.base_url })) setForm({})
  }

  return (
    <Card id="providers">
      <CardHeader>
        <CardTitle>Provider keys</CardTitle>
        <CardDescription>
          Encrypted at rest. {data.ProviderCount} providers. Azure / Cloudflare / vLLM / Custom need a base URL; Bedrock and Vertex use multi-field credentials.
          {' '}Add several keys for one provider to spread traffic by weight: a key that is rate-limited or failing sits out for a while and the next key takes the call.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {data.ProviderKeys.length ? (
          <RowList>
            {data.ProviderKeys.map(k => {
              const siblings = data.ProviderKeys.filter(o => o.Provider === k.Provider)
              const total = siblings.reduce((n, o) => n + o.Weight, 0)
              return (
                <li key={k.ID} className="py-3">
                  <div className="flex items-center justify-between gap-4">
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <strong>{k.Name}</strong>
                        <span className="font-mono text-xs text-muted-foreground">{k.Hint}</span>
                        {k.Label && <span className="rounded bg-muted px-1.5 py-0.5 text-xs">{k.Label}</span>}
                        {k.Sealed
                          ? <span className="inline-flex items-center gap-1 rounded bg-success/10 px-1.5 py-0.5 text-xs text-success" title="Encrypted in your browser with the vault public key; it never crossed the network in the clear"><Lock className="h-3 w-3" /> sealed in browser</span>
                          : <span className="rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground" title="Encrypted by the server at save time. Re-save to seal it in your browser.">server-encrypted</span>}
                        {siblings.length > 1 && <span className="rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground" title="Share of this provider's traffic">weight {k.Weight} · {Math.round((k.Weight / total) * 100)}%</span>}
                        {k.Cooling && <span className="inline-flex items-center gap-1 rounded bg-amber-500/10 px-1.5 py-0.5 text-xs text-amber-700 dark:text-amber-300" title="Skipped until then unless every key is cooling down"><Snowflake className="h-3 w-3" /> cooling until {k.Cooling}</span>}
                      </div>
                      {k.BaseURL && <div className="truncate font-mono text-xs text-muted-foreground">{k.BaseURL}</div>}
                      {k.LastError && <div className="truncate text-xs text-muted-foreground">Last error: {k.LastError}</div>}
                    </div>
                    <DangerButton label="Delete" confirm={`Delete the ${k.Name} key ${k.Hint}?`} onClick={() => void mutate(`/keys/provider/${k.ID}/delete`, {})} />
                  </div>
                  <Drawer nested className="mt-2" summary={<>Edit label &amp; weight</>}>
                    <EditProviderKeyForm k={k} busy={busy} mutate={mutate} />
                  </Drawer>
                </li>
              )
            })}
          </RowList>
        ) : (
          <p className="text-sm text-muted-foreground">No provider keys yet — add one below.</p>
        )}

        <Drawer className="mt-4" summary={<><Plus className="h-4 w-4" /> Add a provider key</>}>
          <form onSubmit={submit} className="grid gap-3" autoComplete="off">
            <Field label="Provider">
              <Select value={provider} onChange={e => setProvider(e.target.value)}>
                {data.ProviderGroups.map(g => (
                  <optgroup key={g.Label} label={g.Label}>
                    {g.Items.map(p => <option key={p.ID} value={p.ID}>{p.Name}</option>)}
                  </optgroup>
                ))}
              </Select>
            </Field>

            {cfg?.CredMode === 'aws' ? (
              <>
                <Field label="AWS access key id"><Input value={form.aws_access_key_id ?? ''} onChange={set('aws_access_key_id')} placeholder="AKIA…" /></Field>
                <Field label="AWS secret access key"><Input type="password" value={form.aws_secret_access_key ?? ''} onChange={set('aws_secret_access_key')} /></Field>
                <Field label="Session token" hint="optional"><Input value={form.aws_session_token ?? ''} onChange={set('aws_session_token')} /></Field>
                <Field label="Region"><Input value={form.aws_region ?? 'us-east-1'} onChange={set('aws_region')} /></Field>
              </>
            ) : cfg?.CredMode === 'vertex' ? (
              <>
                <Field label="Service account JSON">
                  <textarea rows={5} value={form.vertex_sa_json ?? ''} onChange={set('vertex_sa_json')} className="rounded-lg border border-input bg-background p-2 font-mono text-xs" placeholder='{"type":"service_account",...}' />
                </Field>
                <Field label="GCP project"><Input value={form.vertex_project ?? ''} onChange={set('vertex_project')} placeholder="my-gcp-project" /></Field>
                <Field label="Location"><Input value={form.vertex_location ?? 'us-central1'} onChange={set('vertex_location')} /></Field>
              </>
            ) : (
              <>
                <Field label="API key"><Input type="password" value={form.api_key ?? ''} onChange={set('api_key')} placeholder="sk-…" /></Field>
                <Field label="API base URL" hint={cfg?.RequiresBaseURL ? 'required for this provider' : 'optional override'}>
                  <Input type="url" value={form.base_url ?? ''} onChange={set('base_url')} placeholder={cfg?.BaseURL || 'https://…'} />
                </Field>
              </>
            )}
            <div className="grid gap-3 sm:grid-cols-[1fr_8rem]">
              <Field label="Label" hint="optional"><Input value={form.label ?? ''} onChange={set('label')} placeholder="Personal / work" /></Field>
              <Field label="Weight" hint="1-100"><Input type="number" min={1} max={100} value={form.weight ?? ''} onChange={set('weight')} placeholder="1" /></Field>
            </div>
            {sealError && <Alert variant="destructive"><AlertDescription>{sealError}</AlertDescription></Alert>}
            <Button type="submit" disabled={busy || !canSeal()}>{busy && <Loader2 className="h-4 w-4 animate-spin" />} <Lock className="h-4 w-4" /> Seal &amp; save</Button>
            <p className="text-xs text-muted-foreground">
              {canSeal()
                ? 'The key is encrypted in this browser with the vault public key before it is sent. The server stores the sealed envelope (plus its own encryption layer) and unwraps it in memory only to sign the call to the provider.'
                : 'This browser cannot encrypt in-page (WebCrypto unavailable). Keys are not sent in the clear — use a modern browser over HTTPS.'}
            </p>
          </form>
        </Drawer>
      </CardContent>
    </Card>
  )
}

/* ── Bearer tokens ───────────────────────────────────────────────────── */

function TokensCard({
  data, busy, mutate, newKey, onDismissKey,
}: { data: Bootstrap; busy: boolean; mutate: MutateFn; newKey: string; onDismissKey: () => void }) {
  const all = useMemo(() => data.ProviderGroups.flatMap(g => g.Items), [data.ProviderGroups])
  const [form, setForm] = useState<Record<string, string>>({})
  const set = (k: string) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setForm(f => ({ ...f, [k]: e.target.value }))

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (await mutate('/keys/platform', form)) setForm({})
  }

  return (
    <Card id="tokens">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><KeyRound className="h-5 w-5 text-primary" /> Bearer tokens</CardTitle>
        <CardDescription>
          One per app. Required on every <code>/v1</code> call. Stored as a hash — the plaintext is shown once.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {newKey && (
          <Alert className="mb-4 border-primary/40 bg-primary/5">
            <AlertDescription>
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <strong>Copy your token now</strong> — it won’t be shown again.
                  <code className="mt-2 block break-all rounded-lg bg-zinc-950 p-3 font-mono text-xs text-zinc-200">{newKey}</code>
                </div>
                <div className="flex shrink-0 flex-col gap-2">
                  <CopyButton value={newKey} />
                  <Button size="sm" variant="ghost" onClick={onDismissKey}>Done</Button>
                </div>
              </div>
            </AlertDescription>
          </Alert>
        )}

        {data.PlatformKeys.length ? (
          <RowList>
            {data.PlatformKeys.map(t => (
              <li key={t.ID} className="py-3">
                <div className="flex items-start justify-between gap-4">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <strong>{t.Name}</strong>
                      <span className="font-mono text-xs text-muted-foreground">{t.Prefix}</span>
                      <TokenStatus status={t.Status} />
                    </div>
                    <div className="text-xs text-muted-foreground">Scope: {t.Scope}{t.ExpiresAt && <> · expires {t.ExpiresAt}</>}</div>
                    <div className="text-xs text-muted-foreground">{t.Summary}</div>
                  </div>
                  <div className="flex shrink-0 gap-2">
                    {t.Status === 'paused' ? (
                      <Button type="button" size="sm" variant="outline" disabled={busy} onClick={() => void mutate(`/keys/platform/${t.ID}/pause`, { paused: '0' })}>
                        <Play className="h-3.5 w-3.5" /> Resume
                      </Button>
                    ) : (
                      <Button type="button" size="sm" variant="outline" disabled={busy} title="Kill switch: refuse every call on this token until resumed"
                        onClick={() => void mutate(`/keys/platform/${t.ID}/pause`, { paused: '1' })}>
                        <Pause className="h-3.5 w-3.5" /> Pause
                      </Button>
                    )}
                    <DangerButton label="Revoke" confirm={`Revoke token “${t.Name}”? Apps using it stop immediately.`} onClick={() => void mutate(`/keys/platform/${t.ID}/delete`, {})} />
                  </div>
                </div>
                <Drawer nested className="mt-2" summary={<>Edit caps &amp; expiry</>}>
                  <EditTokenForm token={t} busy={busy} mutate={mutate} />
                </Drawer>
              </li>
            ))}
          </RowList>
        ) : (
          <p className="text-sm text-muted-foreground">No tokens yet. Nothing can call <code>/v1</code> until you mint one below.</p>
        )}

        <Drawer className="mt-4" summary={<><Plus className="h-4 w-4" /> New token</>}>
          <form onSubmit={submit} className="grid gap-3" autoComplete="off">
            <Field label="Name"><Input value={form.name ?? ''} onChange={set('name')} placeholder="cursor, ci, staging…" /></Field>
            <Field label="Scope provider" hint="optional — blank = any; lets model-name-only clients like Cursor skip the provider field">
              <Select value={form.allowed_provider ?? ''} onChange={set('allowed_provider')}>
                <option value="">Any provider</option>
                {data.ProviderGroups.map(g => (
                  <optgroup key={g.Label} label={g.Label}>
                    {g.Items.map(p => <option key={p.ID} value={p.ID}>{p.Name}</option>)}
                  </optgroup>
                ))}
              </Select>
            </Field>
            <Field label="Scope model" hint="optional — requires provider">
              <Input value={form.allowed_model ?? ''} onChange={set('allowed_model')} placeholder={all.find(p => p.ID === form.allowed_provider)?.Name ? 'e.g. gpt-oss-120b' : 'e.g. llama-3.3-70b-versatile'} />
            </Field>

            <Drawer nested defaultOpen summary={<>Caps &amp; limits <span className="font-normal text-muted-foreground">(optional — all 0 = unlimited)</span></>}>
              <CapFields form={form} set={set} />
            </Drawer>

            <Button type="submit" disabled={busy}>{busy && <Loader2 className="h-4 w-4 animate-spin" />} Mint token</Button>
            <p className="text-xs text-muted-foreground">
              Tokens share your vaulted provider keys. Caps 429 before egress. Headers: <code>X-Spendmux-Session</code>, <code>X-Spendmux-Tenant</code>, <code>X-Spendmux-Cache</code>, <code>X-Spendmux-Prompt-Cache: off</code>.
            </p>
          </form>
        </Drawer>

        {data.PlatformKeys.length > 0 && (
          <button
            type="button"
            className="mt-3 text-xs font-semibold text-muted-foreground hover:text-destructive hover:underline"
            onClick={() => {
              if (window.confirm('Revoke ALL bearer tokens? Every app using them stops working immediately.')) void mutate('/keys/platform/rotate', {})
            }}
          >
            Revoke all tokens…
          </button>
        )}
      </CardContent>
    </Card>
  )
}

function TokenStatus({ status }: { status: TokenRow['Status'] }) {
  if (status === 'paused') return <Badge variant="accent"><Pause className="h-3 w-3" /> paused</Badge>
  if (status === 'expired') return <Badge variant="muted">expired</Badge>
  return <Badge variant="success">active</Badge>
}

type SetField = (k: string) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => void

/** Cap, limit and expiry inputs shared by the mint and edit forms. */
function CapFields({ form, set }: { form: Record<string, string>; set: SetField }) {
  const cap = (name: string, label: string, hint: string, step = '0.01', placeholder = '0') => (
    <Field key={name} label={label} hint={hint}>
      <Input type="number" min={0} step={step} placeholder={placeholder} value={form[name] ?? ''} onChange={set(name)} />
    </Field>
  )
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <p className="text-sm text-muted-foreground sm:col-span-2">USD caps use configured model prices and conservative request reservations, which can exceed the final charge. Missing usage or uncertain failures keep an estimated allowance charged until reconciled. Provider invoices, taxes and price changes can differ. Unpriced models and audio requests without a reliable cost bound are unavailable with USD caps.</p>
      {cap('session_usd_cap', 'Session USD cap', 'send X-Spendmux-Session', '0.01', '3')}
      {cap('daily_usd_cap', 'Day USD cap', '0 = off')}
      {cap('weekly_usd_cap', 'Week USD cap', '0 = off')}
      {cap('monthly_usd_cap', 'Month USD cap', '0 = off', '0.01', '10')}
      {cap('daily_token_limit', 'Daily token hard stop', '0 = unlimited', '1000')}
      {cap('weekly_token_limit', 'Weekly token hard stop', '0 = unlimited', '1000')}
      {cap('rpm_limit', 'RPM', '0 = default', '1')}
      {cap('tpm_limit', 'TPM', '0 = off', '1000')}
      {cap('cache_ttl', 'Response cache, seconds', '0 = off · X-Spendmux-Cache overrides', '60', '3600')}
      <Field label="Automatic caching" hint="prompt caching on Claude calls + embeddings cache; same answers, lower bill">
        <Select value={form.auto_cache ?? 'on'} onChange={set('auto_cache')}>
          <option value="on">On (recommended)</option>
          <option value="off">Off</option>
        </Select>
      </Field>
      <Field label="Expires" hint="optional — works through this day, UTC">
        <Input type="date" value={form.expires_at ?? ''} onChange={set('expires_at')} />
      </Field>
    </div>
  )
}

function EditTokenForm({ token, busy, mutate }: { token: TokenRow; busy: boolean; mutate: MutateFn }) {
  const initial = (): Record<string, string> => {
    const v = (x: string | number) => (x === '0' || x === 0 ? '' : String(x))
    return {
      name: token.Name,
      session_usd_cap: v(token.SessionUSD), daily_usd_cap: v(token.DailyUSD),
      weekly_usd_cap: v(token.WeeklyUSD), monthly_usd_cap: v(token.MonthlyUSD),
      daily_token_limit: v(token.DailyLimit), weekly_token_limit: v(token.WeeklyLimit),
      rpm_limit: v(token.RPM), tpm_limit: v(token.TPM), cache_ttl: v(token.CacheTTL ?? 0), auto_cache: token.AutoCache === false ? 'off' : 'on', expires_at: token.ExpiresAt,
    }
  }
  const [form, setForm] = useState<Record<string, string>>(initial)
  const set: SetField = k => e => setForm(f => ({ ...f, [k]: e.target.value }))
  async function submit(e: FormEvent) {
    e.preventDefault()
    await mutate(`/keys/platform/${token.ID}/update`, form)
  }
  return (
    <form onSubmit={submit} className="grid gap-3 p-4" autoComplete="off">
      <Field label="Name"><Input value={form.name ?? ''} onChange={set('name')} /></Field>
      <CapFields form={form} set={set} />
      <Button type="submit" size="sm" disabled={busy}>{busy && <Loader2 className="h-4 w-4 animate-spin" />} Save changes</Button>
      <p className="text-xs text-muted-foreground">Takes effect on the next call. The token itself doesn’t change, so apps keep working.</p>
    </form>
  )
}

function EditProviderKeyForm({ k, busy, mutate }: { k: ProviderKeyRow; busy: boolean; mutate: MutateFn }) {
  const [label, setLabel] = useState(k.Label)
  const [weight, setWeight] = useState(String(k.Weight))
  async function submit(e: FormEvent) {
    e.preventDefault()
    await mutate(`/keys/provider/${k.ID}/update`, { label, weight })
  }
  return (
    <form onSubmit={submit} className="grid gap-3 p-4 sm:grid-cols-[1fr_8rem_auto] sm:items-end" autoComplete="off">
      <Field label="Label"><Input value={label} onChange={e => setLabel(e.target.value)} /></Field>
      <Field label="Weight" hint="1-100"><Input type="number" min={1} max={100} value={weight} onChange={e => setWeight(e.target.value)} /></Field>
      <Button type="submit" size="sm" disabled={busy}>{busy && <Loader2 className="h-4 w-4 animate-spin" />} Save</Button>
      <p className="text-xs text-muted-foreground sm:col-span-3">Saving also clears a cooldown, so the key is tried again on the next call. The key itself can’t be edited; add a new one and delete this one to replace it.</p>
    </form>
  )
}

/* ── Tenant caps ─────────────────────────────────────────────────────── */

function TenantCapsCard({ data, busy, mutate }: { data: Bootstrap; busy: boolean; mutate: MutateFn }) {
  const [tenant, setTenant] = useState('')
  const [cap, setCap] = useState('')
  async function submit(e: FormEvent) {
    e.preventDefault()
    if (await mutate('/keys/tenant', { tenant, monthly_usd_cap: cap })) {
      setTenant('')
      setCap('')
    }
  }
  return (
    <Card id="tenants">
      <CardHeader>
        <CardTitle>Tenant caps</CardTitle>
        <CardDescription>Hard-stop one customer with <code>X-Spendmux-Tenant</code>. Other tenants keep running.</CardDescription>
      </CardHeader>
      <CardContent>
        {data.TenantCaps.length ? (
          <RowList>
            {data.TenantCaps.map(t => (
              <Row key={t.Tenant}><strong>{t.Tenant}</strong><div className="text-xs text-muted-foreground">${t.Cap} / month</div></Row>
            ))}
          </RowList>
        ) : (
          <p className="text-sm text-muted-foreground">No tenant caps yet.</p>
        )}
        <Drawer className="mt-4" summary={<><Plus className="h-4 w-4" /> Set a tenant cap</>}>
          <form onSubmit={submit} className="grid gap-3">
            <Field label="Tenant"><Input value={tenant} onChange={e => setTenant(e.target.value)} placeholder="customer-123" required /></Field>
            <Field label="Monthly USD cap" hint="0 = remove"><Input type="number" min={0} step="0.01" value={cap} onChange={e => setCap(e.target.value)} placeholder="50" /></Field>
            <Button type="submit" disabled={busy}>Set tenant cap</Button>
          </form>
        </Drawer>
      </CardContent>
    </Card>
  )
}

function ReceiptCard() {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Receipt</CardTitle>
        <CardDescription>Wallet, session, and tenant dollars vs list price this month — cached and reasoning tokens included.</CardDescription>
      </CardHeader>
      <CardContent>
        <p className="text-sm text-muted-foreground">
          Open <a className="text-primary underline" href="/usage">Usage</a> for the usage log.
        </p>
      </CardContent>
    </Card>
  )
}

function quickStart(base: string) {
  return `curl ${base}/v1/chat/completions \\
  -H "Authorization: Bearer YOUR_TOKEN" \\
  -H "Content-Type: application/json" \\
  -d '{
    "provider": "cerebras",
    "model": "gpt-oss-120b",
    "fallback": true,
    "messages": [{"role":"user","content":"hi"}]
  }'

# Model-name-only clients (Cursor, Cline): scope the token to a provider, then
#   base URL: ${base}/v1     model: gpt-oss-120b
# or put the provider in the model name:  cerebras/gpt-oss-120b

curl ${base}/v1/embeddings \\
  -H "Authorization: Bearer YOUR_TOKEN" -H "Content-Type: application/json" \\
  -d '{"provider":"openai","model":"text-embedding-3-small","input":"hello"}'`
}
