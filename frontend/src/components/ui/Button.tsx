import type { ButtonHTMLAttributes, ReactNode } from 'react'
import { Spinner } from './Spinner'

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  /** Render a loading spinner and disable interaction. */
  loading?: boolean
  /** Narrow style for secondary actions. */
  variant?: 'primary' | 'secondary'
  children: ReactNode
}

/**
 * Primary / secondary button following the Linear/Vercel dark palette.
 * Focus states glow violet; loading state disables and shows a spinner.
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
    'relative inline-flex items-center justify-center gap-2 rounded-lg px-5 py-2.5 text-sm font-medium transition-all duration-200 ease-in-out focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-violet-500/60 focus-visible:ring-offset-2 focus-visible:ring-offset-zinc-950 disabled:pointer-events-none disabled:opacity-50'

  const variants: Record<string, string> = {
    primary:
      'bg-violet-600 text-zinc-50 hover:bg-violet-500 active:bg-violet-700 shadow-sm',
    secondary:
      'border border-zinc-800 bg-zinc-900 text-zinc-300 hover:bg-zinc-800 hover:text-zinc-50',
  }

  return (
    <button
      disabled={disabled || loading}
      className={`${base} ${variants[variant]} ${className}`}
      {...rest}
    >
      {loading && <Spinner size={16} />}
      {children}
    </button>
  )
}

export default Button
