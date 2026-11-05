import { useEffect, useState } from 'react'
import { Github, Lock } from 'lucide-react'

import { APP_NAME } from '@/config'
import { BrandMark } from '@/components/BrandMark'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { AppleIcon, GoogleIcon } from '@/components/ProviderIcons'

/**
 * Sign-in is OAuth-only: Google, GitHub, Apple. No email/password.
 * Only the providers this server has a client id for are shown (/api/site).
 */
const ALL_PROVIDERS = ['google', 'github', 'apple']

export default function LoginPage() {
  const [error, setError] = useState('')
  const [enabled, setEnabled] = useState<string[] | null>(null)
  const next = typeof window !== 'undefined' ? new URLSearchParams(window.location.search).get('next') : null
  const q = next ? `?next=${encodeURIComponent(next)}` : ''

  useEffect(() => {
    const e = new URLSearchParams(window.location.search).get('error')
    if (e) setError(decodeURIComponent(e))
    fetch('/api/site')
      .then((r) => (r.ok ? r.json() : null))
      .then((d) => setEnabled(Array.isArray(d?.sign_in) ? d.sign_in : ALL_PROVIDERS))
      .catch(() => setEnabled(ALL_PROVIDERS))
  }, [])
  const show = (id: string) => enabled?.includes(id)

  return (
    <section className="relative overflow-hidden">
      <div className="hero-glow absolute inset-0 -z-10 opacity-60" aria-hidden />
      <div className="mx-auto flex max-w-6xl items-center justify-center px-5 py-20 sm:px-8">
        <div className="smx-scene-in w-full max-w-md rounded-2xl border border-border bg-card p-8 shadow-[0_30px_70px_-40px_hsl(var(--primary)/0.45)]">
          <div className="mb-6 flex items-center gap-2.5 font-display text-lg font-semibold">
            <BrandMark size={16} /> {APP_NAME}
          </div>
          <p className="text-xs font-semibold uppercase tracking-[0.2em] text-primary">Sign in</p>
          <h1 className="mt-2 font-display text-3xl">Bring your keys.</h1>
          <p className="mt-2 text-sm text-muted-foreground">Add every provider key once. Hit one URL. See where the money went. Signing in creates your account if you don’t have one.</p>

          {error && <Alert variant="destructive" className="mt-5"><AlertDescription>{error}</AlertDescription></Alert>}

          <div className="mt-6 space-y-2">
            {show('google') && (
              <Button asChild variant="outline" size="lg" className="w-full" data-testid="google-oauth-btn">
                <a href={`/auth/google${q}`}><GoogleIcon /> Continue with Google</a>
              </Button>
            )}
            {show('github') && (
              <Button asChild variant="outline" size="lg" className="w-full" data-testid="github-oauth-btn">
                <a href={`/auth/github${q}`}><Github className="h-4 w-4" /> Continue with GitHub</a>
              </Button>
            )}
            {show('apple') && (
              <Button asChild variant="outline" size="lg" className="w-full" data-testid="apple-oauth-btn">
                <a href={`/auth/apple${q}`}><AppleIcon /> Continue with Apple</a>
              </Button>
            )}
            {enabled?.length === 0 && (
              <p className="rounded-xl border border-border bg-muted/40 p-4 text-sm text-muted-foreground">
                No sign-in provider is set up on this server yet. Set <code className="font-mono text-foreground">GITHUB_CLIENT_ID</code> and <code className="font-mono text-foreground">GITHUB_CLIENT_SECRET</code> (or Google / Apple) and restart.
              </p>
            )}
          </div>

          <p className="mt-6 flex items-start gap-2 text-xs leading-relaxed text-muted-foreground">
            <Lock className="mt-0.5 h-3.5 w-3.5 shrink-0" />
            <span>We store your sign-in identity. Provider keys are sealed in your browser before they are sent and your apps only ever see a per-app bearer token. <a className="text-primary underline" href="/privacy">Privacy</a> · <a className="text-primary underline" href="/terms">Terms</a></span>
          </p>
        </div>
      </div>
    </section>
  )
}
