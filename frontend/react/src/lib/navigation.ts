import { useSyncExternalStore } from 'react'

let activeNavigate: ((href: string) => Promise<void>) | undefined

/** Browser pages use the installed router; other entry points retain navigation. */
export function navigatePage(href: string) {
  if (activeNavigate) void activeNavigate(href)
  else window.location.assign(href)
}

export interface PageLocation { appId: string; url: string }

/** Keep page authorization on the server, but retain the React application
 * across navigations. Only ordinary links to known page routes are intercepted. */
export function createPageRouter(
  initial: PageLocation,
  knownPath: (pathname: string) => boolean,
  fetchPage: (url: string, signal: AbortSignal) => Promise<Response>,
  knownPage: (id: string) => boolean,
) {
  let current = initial
  let pending: AbortController | undefined
  const listeners = new Set<() => void>()
  const subscribe = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener) } }
  const snapshot = () => current

  async function navigate(href: string, pop = false) {
    const url = new URL(href, location.href)
    if (url.origin !== location.origin || !knownPath(url.pathname)) {
      location.assign(url.href)
      return
    }
    pending?.abort()
    const controller = new AbortController()
    pending = controller
    try {
      const response = await fetchPage(url.href, controller.signal)
      const destination = new URL(response.url || url.href)
      if (!response.ok || destination.origin !== location.origin || !knownPath(destination.pathname)) throw new Error('Document navigation required')
      const documentNext = new DOMParser().parseFromString(await response.text(), 'text/html')
      const appId = documentNext.querySelector<HTMLElement>('#app')?.dataset.appId
      if (!appId || !knownPage(appId)) throw new Error('Unknown page shell')
      const modules = (doc: Document) => Array.from(doc.querySelectorAll<HTMLScriptElement>('script[type="module"][src]')).map(s => s.getAttribute('src')).join('|')
      if (modules(documentNext) !== modules(document)) throw new Error('Application version changed')
      if (controller.signal.aborted) return
      destination.hash = url.hash
      if (!pop) history.pushState(null, '', destination.href)
      else if (destination.href !== location.href) history.replaceState(null, '', destination.href)
      document.title = documentNext.title
      document.getElementById('app')!.dataset.appId = appId
      current = { appId, url: destination.href }
      listeners.forEach(listener => listener())
      requestAnimationFrame(() => {
        if (destination.hash) document.getElementById(decodeURIComponent(destination.hash.slice(1)))?.scrollIntoView()
        else window.scrollTo(0, 0)
      })
    } catch {
      if (!controller.signal.aborted) location.assign(url.href)
    }
  }

  function click(event: MouseEvent) {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
    const anchor = event.target instanceof Element ? event.target.closest<HTMLAnchorElement>('a[href]') : null
    if (!anchor || anchor.hasAttribute('download') || (anchor.target && anchor.target !== '_self') || anchor.hasAttribute('data-full-navigation') || anchor.rel.includes('external')) return
    const url = new URL(anchor.href, location.href)
    if (url.origin !== location.origin || !knownPath(url.pathname)) return
    if (url.pathname === location.pathname && url.search === location.search && url.hash) return
    event.preventDefault()
    void navigate(url.href)
  }
  function popstate() { void navigate(location.href, true) }
  return {
    navigate,
    useLocation: () => useSyncExternalStore(subscribe, snapshot),
    install() {
      activeNavigate = navigate
      document.addEventListener('click', click)
      window.addEventListener('popstate', popstate)
      return () => { if (activeNavigate === navigate) activeNavigate = undefined; pending?.abort(); document.removeEventListener('click', click); window.removeEventListener('popstate', popstate) }
    },
  }
}
