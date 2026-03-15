import type { ButtonHTMLAttributes, ReactNode } from 'react'
import { Spinner } from './Spinner'

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  /** Render a loading spinner and disable interaction. */
  loading?: boolean
  /** Style variant. */
  variant?: 'primary' | 'secondary' | 'danger' | 'ghost'
  children: ReactNode
}

/**
 * Precision Architectural Button.
 * High-contrast, tactile press feedback with crisp 1px borders and Electric Amber primary accents.
 */
export function Button({
  loading = false,
  variant = 'primary',
  children,
  disabled,
  className = '',
  ...rest
}: ButtonProps) {
  const base =
    'relative inline-flex items-center justify-center gap-2 rounded-md px-4 py-2 text-xs font-semibold tracking-wide transition-all duration-150 ease-out focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-500 disabled:pointer-events-none disabled:opacity-40 active:scale-[0.98] select-none cursor-pointer'

  const variants: Record<string, string> = {
    primary:
      'bg-amber-500 text-arch-950 hover:bg-amber-400 font-bold shadow-sharp border border-amber-400/80',
    secondary:
      'border border-arch-700 bg-arch-900 text-zinc-200 hover:bg-arch-850 hover:border-arch-700 hover:text-white',
    danger:
      'border border-rose-900/60 bg-rose-950/40 text-rose-300 hover:bg-rose-900/50 hover:border-rose-700',
    ghost:
      'text-zinc-400 hover:text-zinc-100 hover:bg-arch-850/80',
  }

  return (
    <button
      disabled={disabled || loading}
      className={`${base} ${variants[variant] || variants.primary} ${className}`}
      {...rest}
    >
      {loading && <Spinner size={14} className="text-current" />}
      {children}
    </button>
  )
}

export default Button
