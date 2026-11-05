/** Origin users put in base_url (no /v1). The server writes it on #app from
 * SPENDMUX_API_URL; without it (or in Vite dev) it is this page's origin. */
export function apiBase(): string {
  if (typeof document === 'undefined') return ''
  return document.getElementById('app')?.dataset.apiBase || window.location.origin
}
