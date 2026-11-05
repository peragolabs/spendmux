/**
 * Thin fetch helpers for the Spendmux app pages.
 *
 * GET endpoints return JSON. Mutations are form-encoded POSTs; the server
 * answers JSON when we send `Accept: application/json`:
 *   { ok: true, new_key?: string }   |   { ok: false, error: code, message }
 * A 401 means the session is gone → /login.
 */

export class ApiError extends Error {
  status: number
  code: string
  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

function redirectIfGated(status: number) {
  if (status === 401) window.location.href = '/login?next=' + encodeURIComponent(window.location.pathname)
}

export async function getJSON<T>(url: string): Promise<T> {
  const r = await fetch(url, { credentials: 'include', headers: { Accept: 'application/json' } })
  if (!r.ok) {
    redirectIfGated(r.status)
    let msg = `HTTP ${r.status}`
    try {
      const j = await r.json()
      msg = j.message || j.error || msg
    } catch {
      /* not json */
    }
    throw new ApiError(r.status, 'request_failed', msg)
  }
  return r.json() as Promise<T>
}

export type MutationResult = { ok: true; new_key?: string }

export async function postForm(url: string, fields: Record<string, string | number | boolean | undefined>): Promise<MutationResult> {
  const body = new URLSearchParams()
  for (const [k, v] of Object.entries(fields)) {
    if (v === undefined || v === '' || v === false) continue
    body.set(k, v === true ? 'on' : String(v))
  }
  const r = await fetch(url, {
    method: 'POST',
    credentials: 'include',
    headers: { Accept: 'application/json', 'Content-Type': 'application/x-www-form-urlencoded' },
    body,
  })
  let j: any = null
  try {
    j = await r.json()
  } catch {
    /* empty */
  }
  if (!r.ok || (j && j.ok === false)) {
    redirectIfGated(r.status)
    throw new ApiError(r.status, j?.error || 'request_failed', j?.message || `HTTP ${r.status}`)
  }
  return (j ?? { ok: true }) as MutationResult
}

/** JSON PUT/POST for the newer JSON APIs; throws ApiError with the server's message. */
export async function sendJSON<T>(method: 'PUT' | 'POST', url: string, payload?: unknown): Promise<T> {
  const r = await fetch(url, {
    method,
    credentials: 'include',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: payload === undefined ? undefined : JSON.stringify(payload),
  })
  let j: any = null
  try {
    j = await r.json()
  } catch {
    /* empty */
  }
  if (!r.ok) {
    redirectIfGated(r.status)
    throw new ApiError(r.status, 'request_failed', j?.message || j?.error || `HTTP ${r.status}`)
  }
  return j as T
}
