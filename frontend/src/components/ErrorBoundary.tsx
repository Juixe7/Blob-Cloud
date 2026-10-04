import { Component, type ErrorInfo, type ReactNode } from 'react'

interface Props {
  children: ReactNode
  fallback?: ReactNode
}

interface State {
  hasError: boolean
  error: Error | null
}

export class ErrorBoundary extends Component<Props, State> {
  constructor(props: Props) {
    super(props)
    this.state = { hasError: false, error: null }
  }

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error }
  }

  componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    // eslint-disable-next-line no-console
    console.error('[ErrorBoundary] Caught render exception:', error, errorInfo)
  }

  handleReload = () => {
    window.location.reload()
  }

  render() {
    if (this.state.hasError) {
      if (this.props.fallback) {
        return this.props.fallback
      }

      return (
        <div className="flex h-screen w-screen flex-col items-center justify-center bg-zinc-950 p-6 text-zinc-100">
          <div className="w-full max-w-md rounded-2xl border border-zinc-800 bg-zinc-900/80 p-8 shadow-2xl backdrop-blur-md text-center">
            <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-full bg-red-500/10 text-red-400">
              <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <circle cx="12" cy="12" r="10" />
                <line x1="12" y1="8" x2="12" y2="12" />
                <line x1="12" y1="16" x2="12.01" y2="16" />
              </svg>
            </div>
            <h2 className="text-lg font-semibold text-zinc-100">Application Error</h2>
            <p className="mt-2 text-xs text-zinc-400 leading-relaxed">
              An unexpected render error occurred. Your files and uploads remain completely safe on the server.
            </p>
            {this.state.error?.message && (
              <pre className="mt-4 max-h-32 overflow-auto rounded bg-zinc-950 p-3 text-left font-mono text-[11px] text-red-400 border border-zinc-800">
                {this.state.error.message}
              </pre>
            )}
            <button
              onClick={this.handleReload}
              className="mt-6 w-full rounded-xl bg-amber-500 px-4 py-2.5 text-xs font-semibold text-zinc-950 shadow-md hover:bg-amber-400 active:scale-95 transition-all"
            >
              Reload Dashboard
            </button>
          </div>
        </div>
      )
    }

    return this.props.children
  }
}
