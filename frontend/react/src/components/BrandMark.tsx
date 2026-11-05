import { cn } from '@/lib/utils'

interface BrandMarkProps {
  className?: string
  size?: number
}

/**
 * Spendmux mark: three signals fanning in to one line — many provider keys,
 * one URL. Single colour (currentColor) so it follows every theme.
 */
export function BrandMark({ className, size = 22 }: BrandMarkProps) {
  return (
    <span
      className={cn(
        'inline-flex items-center justify-center rounded-md bg-primary text-primary-foreground shrink-0',
        className,
      )}
      style={{ width: size + 6, height: size + 6 }}
      aria-hidden
    >
      <MuxGlyph size={size} />
    </span>
  )
}

/** The bare glyph, for places that supply their own background. */
export function MuxGlyph({ size = 22, strokeWidth = 2.6 }: { size?: number; strokeWidth?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" fill="none" stroke="currentColor" strokeWidth={strokeWidth} strokeLinecap="round" strokeLinejoin="round">
      <path d="M5 8l10 8M5 16h10M5 24l10-8M15 16h9" />
      <circle cx="26.5" cy="16" r="2.4" fill="currentColor" stroke="none" />
    </svg>
  )
}
