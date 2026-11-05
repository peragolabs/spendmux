import { ArrowLeft } from 'lucide-react'
import { Button } from '@/components/ui/button'

export default function NotFoundPage() {
  return (
    <section className="relative flex min-h-[calc(100vh-4rem)] items-center overflow-hidden">
      <div className="hero-glow absolute inset-0 -z-10 opacity-70" aria-hidden />
      <div className="mx-auto max-w-lg px-5 py-20 text-center sm:px-8">
        <div className="smx-word mb-6 font-display text-[7rem] leading-none tracking-tight text-primary/90 sm:text-[9rem]">
          404
        </div>
        <h1 className="smx-rise font-display text-3xl md:text-4xl [animation-delay:200ms]">Page not found.</h1>
        <p className="smx-rise mt-3 text-muted-foreground [animation-delay:280ms]">
          The page you were looking for doesn&apos;t exist, or moved somewhere else.
        </p>
        <div className="smx-rise mt-8 flex justify-center gap-3 [animation-delay:360ms]">
          <Button asChild size="lg" data-testid="not-found-home-btn">
            <a href="/">
              <ArrowLeft className="h-4 w-4" />
              Back home
            </a>
          </Button>
          <Button asChild size="lg" variant="outline">
            <a href="/contact">Contact us</a>
          </Button>
        </div>
      </div>
    </section>
  )
}
