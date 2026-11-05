/**
 * Scroll reveal for any element marked `data-reveal`. One observer serves the
 * whole page and a MutationObserver picks up elements rendered later (async
 * data, drawers). Elements stay visible when JS, IntersectionObserver or motion
 * are unavailable: the hidden state only applies under `html.reveal-ready`.
 *
 *   <section data-reveal>…</section>
 *   <li data-reveal style={{ '--reveal-i': i }}>…</li>   // staggered
 */
export function installReveal() {
  if (typeof window === 'undefined' || !('IntersectionObserver' in window)) return
  if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return

  const root = document.documentElement
  root.classList.add('reveal-ready')

  const io = new IntersectionObserver(entries => {
    for (const e of entries) {
      if (!e.isIntersecting) continue
      e.target.classList.add('is-revealed')
      io.unobserve(e.target)
    }
  }, { rootMargin: '0px 0px -8% 0px', threshold: 0.08 })

  const scan = (node: ParentNode) => {
    node.querySelectorAll<HTMLElement>('[data-reveal]:not(.is-revealed)').forEach(el => io.observe(el))
  }
  scan(document)
  new MutationObserver(muts => {
    for (const m of muts) m.addedNodes.forEach(n => {
      if (!(n instanceof HTMLElement)) return
      if (n.matches('[data-reveal]:not(.is-revealed)')) io.observe(n)
      scan(n)
    })
  }).observe(document.body, { childList: true, subtree: true })
}
