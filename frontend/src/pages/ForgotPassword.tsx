import { type FormEvent, useState } from 'react'
import { Link } from 'react-router-dom'
import { apiClient } from '../lib/api'
import { extractError } from '../context/AuthContext'
import { Button } from '../components/ui/Button'
import { Input } from '../components/ui/Input'
import { Alert } from '../components/ui/Alert'

function looksLikeEmail(value: string): boolean {
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value)
}

/**
 * Precision Architectural Forgot Password Page.
 * Asymmetrical 2-Column Editorial Layout.
 */
export function ForgotPassword() {
  const [email, setEmail] = useState('')
  const [fieldError, setFieldError] = useState<string | null>(null)
  const [serverError, setServerError] = useState<string | null>(null)
  const [successMessage, setSuccessMessage] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setFieldError(null)
    setServerError(null)

    const trimmedEmail = email.trim()
    if (!trimmedEmail) {
      setFieldError('Email is required.')
      return
    }
    if (!looksLikeEmail(trimmedEmail)) {
      setFieldError('Enter a valid email address.')
      return
    }

    setLoading(true)
    try {
      const res = await apiClient.post<{ message: string }>('/auth/forgot-password', { email: trimmedEmail })
      setSuccessMessage(res.data.message || 'Email sent')
    } catch (err) {
      setServerError(extractError(err, 'Failed to send recovery email. Please try again.'))
    } finally {
      setLoading(false)
    }
  }

  return (
    <main className="min-h-screen grid grid-cols-1 md:grid-cols-12 bg-arch-950 text-zinc-100 font-sans select-none">
      {/* LEFT COLUMN: Asymmetrical Brand & Technical Specs (40% / 5 cols) */}
      <div className="hidden md:flex md:col-span-5 flex-col justify-between border-r border-arch-border bg-arch-950 p-10 relative bg-arch-grid">
        <div>
          <div className="flex items-center gap-3">
            <div className="flex h-9 w-9 items-center justify-center rounded bg-amber-500 text-arch-950 font-display font-black text-lg shadow-sharp">
              B
            </div>
            <span className="font-display text-xl font-bold tracking-tight text-white">Blob-Cloud</span>
            <span className="font-mono text-[10px] font-semibold text-amber-400 bg-amber-500/10 border border-amber-500/30 rounded px-2 py-0.5 ml-auto">
              SECURITY
            </span>
          </div>

          <div className="mt-16">
            <h1 className="font-display text-3xl font-extrabold tracking-tight text-white leading-tight">
              Password Recovery <br />
              & Account Guard
            </h1>
            <p className="mt-4 text-xs leading-relaxed text-zinc-400 font-sans max-w-sm">
              Secure out-of-app recovery dispatching signed tokens via verified SMTP gateway with instant session invalidation.
            </p>
          </div>
        </div>

        {/* Technical Features Ticker (JetBrains Mono) */}
        <div className="border-t border-arch-border pt-6 font-mono text-[11px] space-y-2 text-zinc-500">
          <div className="flex items-center justify-between">
            <span className="text-zinc-400">DISPATCH GATEWAY</span>
            <span className="text-emerald-400 font-semibold">VERIFIED SMTP</span>
          </div>
          <div className="flex items-center justify-between">
            <span className="text-zinc-400">TOKEN EXPIRATION</span>
            <span className="text-zinc-300">15 MINUTES</span>
          </div>
        </div>
      </div>

      {/* RIGHT COLUMN: Recovery Engine (60% / 7 cols) */}
      <div className="md:col-span-7 flex flex-col justify-center items-center p-6 md:p-16 bg-arch-900">
        <div className="w-full max-w-sm">
          {/* Header */}
          <div className="mb-8">
            <h2 className="font-display text-xl font-bold text-white tracking-tight">Account Recovery</h2>
            <p className="mt-1 font-mono text-xs text-zinc-500">Enter your registered email address to receive a recovery token.</p>
          </div>

          {serverError && (
            <div className="mb-5">
              <Alert variant="error">{serverError}</Alert>
            </div>
          )}

          {successMessage ? (
            <div className="space-y-5">
              <Alert variant="success">{successMessage}</Alert>
              <p className="font-mono text-xs text-zinc-400 leading-relaxed text-center">
                Check your email inbox for instructions to reset your password.
              </p>
              <Link to="/login" className="block w-full">
                <Button variant="secondary" className="w-full">
                  Return to Sign In
                </Button>
              </Link>
            </div>
          ) : (
            <form onSubmit={handleSubmit} className="flex flex-col gap-4" noValidate>
              <div>
                <label htmlFor="forgot-email" className="mb-1 block font-mono text-[10px] font-semibold uppercase tracking-wider text-zinc-400">
                  EMAIL ADDRESS
                </label>
                <Input
                  id="forgot-email"
                  type="email"
                  autoComplete="email"
                  placeholder="operator@blobcloud.dev"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  error={fieldError || undefined}
                  disabled={loading}
                />
              </div>

              <Button type="submit" loading={loading} className="mt-3">
                Send Recovery Token
              </Button>
            </form>
          )}

          <p className="mt-8 text-center text-xs text-zinc-500 font-mono">
            Remembered your password?{' '}
            <Link
              to="/login"
              className="text-amber-400 hover:text-amber-300 font-semibold transition-colors underline underline-offset-4"
            >
              Sign In
            </Link>
          </p>
        </div>
      </div>
    </main>
  )
}

export default ForgotPassword
