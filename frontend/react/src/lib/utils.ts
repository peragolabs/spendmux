import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

/**
 * Tailwind-aware class composer used across the app.
 * `cn('p-2', condition && 'text-red-500')` merges conflicting classes.
 */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}
