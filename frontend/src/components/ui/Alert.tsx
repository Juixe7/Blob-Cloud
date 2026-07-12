import type { ReactNode } from 'react'

interface AlertProps {
  /** 'error' for red, 'info' for muted blue. */
  variant?: 'error' | 'info'
  children: ReactNode
}

/**
 * Compact alert block used for server-side error messages below auth forms.
 */
export function Alert({ variant = 'error', children }: AlertProps) {
  const styles: Record<string, string> = {
    error: 'border-red-500/40 bg-red-500/10 text-red-300',
    info: 'border-blue-500/40 bg-blue-500/10 text-blue-300',
  }

  return (
    <div
      role="alert"
      aria-live="polite"
      className={`animate-fade-in rounded-lg border px-3.5 py-2.5 text-sm ${styles[variant]}`}
    >
      {children}
    </div>
  )
}

export default Alert
