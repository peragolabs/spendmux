import { useState } from 'react'
import { ArrowRight, Check, ChevronDown, Database, GitFork, KeyRound, Lock, ServerCog } from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { useInView, Words } from '@/components/Motion'
import { BaseUrlScene, BudgetScene, CacheScene, FailoverScene, HeroFlow, KeysScene, LoopScene, MemoryScene, SpendScene, TenantScene } from '@/components/Story'
import { SealDemo } from '@/components/SealDemo'
import { cn } from '@/lib/utils'
import { apiBase } from '@/lib/api-base'

const BASE = apiBase()

/* ── Terminal mockup: one request through Spendmux, then the cap fires ── */

const TERMINAL_LINES: React.ReactNode[] = [
  <>$ curl {BASE}/v1/chat/completions \</>,
  <>{'    '}-H "Authorization: Bearer sk-spendmux-cursor" \</>,
  <>{'    '}-H "X-Spendmux-Session: run-42" \</>,
  <>{'    '}-d '{'{'}"provider":"cerebras","model":"gpt-oss-120b", …{'}'}'</>,
  <>{' '}</>,
  <><span className="font-bold text-lime-400">200 OK</span>  cerebras/gpt-oss-120b   124 in · 72 out   <span className="text-amber-300">$0.0001</span>   313 ms</>,
  <>{' '}</>,
  <span className="text-white/40"># …a few hundred calls later, same session</span>,
  <>$ curl … -H "X-Spendmux-Session: run-42" …</>,
  <>{' '}</>,
  <><span className="font-bold text-red-400">429 session_budget_exceeded</span>   run-42 at <span className="text-amber-300">$3.00 / $3.00</span></>,
  <span className="text-white/40"># stopped before Cerebras was billed.</span>,
  <span className="text-white/40"># other apps on the same key keep running.</span>,
]

function Terminal() {
  const [ref, inView] = useInView<HTMLPreElement>(0.4)
  return (
    <div className="overflow-hidden rounded-2xl border border-white/10 bg-zinc-950 shadow-[0_30px_70px_-30px_rgba(0,0,0,0.6)]">
      <div className="flex items-center gap-1.5 border-b border-white/10 bg-white/5 px-3.5 py-2.5">
        <span className="h-2.5 w-2.5 rounded-full bg-white/20" /><span className="h-2.5 w-2.5 rounded-full bg-white/20" /><span className="h-2.5 w-2.5 rounded-full bg-white/20" />
        <em className="ml-auto font-mono text-[11px] not-italic text-white/40">cursor · sk-spendmux-cursor</em>
      </div>
      <pre ref={ref} className={cn('smx-type overflow-x-auto p-4 font-mono text-[12px] leading-[1.7] text-zinc-200', inView && 'is-on')}>
        {TERMINAL_LINES.map((line, i) => <div key={i} style={{ '--l': i } as React.CSSProperties}>{line}</div>)}
      </pre>
    </div>
  )
}

/* ── Code tabs ───────────────────────────────────────────────────────── */

const TABS: { id: string; label: string; code: string }[] = [
  {
    id: 'python', label: 'Python · OpenAI SDK',
    code: `from openai import OpenAI

client = OpenAI(
    base_url="${BASE}/v1",
    api_key="sk-spendmux-...",          # one token per app, capped on /keys
)

resp = client.chat.completions.create(
    model="gpt-oss-120b",
    extra_body={"provider": "cerebras", "fallback": True},
    extra_headers={"X-Spendmux-Session": "run-42"},   # optional: cap this run
    messages=[{"role": "user", "content": "Summarize this repo."}],
)`,
  },
  {
    id: 'curl', label: 'curl · HTTP',
    code: `curl ${BASE}/v1/chat/completions \\
  -H "Authorization: Bearer sk-spendmux-..." \\
  -H "Content-Type: application/json" \\
  -H "X-Spendmux-Tenant: customer-123" \\
  -d '{
    "provider": "groq",
    "model": "openai/gpt-oss-120b",
    "fallback": true,
    "messages": [{"role": "user", "content": "Ping"}]
  }'`,
  },
  {
    id: 'cursor', label: 'Cursor / Cline',
    code: `# Clients that only let you set a model name (Cursor, Cline, Open WebUI):
# on /keys, mint a token scoped to one provider — then no "provider" field is needed.

# Cursor → Settings → Models → OpenAI API Key
#   Override base URL:  ${BASE}/v1
#   API key:            sk-spendmux-cursor      (scoped to cerebras, capped $20/day)
#   Model name:         gpt-oss-120b

# Or, with an unscoped token, put the provider in the model name:
#   Model name:         cerebras/gpt-oss-120b`,
  },
]

function CodeTabs() {
  const [active, setActive] = useState(TABS[0].id)
  const [copied, setCopied] = useState(false)
  const tab = TABS.find(t => t.id === active)!
  return (
    <div className="overflow-hidden rounded-2xl border border-white/10 bg-zinc-950">
      <div className="flex items-center gap-1 border-b border-white/10 bg-white/5 px-3 py-2">
        {TABS.map(t => (
          <button key={t.id} type="button" onClick={() => setActive(t.id)} className={cn('rounded-lg px-3 py-1.5 font-mono text-xs transition-colors', active === t.id ? 'bg-white/10 text-lime-300' : 'text-white/50 hover:text-white')}>{t.label}</button>
        ))}
        <button type="button" className="ml-auto rounded-lg px-3 py-1.5 font-mono text-xs text-white/50 hover:text-white" onClick={() => { void navigator.clipboard.writeText(tab.code).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1200) }) }}>{copied ? 'Copied' : 'Copy'}</button>
      </div>
      <pre className="overflow-x-auto p-6 font-mono text-[13px] leading-[1.7] text-zinc-200">{tab.code}</pre>
    </div>
  )
}

/* ── Sections ────────────────────────────────────────────────────────── */

const STORY: { n: string; title: string; body: React.ReactNode; Scene: () => React.JSX.Element }[] = [
  { n: '01', title: 'Add your keys once, sealed in your browser', Scene: KeysScene,
    body: <>Paste a provider key and your browser encrypts it before the form is sent. Add as many keys per provider as you like, each with a weight, and Spendmux spreads traffic across them.</> },
  { n: '02', title: 'Change one line in your app', Scene: BaseUrlScene,
    body: <>Point any OpenAI or Anthropic client at Spendmux. Chat Completions, <code>/v1/messages</code> and <code>/v1/responses</code> all work, along with embeddings, images, audio and moderations, on 40+ providers.</> },
  { n: '03', title: 'Give every app its own budget', Scene: BudgetScene,
    body: <>Mint one <code>sk-</code> token per app with dollar caps by day, week or month, token caps, and RPM / TPM limits. Requests that exceed the available allowance are rejected before forwarding. USD accounting uses configured model prices and conservative reservations.</> },
  { n: '04', title: 'Cap one customer, or one agent run', Scene: TenantScene,
    body: <>Send <code>X-Spendmux-Tenant</code> to give each of your customers a budget on your key, or <code>X-Spendmux-Session</code> to cap a single run. Everyone else keeps going.</> },
  { n: '05', title: 'Catch runaway agents', Scene: LoopScene,
    body: <>An agent stuck replaying the same request gets <code>loop_detected</code> instead of running up the bill while you sleep.</> },
  { n: '06', title: 'Turn rate limits into retries', Scene: FailoverScene,
    body: <>A key that returns 429 sits out for its Retry-After, and the same call moves to the next key in milliseconds. Fallback to another provider runs only when you ask for it, so you always know who billed you.</> },
  { n: '07', title: 'Let Spendmux remember the conversation', Scene: MemoryScene,
    body: <>Add <code>X-Spendmux-Memory</code> with a thread id and send only the new message. Spendmux prepends the recent turns, so simple clients get multi-turn chat for free.</> },
  { n: '08', title: 'Stop paying twice for the same answer', Scene: CacheScene,
    body: <>Prompt caching is on by default: long Claude conversations reread their earlier turns at a tenth of the price, with the same answers. Embeddings are cached too. For chat, send <code>X-Spendmux-Cache: 3600</code> or set a cache TTL on a token, and repeated requests come back in milliseconds at no cost.</> },
  { n: '09', title: 'See where every dollar went', Scene: SpendScene,
    body: <>Every call logs provider, model, tokens, list-price cost, latency and time to first token. The usage log totals it by provider, and every call can be exported as an OpenTelemetry trace. Prompts are never stored.</> },
]

function Story() {
  return (
    <div className="mt-14 space-y-20 lg:space-y-28">
      {STORY.map((s, i) => (
        <div key={s.n} className="grid grid-cols-1 items-center gap-8 lg:grid-cols-2 lg:gap-16">
          <div data-reveal className={cn('min-w-0', i % 2 === 1 && 'lg:order-2')}>
            <span className="font-mono text-sm text-primary">{s.n}</span>
            <h3 className="mt-2 max-w-md font-display text-2xl leading-tight md:text-3xl">{s.title}</h3>
            <p className="mt-3 max-w-md leading-relaxed text-muted-foreground [&_code]:font-mono [&_code]:text-[0.9em] [&_code]:text-primary">{s.body}</p>
          </div>
          <div data-reveal className="min-w-0" style={{ '--reveal-i': 1 } as React.CSSProperties}><s.Scene /></div>
        </div>
      ))}
    </div>
  )
}

const TRUST = [
  { icon: KeyRound, title: 'Sealed in your browser', body: <>WebCrypto wraps each key with the vault’s RSA-OAEP public key before the form is submitted. CDNs, load balancers, and request logs only ever see an envelope.</> },
  { icon: Database, title: 'Encrypted twice at rest', body: <>The envelope is encrypted again with a separate server secret. A leaked database or backup is useless without both keys, which never live in the database.</> },
  { icon: ServerCog, title: 'Unwrapped for one call', body: <>The key is decrypted in memory only to sign the outbound request, then dropped. It is never logged, cached to disk, or shown again; the UI keeps the last four characters.</> },
]

const FAQ: [string, React.ReactNode][] = [
  ['Can Spendmux use my provider key?', <>Yes, to forward your calls, and only that. Like any proxy, the server has to sign each request to the provider, so it unwraps your key in memory for that one call. What browser sealing adds is that the key never travels or sits in storage in the clear. Because you run Spendmux yourself, the only server that ever holds your key is yours.</>],
  ['What happens when a cap is hit?', <>The request is refused with a <code>429</code> and a specific code such as <code>session_budget_exceeded</code>, before anything is sent to the provider. Other tokens, sessions, and tenants on the same key keep working.</>],
  ['Do you store my prompts?', <>Not for API traffic. Each <code>/v1</code> request logs provider, model, token counts, list-price cost, latency, and time-to-first-token; prompt and completion bodies pass through and are not stored. If you turn on response caching, replies are kept for the TTL you choose, encrypted under a key derived from the request itself, so only the same request can read them back. Chats you start in the Playground are saved to your account so you can return to them.</>],
  ['Which clients work?', <>Anything that lets you set an OpenAI <code>base_url</code>: the OpenAI SDKs, LangChain, LlamaIndex, Cursor, Cline, Open WebUI, and plain HTTP.</>],
  ['Does fallback switch providers on its own?', <>Only when you ask. Send <code>"fallback": true</code> and Spendmux walks the ladder you set on <code>/fallback</code> on 429s and 5xx. Without it, the primary provider’s error is returned as-is.</>],
  ['What does it cost?', <>Nothing. Spendmux is MIT-licensed and runs on your own server. Tokens stay on your own provider invoice.</>],
]

function Faq() {
  return (
    <div data-reveal className="mt-10 divide-y divide-border overflow-hidden rounded-2xl border border-border bg-card">
      {FAQ.map(([q, a]) => (
        <details key={q} className="group px-6 py-5 [&_code]:font-mono [&_code]:text-primary">
          <summary className="flex cursor-pointer list-none items-center justify-between gap-4 font-display text-lg [&::-webkit-details-marker]:hidden">
            {q}
            <ChevronDown className="h-4 w-4 shrink-0 text-muted-foreground transition-transform group-open:rotate-180" />
          </summary>
          <p className="mt-3 max-w-3xl text-sm leading-relaxed text-muted-foreground">{a}</p>
        </details>
      ))}
    </div>
  )
}

const PROVIDERS = ['OpenAI', 'Anthropic', 'Groq', 'Cerebras', 'Gemini', 'Mistral', 'DeepSeek', 'Together', 'Fireworks', 'OpenRouter', 'Azure OpenAI', 'AWS Bedrock', 'Vertex AI', 'Ollama']

function Eyebrow({ children }: { children: React.ReactNode }) {
  return <p className="text-xs font-semibold uppercase tracking-[0.2em] text-primary">{children}</p>
}

export default function LandingPage() {
  return (
    <>
      {/* Hero */}
      <section className="relative overflow-hidden">
        <div className="hero-glow absolute inset-0 -z-10" aria-hidden />
        <div className="mx-auto grid max-w-6xl items-center gap-12 px-5 py-16 sm:px-8 sm:py-24 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)] lg:gap-16">
          <div>
            <Badge variant="outline" className="smx-rise mb-6 gap-2 border-border/70 bg-card/70 px-3 py-1 text-xs backdrop-blur">
              <span className="h-1.5 w-1.5 rounded-full bg-success" aria-hidden />BYOK · OpenAI &amp; Anthropic compatible
            </Badge>
            <h1 className="max-w-xl text-balance font-display text-[clamp(2.25rem,3.2vw+1rem,3.75rem)] font-medium leading-[1.04] tracking-[-0.025em]">
              <Words text={'Spend controls for every app.'} />
            </h1>
            <p className="smx-rise mt-5 max-w-lg text-[17px] [animation-delay:520ms] leading-relaxed text-muted-foreground">
              Add your provider keys once, <strong className="font-medium text-foreground">sealed in your browser</strong>. Give each app its own <code className="font-mono text-primary">sk-</code> token with a budget. Spendmux stops the call before the provider bills you, and shows exactly what spent what.
            </p>
            <div className="smx-rise mt-8 flex flex-wrap items-center gap-3 [animation-delay:640ms]">
              <Button size="lg" asChild><a href="/login">Start with your keys <ArrowRight className="h-4 w-4" /></a></Button>
              <Button size="lg" variant="outline" asChild><a href="#code">See the one-line change</a></Button>
            </div>
            <p className="smx-rise mt-3 text-xs text-muted-foreground [animation-delay:700ms]">Free and open source · MIT · runs on your server</p>
            <ul className="smx-rise mt-7 flex flex-wrap gap-x-5 [animation-delay:760ms] gap-y-2 text-xs text-muted-foreground">
              {['Drop-in for OpenAI and Anthropic SDKs', 'Works with Cursor, Cline, Open WebUI', 'API prompts never stored', 'Open source (MIT)'].map(t => (
                <li key={t} className="flex items-center gap-1.5"><Check className="h-3.5 w-3.5 text-success" />{t}</li>
              ))}
            </ul>
          </div>
          <div className="smx-rise [animation-delay:300ms]"><HeroFlow /></div>
        </div>
      </section>

      {/* Providers */}
      <section className="border-t border-border/60 bg-card/40">
        <div className="mx-auto flex max-w-6xl flex-col gap-4 px-5 py-8 sm:px-8 md:flex-row md:items-center md:gap-8">
          <p className="shrink-0 text-xs font-semibold uppercase tracking-[0.2em] text-muted-foreground">Your keys, 40+ providers</p>
          <div className="smx-marquee min-w-0 flex-1 overflow-hidden">
            <div className="smx-marquee-track gap-x-8 gap-y-2 text-sm font-medium text-foreground/70">
              <ul className="flex shrink-0 gap-x-8 gap-y-2">{PROVIDERS.map(p => <li key={p} className="whitespace-nowrap">{p}</li>)}</ul>
              <ul className="flex shrink-0 gap-x-8" aria-hidden>{PROVIDERS.map(p => <li key={p} className="whitespace-nowrap">{p}</li>)}</ul>
            </div>
          </div>
        </div>
      </section>

      {/* How it works */}
      <section id="how" className="border-t border-border/60">
        <div className="mx-auto max-w-6xl px-5 py-20 sm:px-8">
          <div data-reveal><Eyebrow>How it works</Eyebrow>
          <h2 className="mt-2 max-w-3xl font-display text-3xl md:text-4xl">Everything Spendmux does, in the order your money moves.</h2></div>
          <p className="mt-3 max-w-2xl text-muted-foreground">Anthropic can cap Anthropic’s agent. Spendmux applies per-app controls to compatible clients that speak <code>base_url</code>, on any provider, and never takes a cut of tokens.</p>
          <Story />
        </div>
      </section>

      {/* Trust model */}
      <section id="security" className="border-t border-border/60">
        <div className="mx-auto max-w-6xl px-5 py-20 sm:px-8">
          <div data-reveal><Eyebrow>Key security</Eyebrow>
          <h2 className="mt-2 max-w-3xl font-display text-3xl md:text-4xl">Your key is never in the clear on the wire or on disk.</h2></div>
          <p className="mt-3 max-w-2xl text-muted-foreground">Most gateways receive your provider key as plain text in a form post and encrypt it afterwards. Spendmux seals it in your tab first.</p>
          <div className="mt-10" data-reveal><SealDemo /></div>
          <div className="mt-5 grid gap-5 md:grid-cols-3">
            {TRUST.map((t, i) => (
              <Card key={t.title} data-reveal style={{ '--reveal-i': i } as React.CSSProperties} className="smx-lift p-7">
                <t.icon className="h-5 w-5 text-primary" />
                <h3 className="mt-4 font-display text-xl">{t.title}</h3>
                <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{t.body}</p>
              </Card>
            ))}
          </div>
          <div className="mt-5 flex flex-col gap-3 rounded-2xl border border-dashed border-border p-5 text-sm text-muted-foreground sm:flex-row sm:items-center sm:justify-between">
            <p className="flex items-start gap-2"><Lock className="mt-0.5 h-4 w-4 shrink-0 text-primary" />Keys never touch a third-party server: Spendmux runs next to your app.</p>
            <a className="inline-flex shrink-0 items-center gap-1.5 font-medium text-primary hover:underline" href="https://github.com/vrannang1/spendmux" target="_blank" rel="noreferrer noopener"><GitFork className="h-4 w-4" />Source on GitHub</a>
          </div>
        </div>
      </section>

      {/* Drop-in */}
      <section id="code" className="border-t border-border/60">
        <div className="mx-auto max-w-6xl px-5 py-20 sm:px-8">
          <div data-reveal><Eyebrow>Drop-in</Eyebrow>
          <h2 className="mt-2 font-display text-3xl md:text-4xl">Change one line: <code className="font-mono text-primary">base_url</code>.</h2></div>
          <p className="mt-3 max-w-2xl text-muted-foreground">Auth with your Spendmux token. Send <code>provider</code> and <code>model</code> as separate fields — Spendmux never guesses which account to bill. Add <code>"fallback": true</code> to walk the ladder you set on <code>/fallback</code>.</p>
          <div data-reveal className="mt-8 grid gap-6 lg:grid-cols-[minmax(0,1.15fr)_minmax(0,1fr)]">
            <CodeTabs />
            <Terminal />
          </div>
        </div>
      </section>

      {/* FAQ */}
      <section id="faq" className="border-t border-border/60">
        <div className="mx-auto max-w-6xl px-5 py-20 sm:px-8">
          <div data-reveal><Eyebrow>FAQ</Eyebrow>
          <h2 className="mt-2 font-display text-3xl md:text-4xl">Straight answers.</h2></div>
          <Faq />
        </div>
      </section>

      {/* CTA */}
      <section className="border-t border-border/60">
        <div className="mx-auto max-w-6xl px-5 py-20 sm:px-8">
          <div data-reveal className="relative overflow-hidden rounded-3xl border border-border bg-card px-6 py-12 text-center sm:px-12">
            <div className="hero-glow absolute inset-0 -z-0 opacity-50" aria-hidden />
            <div className="relative">
              <h2 className="font-display text-3xl md:text-5xl">Bring your keys. Keep your invoice.</h2>
              <p className="mx-auto mt-4 max-w-xl text-muted-foreground">Sign in with GitHub or Google, paste a provider key, mint a token, point one app at it. Five minutes.</p>
              <div className="mt-8 flex flex-wrap justify-center gap-3">
                <Button size="lg" asChild><a href="/login">Start with your keys <ArrowRight className="h-4 w-4" /></a></Button>
                <Button size="lg" variant="outline" asChild><a href="/about">How it works</a></Button>
              </div>
            </div>
          </div>
        </div>
      </section>
    </>
  )
}
