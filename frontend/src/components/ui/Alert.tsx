import type { ReactNode } from 'react'

interface AlertProps {
  /** Visual tone of the alert. */
  variant?: 'error' | 'info' | 'success' | 'warning'
  children: ReactNode
}

/**
 * Precision Architectural Alert block with solid left accent border indicator.
 */
export function Alert({ variant = 'error', children }: AlertProps) {
  const styles: Record<string, string> = {
    error: 'border-l-4 border-l-rose-500 border-arch-border bg-rose-950/20 text-rose-300',
    info: 'border-l-4 border-l-cyan-500 border-arch-border bg-cyan-950/20 text-cyan-300',
    success: 'border-l-4 border-l-emerald-500 border-arch-border bg-emerald-950/20 text-emerald-300',
    warning: 'border-l-4 border-l-amber-500 border-arch-border bg-amber-950/20 text-amber-300',
  }

  return (
    <div
      role="alert"
      aria-live="polite"
      className={`animate-fade-in rounded-r border border-l-0 px-3.5 py-2.5 text-xs leading-relaxed font-sans ${styles[variant]}`}
    >
      {children}
    </div>
  )
}

export default Alert
