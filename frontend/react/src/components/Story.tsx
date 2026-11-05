import { useEffect, useRef, useState } from 'react'
import { ArrowDown, Brain, Check, KeyRound, Lock, Repeat, Send, ShieldCheck, Timer, X } from 'lucide-react'

import { CipherText, CountUp, prefersReducedMotion } from '@/components/Motion'
import { cn } from '@/lib/utils'

/*
 * Animated product scenes for the landing page. Every scene is a small
 * timeline that loops while it is on screen and pauses when it is not.
 * Reduced motion shows each scene's final frame, still.
 */

/** True while the element is on screen (unlike useInView, it flips back). */
function useVisible<T extends Element>(threshold = 0.25) {
  const ref = useRef<T>(null)
  const [visible, setVisible] = useState(false)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    if (!('IntersectionObserver' in window)) { setVisible(true); return }
    const io = new IntersectionObserver(([e]) => setVisible(e.isIntersecting), { threshold })
    io.observe(el)
    return () => io.disconnect()
  }, [threshold])
  return [ref, visible] as const
}

/** Steps through `durations` while `run` is true; `loop` counts full passes. */
function useTimeline(durations: readonly number[], run: boolean) {
  const [state, setState] = useState({ step: 0, loop: 0 })
  const reduced = prefersReducedMotion()
  useEffect(() => {
    if (!run || reduced) return
    const t = setTimeout(() => setState(p => (p.step + 1 >= durations.length ? { step: 0, loop: p.loop + 1 } : { step: p.step + 1, loop: p.loop })), durations[state.step])
    return () => clearTimeout(t)
  }, [run, reduced, state, durations])
  return reduced ? { step: durations.length - 1, loop: 0 } : state
}

/** Types `text` out over `duration` ms once `run` is true, after `delay` ms. */
function useTyped(text: string, run: boolean, duration = 900, delay = 0, nonce = 0) {
  const [n, setN] = useState(0)
  useEffect(() => {
    if (!run) { setN(0); return }
    if (prefersReducedMotion()) { setN(text.length); return }
    let raf = 0
    const start = performance.now() + delay
    const tick = (now: number) => {
      const p = Math.max(0, Math.min(1, (now - start) / duration))
      setN(Math.round(p * text.length))
      if (p < 1) raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [text, run, duration, delay, nonce])
  return text.slice(0, n)
}

/* ── Shared bits ─────────────────────────────────────────────────────── */

function Frame({ children, className, innerRef }: { children: React.ReactNode; className?: string; innerRef?: React.Ref<HTMLDivElement> }) {
  return <div ref={innerRef} className={cn('relative overflow-hidden rounded-2xl border border-border bg-card p-5 shadow-[0_30px_70px_-34px_rgba(0,0,0,0.35)] sm:p-6', className)}>{children}</div>
}

function Label({ children, status, tone = 'muted' }: { children: React.ReactNode; status?: string; tone?: 'muted' | 'live' | 'ok' | 'bad' }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <span className="text-[11px] font-semibold uppercase tracking-[0.16em] text-muted-foreground">{children}</span>
      {status && (
        <span key={status} className={cn('smx-fade inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[11px] font-medium',
          tone === 'live' && 'bg-primary/10 text-primary',
          tone === 'ok' && 'bg-success/10 text-success',
          tone === 'bad' && 'bg-destructive/10 text-destructive',
          tone === 'muted' && 'bg-muted text-muted-foreground')}>
          {tone === 'live' && <span className="smx-pulse h-1.5 w-1.5 rounded-full bg-current" aria-hidden />}
          {status}
        </span>
      )}
    </div>
  )
}

function Mono({ children, className }: { children: React.ReactNode; className?: string }) {
  return <code className={cn('font-mono text-[12px]', className)}>{children}</code>
}

function Waiting({ children }: { children: React.ReactNode }) {
  return <p className="py-2 text-[13px] italic text-muted-foreground/70">{children}</p>
}

function Bar({ pct, tone = 'primary', className }: { pct: number; tone?: 'primary' | 'bad' | 'ok'; className?: string }) {
  return (
    <div className={cn('h-2 overflow-hidden rounded-full bg-muted', className)}>
      <div className={cn('h-full rounded-full transition-[width,background-color] duration-700 ease-out', tone === 'primary' && 'bg-primary', tone === 'bad' && 'bg-destructive', tone === 'ok' && 'bg-success')} style={{ width: `${Math.min(100, pct)}%` }} />
    </div>
  )
}

function Caret({ show = true }: { show?: boolean }) {
  return show ? <span className="smx-caret" aria-hidden /> : null
}

/* ── Hero: one request, start to finish ──────────────────────────────── */

const HERO_MS = [2300, 800, 2000, 800, 2600, 2200] as const // type, send, check, forward, reply, hold
const PROMPT = 'Summarize this pull request'
const REPLY = 'This PR adds Anthropic and Responses endpoints and fixes tool calls…'

/**
 * Hero demo, after sealform.co's "what your respondent types / what the server
 * stores": what your app sends, what Spendmux checks before the provider sees
 * anything, and what comes back. Every second pass the session cap trips.
 */
export function HeroFlow() {
  const [ref, visible] = useVisible<HTMLDivElement>(0.2)
  const { step, loop } = useTimeline(HERO_MS, visible)
  const blocked = loop % 2 === 1
  const sent = step >= 1
  const checking = step === 2
  const checked = step >= 3
  const replying = step >= 4

  const model = useTyped('anthropic/claude-sonnet-4-5', visible, 800, 100, loop)
  const prompt = useTyped(PROMPT, visible, 900, 1100, loop)
  const reply = useTyped(REPLY, visible && replying && !blocked, 1500)

  return (
    <div ref={ref} className="relative">
      <div key={loop}>
      {/* 1. What your app sends */}
      <Frame className="p-4 sm:p-5">
        <Label status={sent ? 'sent' : 'typing…'} tone={sent ? 'muted' : 'live'}>What your app sends</Label>
        <div className="mt-3 space-y-2">
          <div className="flex items-center justify-between gap-3 text-[12px]">
            <Mono className="text-muted-foreground">POST /v1/messages</Mono>
            <Mono className="truncate rounded-md bg-muted px-2 py-0.5 text-foreground">sk-spendmux-cursor</Mono>
          </div>
          <Field label="Model"><Mono>{model}<Caret show={!sent && model.length < 27} /></Mono></Field>
          <Field label="Message">{prompt}<Caret show={!sent && model.length >= 27} /></Field>
          <div className="flex items-center justify-between pt-1">
            <span className="text-[11px] text-muted-foreground">header <Mono>X-Spendmux-Session: run-42</Mono></span>
            <span className={cn('inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-[12px] font-semibold transition-all duration-300', sent ? 'bg-muted text-muted-foreground' : 'bg-primary text-primary-foreground', step === 0 && prompt.length === PROMPT.length && 'smx-press')}>
              <Send className="h-3.5 w-3.5" />{sent ? 'Sent' : 'Send'}
            </span>
          </div>
        </div>
      </Frame>

      <Connector active={step === 1} label="every call goes through Spendmux first" />

      {/* 2. What Spendmux checks */}
      <Frame className="p-4 sm:p-5">
        <Label status={checking ? 'checking…' : checked ? (blocked ? 'blocked' : 'passed') : undefined} tone={checking ? 'live' : blocked ? 'bad' : 'ok'}>What Spendmux checks</Label>
        <div className="mt-3 min-h-[108px]">
          {step < 2 ? <Waiting>Waiting for a request…</Waiting> : (
            <ul className="space-y-1.5 text-[12.5px]">
              <Check_ i={0} ok label="Token" value="sk-spendmux-cursor · valid" />
              <Check_ i={1} ok={!blocked} label="Session budget" value={blocked ? 'run-42 · $3.00 of $3.00' : 'run-42 · $2.31 of $3.00'} />
              {!blocked && <Check_ i={2} ok label="Rate limit" value="12 of 60 requests / min" />}
              {!blocked && <Check_ i={3} ok label="Provider key" value="anthropic · key 2 of 3 (by weight)" />}
              {blocked && <li className="smx-rise pl-6 text-[12px] text-destructive" style={{ animationDelay: '900ms' }}>Stopped here. Nothing was sent to Anthropic.</li>}
            </ul>
          )}
        </div>
      </Frame>

      <Connector active={step === 3 && !blocked} label={blocked ? 'refused before the provider is billed' : 'forwarded with your provider key'} />

      {/* 3. What comes back */}
      <Frame className="p-4 sm:p-5">
        <Label status={blocked ? (checked ? '429' : undefined) : replying ? (reply.length < REPLY.length ? 'streaming' : '200 OK') : undefined} tone={blocked ? 'bad' : reply.length < REPLY.length ? 'live' : 'ok'}>What comes back</Label>
        <div className="mt-3 min-h-[64px]">
          {blocked ? (checked ? (
            <div className="smx-rise space-y-1.5">
              <Mono className="font-semibold text-destructive">session_budget_exceeded</Mono>
              <p className="text-[12.5px] leading-snug text-muted-foreground">run-42 hit its $3.00 cap. Other apps on the same Anthropic key keep running.</p>
            </div>
          ) : <Waiting>Waiting for a response…</Waiting>) : replying ? (
            <div className="space-y-2.5">
              <p className="text-[13px] leading-snug">{reply}<Caret show={reply.length < REPLY.length} /></p>
              <div className="smx-rise flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-[11.5px] text-muted-foreground" style={{ animationDelay: '1300ms' }}>
                <span>1,204 in · 312 out</span>
                <span className="text-foreground"><CountUp value={0.0083} decimals={4} prefix="$" run={replying} /></span>
                <span>640 ms</span>
                <span className="ml-auto">run-42 · $2.32 of $3.00</span>
              </div>
            </div>
          ) : <Waiting>Waiting for a response…</Waiting>}
        </div>
      </Frame>
      </div>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="rounded-lg border border-border bg-background/70 px-3 py-2">
      <div className="text-[10.5px] uppercase tracking-wider text-muted-foreground">{label}</div>
      <div className="mt-0.5 min-h-[20px] truncate text-[13px]">{children}</div>
    </div>
  )
}

function Check_({ i, ok, label, value }: { i: number; ok: boolean; label: string; value: string }) {
  return (
    <li className="smx-rise flex items-center gap-2.5" style={{ animationDelay: `${i * 380}ms` }}>
      <span className={cn('smx-pop inline-flex h-4 w-4 shrink-0 items-center justify-center rounded-full', ok ? 'bg-success/15 text-success' : 'bg-destructive/15 text-destructive')} style={{ animationDelay: `${i * 380 + 200}ms` }}>
        {ok ? <Check className="h-3 w-3" /> : <X className="h-3 w-3" />}
      </span>
      <span className="w-[104px] shrink-0 text-muted-foreground">{label}</span>
      <span className={cn('truncate font-mono text-[11.5px]', !ok && 'text-destructive')}>{value}</span>
    </li>
  )
}

function Connector({ active, label }: { active: boolean; label: string }) {
  return (
    <div className="relative flex h-10 items-center gap-3 pl-8" aria-hidden>
      <div className="absolute bottom-0 left-[22px] top-0 w-px bg-border" />
      {active && <span className="smx-drop absolute left-[18px] h-2 w-2 rounded-full bg-primary shadow-[0_0_10px_hsl(var(--primary))]" />}
      <ArrowDown className={cn('absolute bottom-0 left-[15px] h-3.5 w-3.5 transition-colors', active ? 'text-primary' : 'text-border')} />
      <span className={cn('pl-2 text-[11px] transition-colors duration-300', active ? 'text-primary' : 'text-muted-foreground')}>{label}</span>
    </div>
  )
}

/* ── 01 Keys: sealed in the browser, pooled with weights ─────────────── */

const POOL = [
  { provider: 'anthropic', label: 'Production', raw: 'sk-ant-api03-Kx9QvA7tR2m', sealed: 'sealed · ••••tR2m', weight: 3 },
  { provider: 'anthropic', label: 'Backup', raw: 'sk-ant-api03-pL4cW1nZe8h', sealed: 'sealed · ••••Ze8h', weight: 1 },
  { provider: 'openai', label: 'Main', raw: 'sk-proj-7fQx2mB9vLk4TzR8', sealed: 'sealed · ••••TzR8', weight: 1 },
]
const POOL_MS = [1400, 1400, 1400, 1600, 2600] as const

export function KeysScene() {
  const [ref, visible] = useVisible<HTMLDivElement>()
  const { step, loop } = useTimeline(POOL_MS, visible)
  const shown = Math.min(POOL.length, step + 1)
  const shares = step >= 3
  return (
    <Frame innerRef={ref}>
      <Label status={shares ? 'balancing' : 'sealing in your browser'} tone={shares ? 'ok' : 'live'}>Provider keys</Label>
      <ul key={loop} className="mt-4 min-h-[226px] space-y-2.5">
        {POOL.slice(0, shown).map((k, i) => {
          const same = POOL.filter(p => p.provider === k.provider)
          const share = Math.round((k.weight / same.reduce((a, p) => a + p.weight, 0)) * 100)
          return (
            <li key={k.raw} className="smx-rise rounded-xl border border-border bg-background/60 px-3.5 py-2.5">
              <div className="flex items-center gap-2.5">
                <Lock className={cn('h-4 w-4 shrink-0 transition-colors duration-500', step > i || shares ? 'text-success' : 'text-muted-foreground')} />
                <span className="text-[13px] font-semibold">{k.provider}</span>
                <span className="text-[12px] text-muted-foreground">{k.label}</span>
                <span className="ml-auto text-[11px] text-muted-foreground">weight {k.weight}</span>
              </div>
              <div className="mt-1.5 flex items-center gap-3">
                <CipherText from={k.raw} to={k.sealed} run={visible} duration={1100} className="min-w-0 flex-1 truncate font-mono text-[11.5px] text-muted-foreground" />
                <span className={cn('w-[86px] transition-opacity duration-500', shares ? 'opacity-100' : 'opacity-0')}>
                  <Bar pct={shares ? share : 0} />
                </span>
                <span className={cn('w-9 text-right font-mono text-[11px] transition-opacity duration-500', shares ? 'opacity-100' : 'opacity-0')}>{share}%</span>
              </div>
            </li>
          )
        })}
      </ul>
      <p className="mt-4 text-[12px] leading-snug text-muted-foreground">Encrypted with WebCrypto before the form is sent, then wrapped again at rest. Several keys per provider share traffic by weight.</p>
    </Frame>
  )
}

/* ── 02 One-line change ──────────────────────────────────────────────── */

const SDKS = ['OpenAI SDK', 'Anthropic SDK', 'Responses API', 'Cursor', 'Cline', 'LangChain', 'Open WebUI', 'curl']
const BASE_MS = [1200, 1400, 1300, 2800] as const

export function BaseUrlScene() {
  const [ref, visible] = useVisible<HTMLDivElement>()
  const { step, loop } = useTimeline(BASE_MS, visible)
  const host = typeof window !== 'undefined' ? window.location.host : 'spendmux.app'
  const url = useTyped(`https://${host}/v1`, visible && step >= 1, 900)
  const key = useTyped('sk-spendmux-cursor', visible && step >= 2, 700)
  return (
    <Frame innerRef={ref}>
      <Label status={step >= 3 ? 'done' : 'editing'} tone={step >= 3 ? 'ok' : 'live'}>app.py</Label>
      <pre key={loop} className="mt-4 overflow-x-auto rounded-xl bg-zinc-950 p-4 font-mono text-[12px] leading-[1.75] text-zinc-300">
        <span className="text-zinc-500">from</span> openai <span className="text-zinc-500">import</span> OpenAI{'\n\n'}
        client = OpenAI({'\n'}
        {'    '}base_url=<span className="relative">
          {step < 1
            ? <span className="text-amber-300">"https://api.openai.com/v1"</span>
            : <span className="text-lime-300">"{url}"<Caret show={step === 1} /></span>}
        </span>,{'\n'}
        {'    '}api_key={step < 2 ? <span className="text-amber-300">"sk-proj-…"</span> : <span className="text-lime-300">"{key}"<Caret show={step === 2} /></span>},{'\n'}
        ){'\n'}
        <span className="text-zinc-500"># everything else stays the same</span>
      </pre>
      <ul className="mt-4 flex flex-wrap gap-1.5">
        {SDKS.map((s, i) => (
          <li key={s} className={cn('rounded-full border px-2.5 py-1 text-[11.5px] transition-all duration-500', step >= 3 ? 'border-primary/40 bg-primary/5 text-foreground' : 'border-border text-muted-foreground')} style={{ transitionDelay: step >= 3 ? `${i * 60}ms` : '0ms' }}>{s}</li>
        ))}
      </ul>
    </Frame>
  )
}

/* ── 03 Budgets per app ──────────────────────────────────────────────── */

const APPS = [
  { tok: 'sk-spendmux-cursor', cap: '$20 / day', path: [22, 48, 77, 100], spent: ['$4.40', '$9.60', '$15.40', '$20.00'] },
  { tok: 'sk-spendmux-ci', cap: '$5 / week', path: [8, 16, 26, 34], spent: ['$0.40', '$0.80', '$1.30', '$1.70'] },
  { tok: 'sk-spendmux-prod', cap: 'no cap', path: [30, 52, 70, 88], spent: ['$12.10', '$21.00', '$28.30', '$35.60'] },
]
const BUDGET_MS = [1000, 1000, 1000, 3200] as const

export function BudgetScene() {
  const [ref, visible] = useVisible<HTMLDivElement>()
  const { step } = useTimeline(BUDGET_MS, visible)
  const tripped = step >= 3
  return (
    <Frame innerRef={ref}>
      <Label status={tripped ? 'cursor capped' : 'spending'} tone={tripped ? 'bad' : 'live'}>One provider key, three wallets</Label>
      <ul className="mt-4 space-y-4">
        {APPS.map((a, i) => {
          const capped = i === 0 && tripped
          return (
            <li key={a.tok}>
              <div className="mb-1.5 flex items-baseline justify-between gap-3 text-[12px]">
                <Mono className="text-foreground">{a.tok}</Mono>
                <span className="font-mono text-muted-foreground"><span className={cn(capped ? 'text-destructive' : 'text-foreground')}>{a.spent[step]}</span> · {a.cap}</span>
              </div>
              <Bar pct={visible ? a.path[step] : 0} tone={capped ? 'bad' : i === 2 ? 'ok' : 'primary'} />
            </li>
          )
        })}
      </ul>
      <div className={cn('mt-4 rounded-lg px-3 py-2 font-mono text-[12px] transition-all duration-500', tripped ? 'translate-y-0 bg-destructive/10 text-destructive opacity-100' : 'translate-y-1 opacity-0')}>
        429 usd_budget_exceeded · only cursor stops
      </div>
    </Frame>
  )
}

/* ── 04 Sessions and tenants ─────────────────────────────────────────── */

const TENANTS = [
  { id: 'customer-114', spent: [0.4, 0.9, 1.4, 1.9], cap: 5 },
  { id: 'customer-207', spent: [1.6, 3.1, 4.4, 5], cap: 5 },
  { id: 'customer-388', spent: [0.2, 0.5, 0.7, 1.0], cap: 5 },
]

export function TenantScene() {
  const [ref, visible] = useVisible<HTMLDivElement>()
  const { step } = useTimeline(BUDGET_MS, visible)
  return (
    <Frame innerRef={ref}>
      <Label status={step >= 3 ? 'customer-207 paused' : 'metering'} tone={step >= 3 ? 'bad' : 'live'}>X-Spendmux-Tenant · $5 / day each</Label>
      <div className="mt-4 grid grid-cols-3 gap-2.5">
        {TENANTS.map(t => {
          const v = visible ? t.spent[step] : 0
          const hit = v >= t.cap
          return (
            <div key={t.id} className={cn('rounded-xl border p-3 transition-colors duration-500', hit ? 'border-destructive/50 bg-destructive/5' : 'border-border bg-background/60')}>
              <div className="truncate font-mono text-[11px] text-muted-foreground">{t.id}</div>
              <div className={cn('mt-1 font-display text-xl leading-none', hit && 'text-destructive')}>${v.toFixed(2)}</div>
              <div className="mt-3 flex h-14 items-end overflow-hidden rounded-md bg-muted">
                <div className={cn('w-full rounded-md transition-[height,background-color] duration-700 ease-out', hit ? 'bg-destructive' : 'bg-primary')} style={{ height: `${(v / t.cap) * 100}%` }} />
              </div>
              <div className={cn('mt-2 text-[11px] transition-colors', hit ? 'text-destructive' : 'text-success')}>{hit ? 'capped' : 'running'}</div>
            </div>
          )
        })}
      </div>
      <div className="mt-4 flex items-center gap-2 rounded-lg border border-dashed border-border px-3 py-2 text-[12px] text-muted-foreground">
        <Timer className="h-3.5 w-3.5 shrink-0 text-primary" />
        <span>Same idea per run: <Mono className="text-foreground">X-Spendmux-Session: run-42</Mono> caps one agent run.</span>
      </div>
    </Frame>
  )
}

/* ── 05 Loop detector ────────────────────────────────────────────────── */

const LOOP_ROWS = [
  { t: '14:02:11', body: 'read_file("src/app.ts")', status: '200' },
  { t: '14:02:13', body: 'read_file("src/app.ts")', status: '200' },
  { t: '14:02:15', body: 'read_file("src/app.ts")', status: '200' },
  { t: '14:02:17', body: 'read_file("src/app.ts")', status: '200' },
]
const LOOP_MS = [700, 700, 700, 700, 900, 3000] as const

export function LoopScene() {
  const [ref, visible] = useVisible<HTMLDivElement>()
  const { step, loop } = useTimeline(LOOP_MS, visible)
  const caught = step >= 4
  return (
    <Frame innerRef={ref}>
      <Label status={caught ? 'loop caught' : 'agent running'} tone={caught ? 'bad' : 'live'}>sk-spendmux-agent</Label>
      <ul key={loop} className="mt-4 min-h-[172px] space-y-1.5 font-mono text-[12px]">
        {LOOP_ROWS.slice(0, Math.min(LOOP_ROWS.length, step + 1)).map((r, i) => (
          <li key={i} className="smx-rise flex items-center gap-3 rounded-md px-2 py-1 text-muted-foreground">
            <span className="text-foreground/60">{r.t}</span>
            <span className="text-success">{r.status}</span>
            <span className="truncate">{r.body}</span>
            <Repeat className={cn('ml-auto h-3.5 w-3.5 shrink-0 transition-colors', i > 0 ? 'text-amber-500' : 'text-transparent')} />
          </li>
        ))}
        {caught && (
          <li className="smx-rise flex items-center gap-3 rounded-md bg-destructive/10 px-2 py-1.5 text-destructive">
            <span>14:02:19</span><span className="font-bold">429</span><span>loop_detected</span><span className="ml-auto">not billed</span>
          </li>
        )}
      </ul>
      <p className="mt-4 text-[12px] leading-snug text-muted-foreground">The same prompt replayed again and again is refused, instead of turning into a bigger bill overnight.</p>
    </Frame>
  )
}

/* ── 06 Rate limits become retries, not outages ──────────────────────── */

const FAIL_MS = [1100, 1300, 1300, 1300, 2600] as const
const FAIL_ROWS: { key: string; status: string; ms: string; note?: string }[] = [
  { key: 'key A', status: '200', ms: '412 ms' },
  { key: 'key A', status: '429', ms: '38 ms', note: 'rate limited · cooling 60s' },
  { key: 'key B', status: '200', ms: '455 ms', note: 'same call, retried in 40 ms' },
  { key: 'key C', status: '200', ms: '398 ms' },
]

export function FailoverScene() {
  const [ref, visible] = useVisible<HTMLDivElement>()
  const { step, loop } = useTimeline(FAIL_MS, visible)
  const cooling = step >= 1
  return (
    <Frame innerRef={ref}>
      <Label status={cooling ? 'no failed calls' : 'serving'} tone="ok">anthropic · 3 keys</Label>
      <div className="mt-4 grid grid-cols-3 gap-2">
        {['key A', 'key B', 'key C'].map((k, i) => {
          const cool = i === 0 && cooling
          const live = (step === 0 && i === 0) || (step === 2 && i === 1) || (step === 3 && i === 2)
          return (
            <div key={k} className={cn('flex items-center gap-2 rounded-xl border px-3 py-2 transition-all duration-500', cool ? 'border-amber-500/50 bg-amber-500/5' : live ? 'border-primary/50 bg-primary/5 shadow-[0_0_0_4px_hsl(var(--primary)/0.08)]' : 'border-border')}>
              <KeyRound className={cn('h-3.5 w-3.5', cool ? 'text-amber-500' : 'text-primary')} />
              <span className="text-[12px] font-semibold">{k}</span>
              <span className={cn('ml-auto text-[10.5px]', cool ? 'text-amber-600 dark:text-amber-400' : 'text-muted-foreground')}>{cool ? 'cooling' : 'ready'}</span>
            </div>
          )
        })}
      </div>
      <ul key={loop} className="mt-4 min-h-[136px] space-y-1.5 font-mono text-[12px]">
        {FAIL_ROWS.slice(0, Math.min(FAIL_ROWS.length, step + 1)).map((r, i) => (
          <li key={i} className={cn('smx-rise flex items-center gap-3 rounded-md px-2 py-1', r.status === '429' ? 'bg-amber-500/10' : '')}>
            <span className={r.status === '429' ? 'text-amber-600 dark:text-amber-400' : 'text-success'}>{r.status}</span>
            <span className="text-foreground/80">{r.key}</span>
            <span className="truncate text-muted-foreground">{r.note ?? ''}</span>
            <span className="ml-auto text-muted-foreground">{r.ms}</span>
          </li>
        ))}
      </ul>
      <p className="mt-4 text-[12px] leading-snug text-muted-foreground">Retry-After is honoured per key. Switching providers only happens when you send <Mono className="text-primary">"fallback": true</Mono>.</p>
    </Frame>
  )
}

/* ── 07 Conversation memory ──────────────────────────────────────────── */

const MEM_MS = [1500, 1500, 1500, 3000] as const

export function MemoryScene() {
  const [ref, visible] = useVisible<HTMLDivElement>()
  const { step, loop } = useTimeline(MEM_MS, visible)
  const q1 = useTyped('My name is Priya and I deploy on Fly.io.', visible, 900, 0, loop)
  const q2 = useTyped('Where do I deploy?', visible && step >= 2, 600)
  return (
    <Frame innerRef={ref}>
      <Label status={step >= 3 ? 'remembered' : 'thread support-91'} tone={step >= 3 ? 'ok' : 'live'}>X-Spendmux-Memory</Label>
      <div key={loop} className="mt-4 min-h-[232px] space-y-2.5 text-[13px]">
        <Bubble who="you">{q1}</Bubble>
        {step >= 1 && <Bubble who="model">Got it, Priya.</Bubble>}
        {step >= 1 && <div className="smx-rise flex items-center gap-2 pl-1 text-[11px] text-muted-foreground"><Brain className="h-3.5 w-3.5 text-primary" />Saved to the thread. Your app sends only the new message next time.</div>}
        {step >= 2 && <Bubble who="you">{q2}</Bubble>}
        {step >= 3 && <Bubble who="model">On Fly.io, Priya.</Bubble>}
      </div>
      <p className="mt-4 text-[12px] leading-snug text-muted-foreground">Opt-in per request. Spendmux keeps the recent turns, trimmed to a size budget, so clients can stay stateless.</p>
    </Frame>
  )
}

function Bubble({ who, children }: { who: 'you' | 'model'; children: React.ReactNode }) {
  return (
    <div className={cn('smx-rise flex', who === 'you' ? 'justify-end' : 'justify-start')}>
      <span className={cn('max-w-[85%] rounded-2xl px-3.5 py-2 leading-snug', who === 'you' ? 'rounded-br-md bg-primary text-primary-foreground' : 'rounded-bl-md border border-border bg-background')}>{children}</span>
    </div>
  )
}

/* ── 09 Spend log ──────────────────────────────────────────────────────── */

const SPEND = [
  { app: 'anthropic', usd: 41.02 },
  { app: 'openai', usd: 12.4 },
  { app: 'groq', usd: 3.1 },
  { app: 'cerebras', usd: 0.36 },
]

export function SpendScene() {
  const [ref, visible] = useVisible<HTMLDivElement>()
  const { loop } = useTimeline([6500], visible)
  return (
    <Frame innerRef={ref}>
      <Label status="this month" tone="muted">Spend by provider</Label>
      <div key={loop} className="mt-4">
        <div className="font-display text-4xl leading-none"><CountUp value={56.88} prefix="$" run={visible} duration={1400} /></div>
        <ul className="mt-5 space-y-2.5">
          {SPEND.map((s, i) => (
            <li key={s.app} className="grid grid-cols-[84px_1fr_58px] items-center gap-3 text-[12px]">
              <Mono className="truncate text-foreground">{s.app}</Mono>
              <div className="h-2 overflow-hidden rounded-full bg-muted">
                {visible && <div className="smx-grow h-full rounded-full bg-primary" style={{ width: `${(s.usd / SPEND[0].usd) * 100}%`, animationDelay: `${i * 140}ms` }} />}
              </div>
              <span className="text-right font-mono text-muted-foreground">${s.usd.toFixed(2)}</span>
            </li>
          ))}
        </ul>
        <div className="mt-5 grid grid-cols-3 gap-2">
          {[['requests', '18,204'], ['tokens', '41.7M'], ['avg latency', '412 ms']].map(([l, v], i) => (
            <div key={l} className="smx-rise rounded-xl border border-border bg-background/60 px-3 py-2.5" style={{ animationDelay: `${600 + i * 120}ms` }}>
              <div className="text-[11px] text-muted-foreground">{l}</div>
              <div className="mt-0.5 font-display text-lg leading-none">{v}</div>
            </div>
          ))}
        </div>
      </div>
      <p className="mt-4 text-[12px] leading-snug text-muted-foreground"><ShieldCheck className="mr-1 inline h-3.5 w-3.5 text-primary" />Provider, model, tokens, cost and latency for every call. Never your prompts.</p>
    </Frame>
  )
}

/* ── 09 Response cache ───────────────────────────────────────────────── */

const CACHE_MS = [1300, 1300, 1300, 1300, 2600] as const
const CACHE_ROWS = [
  { cache: 'miss', ms: '1,240 ms', cost: '$0.0083' },
  { cache: 'hit', ms: '4 ms', cost: '$0.0000' },
  { cache: 'hit', ms: '3 ms', cost: '$0.0000' },
  { cache: 'hit', ms: '5 ms', cost: '$0.0000' },
]

export function CacheScene() {
  const [ref, visible] = useVisible<HTMLDivElement>()
  const { step, loop } = useTimeline(CACHE_MS, visible)
  const shown = Math.min(CACHE_ROWS.length, step + 1)
  const saved = Math.max(0, shown - 1) * 0.0083
  return (
    <Frame innerRef={ref}>
      <Label status={step >= 1 ? 'serving from cache' : 'first call'} tone={step >= 1 ? 'ok' : 'live'}>X-Spendmux-Cache: 3600</Label>
      <ul key={loop} className="mt-4 min-h-[148px] space-y-1.5 font-mono text-[12px]">
        {CACHE_ROWS.slice(0, shown).map((r, i) => (
          <li key={i} className={cn('smx-rise flex items-center gap-3 rounded-md px-2 py-1.5', r.cache === 'hit' && 'bg-success/10')}>
            <span className="text-success">200</span>
            <span className={cn('rounded-full px-2 py-0.5 text-[10.5px]', r.cache === 'hit' ? 'bg-success/15 text-success' : 'bg-muted text-muted-foreground')}>{r.cache}</span>
            <span className="truncate text-muted-foreground">gpt-4o · same prompt</span>
            <span className="ml-auto text-muted-foreground">{r.ms}</span>
            <span className="w-16 text-right text-foreground">{r.cost}</span>
          </li>
        ))}
      </ul>
      <div className="mt-3 flex items-baseline justify-between rounded-lg border border-border bg-background/60 px-3 py-2">
        <span className="text-[12px] text-muted-foreground">Saved by cache</span>
        <span className="font-display text-lg leading-none">${saved.toFixed(4)}</span>
      </div>
      <p className="mt-4 text-[12px] leading-snug text-muted-foreground">Replies are stored encrypted under a key derived from the request, so only the same request can read them back.</p>
    </Frame>
  )
}

