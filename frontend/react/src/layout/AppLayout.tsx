import { type ReactNode, useEffect, useState } from 'react'
import { LogIn, Menu, User } from 'lucide-react'

import { APP_NAME } from '@/config'
import { BrandMark } from '@/components/BrandMark'
import { ThemeSwitcher } from '@/components/ThemeSwitcher'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

interface NavItem {
  label: string
  href: string
  match: string
  key: string
}

/**
 * Server picks the page — nav is just anchor links, no client router.
 * `match` is compared against `window.location.pathname` to highlight the active tab.
 */
function useIsActive(match: string): boolean {
  if (typeof window === 'undefined') return false
  const path = window.location.pathname
  if (match === '/') return path === '/'
  return path.startsWith(match)
}

function NavLink({ item }: { item: NavItem }) {
  const active = useIsActive(item.match)
  return (
    <a
      href={item.href}
      data-testid={`nav-link-${item.key}`}
      className={cn(
        'relative rounded-full px-3 py-1.5 text-sm font-medium transition-colors',
        active
          ? 'text-foreground'
          : 'text-muted-foreground hover:text-foreground',
      )}
    >
      {item.label}
      {active && (
        <span
          className="absolute inset-x-3 -bottom-[1px] h-[2px] rounded-full bg-accent"
          aria-hidden
        />
      )}
    </a>
  )
}

export function AppLayout({ children }: { children: ReactNode }) {
  const [isLoggedIn, setIsLoggedIn] = useState(false)
  const [authLoading, setAuthLoading] = useState(true)
  const [mobileOpen, setMobileOpen] = useState(false)

  useEffect(() => {
    fetch('/api/auth/user/me', { credentials: 'include' })
      .then(r => (r.ok ? r.json() : null))
      .then(data => {
        if (data) {
          setIsLoggedIn(true)
        }
      })
      .catch(() => {})
      .finally(() => setAuthLoading(false))
  }, [])

  const nav: NavItem[] = isLoggedIn
    ? [
        { key: 'keys',       label: 'Keys',       href: '/keys',     match: '/keys' },
        { key: 'fallback',   label: 'Fallback',   href: '/fallback', match: '/fallback' },
        { key: 'usage',      label: 'Usage',      href: '/usage',    match: '/usage' },
        { key: 'playground', label: 'Playground', href: '/chat',     match: '/chat' },
      ]
    : [
        { key: 'home',    label: 'Home',    href: '/',        match: '/' },
        { key: 'docs',    label: 'How it works', href: '/about', match: '/about' },
      ]

  const year = new Date().getFullYear()

  return (
    <div className="flex min-h-screen flex-col bg-background text-foreground">
      {/* ------------------------------ Header ------------------------------ */}
      <header
        className="sticky top-0 z-40 border-b border-border/60 bg-background/70 backdrop-blur-xl"
        data-testid="app-header"
      >
        <div className="mx-auto flex h-16 max-w-6xl items-center justify-between px-5 sm:px-8">
          <a
            href="/"
            className="flex items-center gap-2.5 font-display text-lg font-semibold tracking-tight"
            aria-label={`${APP_NAME} home`}
            data-testid="brand-home-link"
          >
            <BrandMark size={16} />
            <span>{APP_NAME}</span>
          </a>

          <nav
            className="hidden items-center gap-1 md:flex"
            aria-label="Primary"
            data-testid="primary-nav"
          >
            {nav.map(item => <NavLink key={item.key} item={item} />)}
          </nav>

          <div className="flex items-center gap-2">
            <ThemeSwitcher />
            <div className="hidden sm:block">
              {authLoading ? (
                <Button variant="ghost" size="sm" disabled aria-hidden>
                  …
                </Button>
              ) : isLoggedIn ? (
                <Button
                  asChild
                  size="sm"
                  variant="default"
                  data-testid="header-account-btn"
                >
                  <a href="/account">
                    <User className="h-4 w-4" />
                    Account
                  </a>
                </Button>
              ) : (
                <Button
                  asChild
                  size="sm"
                  variant="outline"
                  data-testid="header-signin-btn"
                >
                  <a href="/login">
                    <LogIn className="h-4 w-4" />
                    Sign in
                  </a>
                </Button>
              )}
            </div>
            <Button
              variant="ghost"
              size="icon"
              className="md:hidden"
              onClick={() => setMobileOpen(v => !v)}
              aria-label="Toggle menu"
              data-testid="mobile-menu-toggle"
            >
              <Menu className="h-5 w-5" />
            </Button>
          </div>
        </div>
        {mobileOpen && (
          <div className="border-t border-border/60 bg-background md:hidden" data-testid="mobile-menu">
            <nav className="mx-auto flex max-w-6xl flex-col gap-1 px-5 py-3 sm:px-8" aria-label="Mobile">
              {nav.map(item => (
                <a
                  key={item.key}
                  href={item.href}
                  data-testid={`mobile-nav-${item.key}`}
                  className="rounded-lg px-3 py-2 text-sm font-medium text-muted-foreground hover:bg-secondary hover:text-foreground"
                >
                  {item.label}
                </a>
              ))}
              <div className="mt-2 flex gap-2">
                {isLoggedIn ? (
                  <Button asChild size="sm" className="flex-1"><a href="/account">Account</a></Button>
                ) : (
                  <Button asChild size="sm" variant="outline" className="flex-1"><a href="/login">Sign in</a></Button>
                )}
              </div>
            </nav>
          </div>
        )}
      </header>

      {/* ------------------------------ Main ------------------------------ */}
      <main className="smx-fade flex-1">{children}</main>

      {/* ------------------------------ Footer ------------------------------ */}
      <footer className="border-t border-border/60 bg-card/40" data-testid="app-footer">
        <div className="mx-auto grid max-w-6xl gap-10 px-5 py-14 sm:px-8 md:grid-cols-[1.5fr_1fr_1fr_1fr]">
          <div>
            <a href="/" className="flex items-center gap-2.5 font-display text-lg font-semibold">
              <BrandMark size={16} />
              <span>{APP_NAME}</span>
            </a>
            <p className="mt-4 max-w-xs text-sm text-muted-foreground">
              Spend control on your own LLM keys. One URL, one token per app, every dollar accounted for. Open source, self-hosted.
            </p>
          </div>

          <FooterCol title="Product" links={[
            { label: 'Home',    href: '/' },
            { label: 'How it works', href: '/about' },
          ]} />

          <FooterCol title="Account" links={[
            { label: 'Sign in',     href: '/login' },
            { label: 'Your account', href: '/account' },
          ]} />

          <FooterCol title="Legal" links={[
            { label: 'Terms',   href: '/terms' },
            { label: 'Privacy', href: '/privacy' },
            { label: 'Source on GitHub', href: 'https://github.com/vrannang1/spendmux' },
            { label: 'Hosted: spendmux.com', href: 'https://spendmux.com' },
          ]} />
        </div>
        <div className="border-t border-border/60">
          <div className="mx-auto flex max-w-6xl flex-col gap-3 px-5 py-5 text-xs text-muted-foreground sm:flex-row sm:items-center sm:justify-between sm:px-8">
            <span>© {year} {APP_NAME}. MIT licensed.</span>
            <span className="font-mono tracking-widest uppercase">Built for people who bring their own keys</span>
          </div>
        </div>
      </footer>
    </div>
  )
}

function FooterCol({ title, links }: { title: string; links: { label: string; href: string }[] }) {
  return (
    <div>
      <h4 className="mb-3 font-mono text-[0.68rem] font-semibold uppercase tracking-[0.16em] text-muted-foreground">
        {title}
      </h4>
      <ul className="space-y-2 text-sm">
        {links.map(l => (
          <li key={l.label}>
            <a href={l.href} className="text-foreground/85 transition-colors hover:text-foreground link-underline">
              {l.label}
            </a>
          </li>
        ))}
      </ul>
    </div>
  )
}
