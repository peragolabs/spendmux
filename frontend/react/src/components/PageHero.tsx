import { Words } from '@/components/Motion'
export function PageHero({ eyebrow, title, body }: { eyebrow: string; title: string; body: React.ReactNode }) {
  return (
    <section className="relative overflow-hidden">
      <div className="hero-glow absolute inset-0 -z-10 opacity-60" aria-hidden />
      <div className="mx-auto max-w-5xl px-5 pt-20 pb-14 sm:px-8">
        <span className="smx-rise mono-eyebrow">{eyebrow}</span>
        <h1 className="mt-3 font-display text-4xl md:text-5xl"><Words text={title} /></h1>
        <p className="smx-rise mt-4 max-w-2xl text-muted-foreground [animation-delay:420ms]">{body}</p>
      </div>
    </section>
  )
}
