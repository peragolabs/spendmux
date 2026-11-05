/**
 * Theme registry.
 *
 * Themes are pure CSS variable packs living in ../index.css.
 * Switching is a single attribute on <html>. Persisted via localStorage.
 */

export type ThemeName = 'forest' | 'aurora'

export const AVAILABLE_THEMES: { id: ThemeName; label: string; hint: string }[] = [
  { id: 'forest',     label: 'Forest',     hint: 'Paper + deep greens' },
  { id: 'aurora',     label: 'Aurora',     hint: 'Glass + neon accents' },
]

const STORAGE_KEY = 'keller:theme'
const DEFAULT: ThemeName = 'forest'

function isTheme(v: unknown): v is ThemeName {
  return typeof v === 'string' && AVAILABLE_THEMES.some(t => t.id === v)
}

export function applyTheme(name: ThemeName) {
  document.documentElement.setAttribute('data-theme', name)
  try { localStorage.setItem(STORAGE_KEY, name) } catch { /* private mode */ }
}

export function getStoredTheme(): ThemeName {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (isTheme(stored)) return stored
  } catch { /* ignore */ }
  return DEFAULT
}

export function applyStoredTheme() {
  applyTheme(getStoredTheme())
}
