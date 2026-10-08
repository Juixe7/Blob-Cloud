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
      {/* LEFT COLUMN: Brand & Product Highlights (40% / 5 cols) */}
      <div className="hidden md:flex md:col-span-5 flex-col justify-between border-r border-arch-border bg-arch-950 p-8 lg:p-10 relative bg-arch-grid">
        <div>
          <div className="flex items-center gap-3">
            <div className="flex h-9 w-9 items-center justify-center rounded bg-amber-500 text-arch-950 font-display font-black text-lg shadow-sharp">
              B
            </div>
            <span className="font-display text-xl font-bold tracking-tight text-white">Blob-Cloud</span>
          </div>

          <div className="mt-10 lg:mt-12">
            <h1 className="font-display text-2xl lg:text-3xl font-extrabold tracking-tight text-white leading-tight">
              Quick and secure <br />
              password recovery.
            </h1>
            <p className="mt-3 text-xs leading-relaxed text-zinc-400 font-sans max-w-sm">
              We'll send you a secure verification link so you can safely reset your password and get back into your account.
            </p>
          </div>
        </div>

        {/* Feature Highlights */}
        <div className="border-t border-arch-border pt-5 space-y-3">
          <div className="flex items-start gap-2.5">
            <div className="mt-0.5 text-amber-400/90 shrink-0">
              <svg className="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <rect x="3" y="11" width="18" height="11" rx="2" ry="2" />
                <path d="M7 11V7a5 5 0 0 1 10 0v4" />
              </svg>
            </div>
            <div>
              <p className="text-xs font-medium text-zinc-300">Secure Reset</p>
              <p className="text-[11px] text-zinc-500 leading-snug">One-time encrypted recovery link sent directly to your inbox.</p>
            </div>
          </div>

          <div className="flex items-start gap-2.5">
            <div className="mt-0.5 text-amber-400/90 shrink-0">
              <svg className="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <circle cx="12" cy="12" r="10" />
                <polyline points="12 6 12 12 16 14" />
              </svg>
            </div>
            <div>
              <p className="text-xs font-medium text-zinc-300">Time-Limited</p>
              <p className="text-[11px] text-zinc-500 leading-snug">Links expire automatically after 15 minutes for your protection.</p>
            </div>
          </div>
        </div>
      </div>

      {/* RIGHT COLUMN: Recovery Engine (60% / 7 cols) */}
      <div className="md:col-span-7 flex flex-col justify-center items-center p-6 md:p-16 bg-arch-900">
        <div className="w-full max-w-sm">
          {/* Header */}
          <div className="mb-8">
            <h2 className="font-display text-2xl font-bold text-white tracking-tight">Reset your password</h2>
            <p className="mt-1 text-sm text-zinc-400">Enter your email address to receive reset instructions.</p>
          </div>

          {serverError && (
            <div className="mb-5">
              <Alert variant="error">{serverError}</Alert>
            </div>
          )}

          {successMessage ? (
            <div className="space-y-5">
              <Alert variant="success">{successMessage}</Alert>
              <p className="text-sm text-zinc-400 leading-relaxed text-center">
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
                <label htmlFor="forgot-email" className="mb-1.5 block text-xs font-medium text-zinc-300">
                  Email address
                </label>
                <Input
                  id="forgot-email"
                  type="email"
                  autoComplete="email"
                  placeholder="name@example.com"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  error={fieldError || undefined}
                  disabled={loading}
                />
              </div>

              <Button type="submit" loading={loading} className="mt-3">
                Send Reset Link
              </Button>
            </form>
          )}

          <p className="mt-8 text-center text-xs text-zinc-400">
            Remember your password?{' '}
            <Link
              to="/login"
              className="text-amber-400 hover:text-amber-300 font-medium transition-colors hover:underline"
            >
              Sign in
            </Link>
          </p>
        </div>
      </div>
    </main>
  )
}

export default ForgotPassword
