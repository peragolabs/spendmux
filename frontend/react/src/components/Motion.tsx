import { Fragment, useEffect, useRef, useState } from 'react'

export const prefersReducedMotion = () =>
  typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches

/** True once the element has scrolled into view (and stays true). */
export function useInView<T extends Element>(threshold = 0.3) {
  const ref = useRef<T>(null)
  const [inView, setInView] = useState(false)
  useEffect(() => {
    const el = ref.current
    if (!el || inView) return
    if (!('IntersectionObserver' in window)) { setInView(true); return }
    const io = new IntersectionObserver(([e]) => { if (e.isIntersecting) { setInView(true); io.disconnect() } }, { threshold })
    io.observe(el)
    return () => io.disconnect()
  }, [inView, threshold])
  return [ref, inView] as const
}

const GLYPHS = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/='

/**
 * Scrambles `from` into `to`, left to right, when `run` flips true.
 * Reduced motion jumps straight to `to`.
 */
export function CipherText({ from, to, run, duration = 1400, className }: { from: string; to: string; run: boolean; duration?: number; className?: string }) {
  const [text, setText] = useState(from)
  useEffect(() => {
    if (!run) { setText(from); return }
    if (prefersReducedMotion()) { setText(to); return }
    const len = Math.max(from.length, to.length)
    const start = performance.now()
    let raf = 0
    const tick = (now: number) => {
      const p = Math.min(1, (now - start) / duration)
      const settled = Math.floor(p * len)
      let out = ''
      for (let i = 0; i < len; i++) {
        if (i < settled) out += to[i] ?? ''
        else if (i < settled + 6) out += GLYPHS[(Math.random() * GLYPHS.length) | 0]
        else out += from[i] ?? ''
      }
      setText(out)
      if (p < 1) raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [run, from, to, duration])
  return <span className={className}>{text}</span>
}

/** Counts from 0 to `value` when `run` flips true. */
export function CountUp({ value, run, decimals = 2, prefix = '', duration = 1200 }: { value: number; run: boolean; decimals?: number; prefix?: string; duration?: number }) {
  const [n, setN] = useState(0)
  useEffect(() => {
    if (!run) return
    if (prefersReducedMotion()) { setN(value); return }
    const start = performance.now()
    let raf = 0
    const tick = (now: number) => {
      const p = Math.min(1, (now - start) / duration)
      setN(value * (1 - Math.pow(1 - p, 3)))
      if (p < 1) raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [run, value, duration])
  return <>{prefix}{n.toFixed(decimals)}</>
}

/** Splits a headline into words that rise in one after another (.smx-word). */
export function Words({ text }: { text: string }) {
  return <>{text.split(' ').map((w, i) => <Fragment key={i}>{i > 0 && ' '}<span className="smx-word" style={{ '--w': i } as React.CSSProperties}>{w}</span></Fragment>)}</>
}
