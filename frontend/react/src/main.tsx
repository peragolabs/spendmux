import { createPageRouter } from './lib/navigation'
import { StrictMode, type ComponentType, type ReactNode } from 'react'
import { createRoot } from 'react-dom/client'

import { AppLayout } from './layout/AppLayout'
import LandingPage from './pages/LandingPage'
import LoginPage from './pages/LoginPage'
import AccountPage from './pages/AccountPage'
import GettingStartedPage from './pages/GettingStartedPage'
import NotFoundPage from './pages/NotFoundPage'
import KeysPage from './pages/KeysPage'
import PlaygroundPage from './pages/PlaygroundPage'
import UsagePage from './pages/UsagePage'
import FallbackPage from './pages/FallbackPage'
import { PrivacyPage, TermsPage } from './pages/LegalPages'

import './index.css'
import { applyStoredTheme } from './theme'
import { installReveal } from './lib/reveal'

/* Apply the persisted theme before React mounts to avoid a flash. */
applyStoredTheme()
installReveal()

const appDiv = document.getElementById('app')
if (!appDiv) throw new Error('Mount point #app not found')

const pages: Record<string, ComponentType> = {
  home: LandingPage,
  login: LoginPage,
  account: AccountPage,
  about: GettingStartedPage,
  notfound: NotFoundPage,

  // ── AI-GENERATED PAGES BELOW ─────────────────────────────
  // marker: do not remove this line
  // AI: import new page components above, add map entries below (key = data-app-id).
  keys: KeysPage,
  playground: PlaygroundPage,
  usage: UsagePage,
  fallback: FallbackPage,
  terms: TermsPage,
  privacy: PrivacyPage,
}

/**
 * Page selection order:
 *   1. Backend-injected data-app-id (production Keller shell)
 *   2. ?page= query param  → dev-only fallback for previewing every page
 *      without the Keller backend
 *   3. Fallback to `home`
 */
const queryPage = import.meta.env.DEV
  ? new URLSearchParams(window.location.search).get('page')
  : null
const appId = appDiv.dataset.appId || queryPage || 'home'
const pagePaths = new Set(['/', '/login', '/account', '/about', '/keys', '/chat', '/usage', '/fallback', '/terms', '/privacy'])
const router = createPageRouter({ appId, url: location.href }, path => pagePaths.has(path), (url, signal) => fetch(url, { credentials: 'include', signal }), id => !!pages[id])
router.install()

function Shell({ children }: { children: ReactNode }) {
  return (
    <StrictMode>
      <AppLayout>{children}</AppLayout>
    </StrictMode>
  )
}

function RoutedApp() {
  const route = router.useLocation()
  const Page = pages[route.appId] ?? NotFoundPage
  return <Shell><Page key={route.url} /></Shell>
}

createRoot(appDiv).render(<RoutedApp />)
