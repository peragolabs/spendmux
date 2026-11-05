import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook, waitFor } from '@testing-library/react'
import { createPageRouter } from './navigation'

const shell = (id: string, url: string) => ({ ok: true, url: location.origin + url, text: async () => `<title>${id}</title><div id="app" data-app-id="${id}"></div>` }) as Response
let dispose: (() => void) | undefined
beforeEach(() => {
  history.replaceState(null, '', '/')
  document.body.innerHTML = '<div id="app" data-app-id="home"></div>'
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})
afterEach(() => { dispose?.(); vi.restoreAllMocks() })

function setup(fetcher = vi.fn(async () => shell('rooms', '/rooms'))) {
  const router = createPageRouter({ appId: 'home', url: location.href }, p => ['/', '/rooms', '/login'].includes(p), fetcher, id => ['home', 'rooms', 'login'].includes(id))
  dispose = router.install()
  return { router, fetcher }
}

describe('page navigation', () => {
  it('keeps the mount and updates the route without a document reload', async () => {
    const { router } = setup()
    const mount = document.getElementById('app')
    const hook = renderHook(() => router.useLocation())
    await act(async () => { await router.navigate('/rooms') })
    expect(hook.result.current.appId).toBe('rooms')
    expect(location.pathname).toBe('/rooms')
    expect(document.getElementById('app')).toBe(mount)
    expect(document.title).toBe('rooms')
  })
  it('honors a server authentication redirect before mounting protected pages', async () => {
    const { router } = setup(vi.fn(async () => shell('login', '/login?next=/rooms')))
    await router.navigate('/rooms')
    expect(location.pathname).toBe('/login')
    expect(document.getElementById('app')?.dataset.appId).toBe('login')
  })
  it('revalidates back and forward navigation with the server', async () => {
    const { router, fetcher } = setup()
    await router.navigate('/rooms')
    fetcher.mockResolvedValueOnce(shell('home', '/'))
    history.replaceState(null, '', '/')
    window.dispatchEvent(new PopStateEvent('popstate'))
    await waitFor(() => expect(document.getElementById('app')?.dataset.appId).toBe('home'))
    expect(fetcher).toHaveBeenCalledTimes(2)
  })
  it('leaves modified clicks, downloads, OAuth and external links to the browser', () => {
    const { fetcher } = setup()
    for (const [href, attributes, ctrlKey] of [
      ['/rooms', '', true], ['/rooms', 'download', false], ['/auth/google', '', false], ['https://example.com', '', false],
    ] as const) {
      const anchor = document.createElement('a')
      anchor.href = href
      if (attributes) anchor.setAttribute(attributes, '')
      document.body.append(anchor)
      const event = new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey })
      anchor.dispatchEvent(event)
      expect(event.defaultPrevented).toBe(false)
      anchor.remove()
    }
    expect(fetcher).not.toHaveBeenCalled()
  })
})


it('a slow previous response cannot replace the newest destination', async () => {
  let release!: (response: Response) => void
  const first = new Promise<Response>(resolve => { release = resolve })
  const fetcher = vi.fn().mockReturnValueOnce(first).mockResolvedValueOnce(shell('login', '/login'))
  const { router } = setup(fetcher)
  const previous = router.navigate('/rooms')
  await router.navigate('/login')
  release(shell('rooms', '/rooms'))
  await previous
  expect(location.pathname).toBe('/login')
  expect(document.getElementById('app')?.dataset.appId).toBe('login')
  expect(fetcher.mock.calls[0][1].aborted).toBe(true)
})

it('does not execute scripts embedded in a fetched page shell', async () => {
  const fetcher = vi.fn(async () => ({ ...shell('rooms', '/rooms'), text: async () =>
    '<title>rooms</title><div id="app" data-app-id="rooms"></div><script>document.body.innerHTML="executed"</script>'
  }) as Response)
  const { router } = setup(fetcher)
  await router.navigate('/rooms')
  expect(document.getElementById('app')).not.toBeNull()
  expect(document.querySelector('script')).toBeNull()
})
