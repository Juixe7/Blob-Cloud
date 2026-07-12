import { forwardRef, type InputHTMLAttributes } from 'react'

interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  /** Optional validation error rendered below the input. */
  error?: string
}

/**
 * Styled text input with violet focus ring and optional inline error.
 * Sets aria-invalid + aria-describedby automatically when an error is given.
 */
export const Input = forwardRef<HTMLInputElement, InputProps>(
  ({ error, id, className = '', ...rest }, ref) => {
    const errorId = error && id ? `${id}-error` : undefined

    return (
      <div className="flex flex-col gap-1.5">
        <input
          ref={ref}
          id={id}
          aria-invalid={error ? 'true' : undefined}
          aria-describedby={errorId}
          className={`w-full rounded-lg border bg-zinc-900/50 px-3.5 py-2.5 text-sm text-zinc-50 placeholder-zinc-500 transition-all duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-violet-500/20 focus:border-violet-500/60 ${
            error
              ? 'border-red-500/60 focus:ring-red-500/20'
              : 'border-zinc-800 hover:border-zinc-700'
          } ${className}`}
          {...rest}
        />
        {error && (
          <p id={errorId} className="text-xs text-red-400" role="alert">
            {error}
          </p>
        )}
      </div>
    )
  },
)

Input.displayName = 'Input'

export default Input
