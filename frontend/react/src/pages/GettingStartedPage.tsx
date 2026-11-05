import { ArrowRight } from 'lucide-react'

import { PageHero } from '@/components/PageHero'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { apiBase } from '@/lib/api-base'

const BASE = apiBase()

const STEPS = [
  { n: '01', title: 'Add keys', body: <>Paste provider API keys on /keys: Groq, Cerebras, Novita, OpenAI, Anthropic, and 35+ more. Your browser seals each one before it is sent; it is encrypted again at rest and shown only as its last four characters afterwards.</> },
  { n: '02', title: 'Mint a token per app', body: <>On <a className="text-primary underline" href="/keys">/keys</a>, mint a bearer token for each app and give it caps: day/week/month USD, a session cap, token hard stops, RPM/TPM. Scope it to one provider if the client only lets you set a model name (Cursor, Cline).</> },
  { n: '03', title: 'Point the client at one URL', body: <>Base URL <code className="font-mono text-primary">{BASE}/v1</code>, token in <code>Authorization</code>. Send <code>provider</code> + <code>model</code> (or <code>provider/model</code> as the model name). Add <code>"fallback": true</code> to walk your ladder on 429/5xx.</> },
]

const HEADERS = [
  ['X-Spendmux-Session', 'Caps one run. Same token, different session ids — one hitting its cap does not stop the others.'],
  ['X-Spendmux-Tenant', 'Caps one customer of your app for the month, across all tokens.'],
  ['X-Spendmux-Memory', 'Opt-in conversation memory. Send only new messages; Spendmux adds the thread’s earlier turns, even across a fallback to another provider. Stored encrypted under your token.'],
  ['"fallback": true (body)', 'Opt in to the ladder on /fallback. Without it, the primary’s error is returned as-is.'],
]

const ERRORS = [
  ['session_budget_exceeded', 'the session’s USD cap is spent'],
  ['usd_budget_exceeded', 'the token’s day/week/month USD cap is spent'],
  ['token_budget_exceeded', 'the token’s daily/weekly token hard stop is hit'],
  ['tenant_budget_exceeded', 'the tenant’s monthly cap is spent'],
  ['rate_limit_exceeded / tpm_exceeded', 'the token’s RPM or TPM limit'],
  ['loop_detected', 'the same request body repeated too often on one token'],
  ['key_scope_denied', 'the token is scoped to another provider/model'],
  ['fallback_not_configured', 'fallback requested but the ladder is empty'],
]

export default function GettingStartedPage() {
  return (
    <>
      <PageHero eyebrow="How it works" title="Three steps. No SDK lock-in." body="Add keys once. Call one OpenAI-compatible URL. Fail over only when you say so." />
      <section className="mx-auto max-w-6xl px-5 pb-20 sm:px-8">
        <div className="grid gap-5 md:grid-cols-3">
          {STEPS.map((s, i) => (
            <Card key={s.n} data-reveal style={{ '--reveal-i': i } as React.CSSProperties} className="smx-lift p-7">
              <span className="font-mono text-sm text-muted-foreground">{s.n}</span>
              <h3 className="mt-3 font-display text-xl">{s.title}</h3>
              <p className="mt-2 text-sm leading-relaxed text-muted-foreground [&_code]:font-mono">{s.body}</p>
            </Card>
          ))}
        </div>

        <div className="mt-14 grid gap-10 md:grid-cols-2">
          <div data-reveal>
            <h2 className="font-display text-2xl">Headers that change the outcome</h2>
            <dl className="mt-4 grid gap-3">
              {HEADERS.map(([h, d]) => (
                <div key={h} className="rounded-xl border border-border bg-card p-4"><dt className="font-mono text-sm text-primary">{h}</dt><dd className="mt-1 text-sm text-muted-foreground">{d}</dd></div>
              ))}
            </dl>
          </div>
          <div data-reveal style={{ '--reveal-i': 1 } as React.CSSProperties}>
            <h2 className="font-display text-2xl">When Spendmux says no</h2>
            <p className="mt-2 text-sm text-muted-foreground">A <code className="font-mono">429</code> with one of these codes — before the provider is billed:</p>
            <dl className="mt-4 grid gap-2">
              {ERRORS.map(([c, d]) => (
                <div key={c} className="flex gap-3 text-sm"><dt className="w-56 shrink-0 font-mono text-primary">{c}</dt><dd className="text-muted-foreground">{d}</dd></div>
              ))}
            </dl>
          </div>
        </div>

        <div data-reveal className="mt-14 rounded-2xl border border-border bg-card p-7">
          <h2 className="font-display text-2xl">Drop-in for the OpenAI SDK</h2>
          <pre className="mt-4 overflow-x-auto rounded-xl bg-zinc-950 p-5 font-mono text-[13px] leading-relaxed text-zinc-200">{`from openai import OpenAI

client = OpenAI(base_url="${BASE}/v1", api_key="sk-spendmux-...")

resp = client.chat.completions.create(
    model="gpt-oss-120b",
    extra_body={"provider": "cerebras", "fallback": True},
    extra_headers={"X-Spendmux-Session": "run-42"},
    messages=[{"role": "user", "content": "hi"}],
)`}</pre>
          <div className="mt-5 flex flex-wrap gap-3">
            <Button asChild><a href="/login">Sign in and add a key <ArrowRight className="h-4 w-4" /></a></Button>
            <Button variant="outline" asChild><a href="https://github.com/vrannang1/spendmux" target="_blank" rel="noreferrer noopener">Source on GitHub</a></Button>
          </div>
        </div>
      </section>
    </>
  )
}
