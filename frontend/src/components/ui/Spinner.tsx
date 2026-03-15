interface SpinnerProps {
  /** Pixel size of the spinner. */
  size?: number
  className?: string
}

/** Small inline spinner used inside buttons / loaders. */
export function Spinner({ size = 16, className = '' }: SpinnerProps) {
  return (
    <svg
      className={`animate-spin ${className}`}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      aria-hidden="true"
    >
      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
      <path
        className="opacity-90"
        fill="currentColor"
        d="M4 12a8 8 0 0 1 8-8V0C5.373 0 0 5.373 0 12h4z"
      />
    </svg>
  )
}

/** Full-page minimal loader shown by PrivateRoute during auth bootstrap. */
export function FullPageSpinner() {
  return (
    <div
      role="status"
      aria-live="polite"
      className="flex min-h-screen items-center justify-center bg-zinc-950"
    >
      <div className="flex flex-col items-center gap-4">
        <Spinner size={28} className="text-violet-500" />
        <p className="text-sm text-zinc-500">Loading Blob-Cloud…</p>
      </div>
    </div>
  )
}

export default Spinner
