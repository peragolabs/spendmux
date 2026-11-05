import { useEffect, useState } from 'react'
import { Sparkles, Trees } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { applyTheme, getStoredTheme, type ThemeName } from '@/theme'

/** A deliberate two-theme toggle: calm Forest by default, expressive Aurora on demand. */
export function ThemeSwitcher() {
  const [theme, setTheme] = useState<ThemeName>(() => getStoredTheme())

  useEffect(() => {
    const sync = (e: StorageEvent) => {
      if (e.key === 'keller:theme' && (e.newValue === 'forest' || e.newValue === 'aurora')) {
        setTheme(e.newValue)
        applyTheme(e.newValue)
      }
    }
    window.addEventListener('storage', sync)
    return () => window.removeEventListener('storage', sync)
  }, [])

  const next = theme === 'forest' ? 'aurora' : 'forest'
  const Icon = next === 'aurora' ? Sparkles : Trees
  const label = next === 'aurora' ? 'Dark theme' : 'Light theme'

  return (
    <Button
      variant="ghost"
      size="sm"
      className="gap-2"
      onClick={() => { setTheme(next); applyTheme(next) }}
      aria-label={`Switch to ${next === 'aurora' ? 'Aurora dark' : 'Forest light'} theme`}
      data-testid="theme-switcher-trigger"
    >
      <Icon className="h-4 w-4" />
      <span className="hidden sm:inline">{label}</span>
    </Button>
  )
}
