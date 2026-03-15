import { forwardRef, type InputHTMLAttributes } from 'react'

interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  /** Optional validation error rendered below the input. */
  error?: string
  /** Use monospace font for technical codes/tokens. */
  mono?: boolean
}

/**
 * Precision Architectural Input.
 * High-density text input with 1px border rules and amber focus ring.
 */
export const Input = forwardRef<HTMLInputElement, InputProps>(
  ({ error, mono = false, id, className = '', ...rest }, ref) => {
    const errorId = error && id ? `${id}-error` : undefined

    return (
      <div className="flex flex-col gap-1">
        <input
          ref={ref}
          id={id}
          aria-invalid={error ? 'true' : undefined}
          aria-describedby={errorId}
          className={`w-full rounded border bg-arch-950 px-3 py-2 text-xs text-zinc-100 placeholder-zinc-500 transition-colors duration-150 focus:outline-none ${
            mono ? 'font-mono tracking-wide' : 'font-sans'
          } ${
            error
              ? 'border-rose-500/80 focus:border-rose-500 focus:ring-1 focus:ring-rose-500/50'
              : 'border-arch-border hover:border-arch-700 focus:border-amber-500/90 focus:ring-1 focus:ring-amber-500/30'
          } ${className}`}
          {...rest}
        />
        {error && (
          <p id={errorId} className="text-[11px] font-mono text-rose-400" role="alert">
            {error}
          </p>
        )}
      </div>
    )
  },
)

Input.displayName = 'Input'

export default Input
