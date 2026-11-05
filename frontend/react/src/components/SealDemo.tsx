import { useEffect, useState } from 'react'
import { Database, Globe, Lock, Monitor } from 'lucide-react'

import { CipherText, prefersReducedMotion, useInView } from '@/components/Motion'
import { cn } from '@/lib/utils'

/**
 * Key-sealing walkthrough for the landing page: a provider key is typed in the
 * browser, scrambles into an envelope, travels over the wire, and lands in the
 * database wrapped a second time. Loops while on screen.
 */

const KEY = 'sk-proj-7fQx2mB9vLk4TzR8'
const ENVELOPE = 'eyJ2IjoxLCJrIjoiUlNBLU9B'
const AT_REST = 'v2:9hK0cQ+Zr3WbnX1sPaE7'

const STEP_MS = [1100, 1500, 1300, 1400, 2600] // typing, sealing, wire, at rest, hold

export function SealDemo() {
  const [ref, inView] = useInView<HTMLDivElement>(0.35)
  const [step, setStep] = useState(0)
  const [typed, setTyped] = useState(0)

  useEffect(() => {
    if (!inView) return
    if (prefersReducedMotion()) { setTyped(KEY.length); setStep(4); return }
    const t = setTimeout(() => setStep(s => (s + 1) % STEP_MS.length), STEP_MS[step])
    return () => clearTimeout(t)
  }, [inView, step])

  useEffect(() => {
    if (!inView || step !== 0 || prefersReducedMotion()) return
    setTyped(0)
    const id = setInterval(() => setTyped(n => (n >= KEY.length ? n : n + 1)), 900 / KEY.length)
    return () => clearInterval(id)
  }, [inView, step])

  const sealed = step >= 1
  const onWire = step >= 2
  const atRest = step >= 3

  return (
    <div ref={ref} className="relative overflow-hidden rounded-2xl border border-border bg-card p-5 sm:p-7">
      <div className="grid gap-4 md:grid-cols-[1fr_auto_1fr_auto_1fr] md:items-stretch md:gap-3">
        <Stage icon={Monitor} label="Your browser" active={step <= 1} done={sealed}>
          <div className="rounded-lg border border-border bg-background px-3 py-2 font-mono text-[12px]">
            <span className="text-muted-foreground">key: </span>
            {sealed
              ? <CipherText from={KEY} to={ENVELOPE} run={sealed} className="text-primary" />
              : <>{KEY.slice(0, typed)}<span className="smx-caret" aria-hidden /></>}
          </div>
          <Tag show={sealed}>WebCrypto · RSA-OAEP-4096 + AES-256-GCM</Tag>
        </Stage>

        <Wire active={step === 2} />

        <Stage icon={Globe} label="Network, CDN, logs" active={step === 2} done={onWire}>
          <div className="rounded-lg border border-dashed border-border px-3 py-2 font-mono text-[12px] text-muted-foreground">
            {onWire ? <span className="smx-rise inline-block text-foreground/80">{ENVELOPE}…</span> : <span className="opacity-50">waiting…</span>}
          </div>
          <Tag show={onWire}>only the envelope is visible</Tag>
        </Stage>

        <Wire active={step === 3} />

        <Stage icon={Database} label="Database" active={step === 3} done={atRest}>
          <div className="rounded-lg border border-border bg-background px-3 py-2 font-mono text-[12px]">
            {atRest ? <CipherText from={ENVELOPE} to={AT_REST} run={atRest} duration={900} className="text-primary" /> : <span className="text-muted-foreground opacity-50">empty</span>}
          </div>
          <Tag show={atRest}>wrapped again · server secret</Tag>
        </Stage>
      </div>
    </div>
  )
}

function Stage({ icon: Icon, label, active, done, children }: { icon: typeof Lock; label: string; active: boolean; done: boolean; children: React.ReactNode }) {
  return (
    <div className={cn('flex min-w-0 flex-col gap-2.5 rounded-xl border p-4 transition-all duration-500', active ? 'border-primary/50 bg-primary/[0.04] shadow-[0_0_0_4px_hsl(var(--primary)/0.08)]' : 'border-border')}>
      <div className="flex items-center gap-2 text-xs font-semibold text-muted-foreground">
        <Icon className="h-4 w-4" />{label}
        <Lock className={cn('ml-auto h-3.5 w-3.5 text-success transition-all duration-500', done ? 'scale-100 opacity-100' : 'scale-50 opacity-0')} aria-hidden />
      </div>
      {children}
    </div>
  )
}

function Tag({ show, children }: { show: boolean; children: React.ReactNode }) {
  return <p className={cn('text-[11px] text-muted-foreground transition-all duration-500', show ? 'translate-y-0 opacity-100' : 'translate-y-1 opacity-0')}>{children}</p>
}

function Wire({ active }: { active: boolean }) {
  return (
    <div className="relative hidden w-10 items-center md:flex" aria-hidden>
      <div className="h-px w-full bg-border" />
      {active && <span className="smx-packet absolute left-0 top-1/2 h-2 w-2 -translate-y-1/2 rounded-full bg-primary shadow-[0_0_10px_hsl(var(--primary))]" />}
    </div>
  )
}
