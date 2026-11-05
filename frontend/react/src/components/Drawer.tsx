import { type ReactNode } from 'react'
import { ChevronDown } from 'lucide-react'

import { cn } from '@/lib/utils'

/**
 * Native <details> drawer: keyboard accessible, no state to break, and the
 * open/closed state survives re-renders. Used for "＋ Add …" forms so pages
 * show their lists first.
 */
export function Drawer({
  summary,
  children,
  defaultOpen,
  nested,
  className,
}: {
  summary: ReactNode
  children: ReactNode
  defaultOpen?: boolean
  nested?: boolean
  className?: string
}) {
  return (
    <details
      open={defaultOpen}
      className={cn(
        'group rounded-xl border',
        nested ? 'border-border bg-muted/40' : 'border-dashed border-border',
        className,
      )}
    >
      <summary
        className={cn(
          'flex cursor-pointer select-none list-none items-center gap-2 px-4 py-2.5 text-sm font-semibold',
          nested ? 'text-foreground' : 'text-primary',
          'group-open:border-b group-open:border-dashed group-open:border-border',
        )}
      >
        {summary}
        <ChevronDown className="ml-auto h-4 w-4 text-muted-foreground transition-transform group-open:rotate-180" />
      </summary>
      <div className="px-4 pb-4 pt-3">{children}</div>
    </details>
  )
}

/** A card-sized collapsed section (e.g. "Quick start", "Why this is different"). */
export function DrawerCard({ title, children }: { title: ReactNode; children: ReactNode }) {
  return (
    <details className="group rounded-2xl border border-border bg-card shadow-sm">
      <summary className="flex cursor-pointer select-none list-none items-center gap-2 px-6 py-4">
        <h2 className="font-display text-lg">{title}</h2>
        <ChevronDown className="ml-auto h-4 w-4 text-muted-foreground transition-transform group-open:rotate-180" />
      </summary>
      <div className="px-6 pb-6">{children}</div>
    </details>
  )
}
