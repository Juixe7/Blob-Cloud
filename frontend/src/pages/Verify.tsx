import { useState, useEffect, type FormEvent } from 'react'
import { useNavigate, useSearchParams, Link } from 'react-router-dom'
import { apiClient } from '../lib/api'
import { useAuth, extractError } from '../context/AuthContext'
import { useToast } from '../components/Toast'
import { Alert } from '../components/ui/Alert'
import { Button } from '../components/ui/Button'
import { Spinner } from '../components/ui/Spinner'

/**
 * Precision Architectural Email Verification Page.
 * Asymmetrical 2-Column Editorial Layout.
 */
export function Verify() {
  const [searchParams] = useSearchParams()
  const userId = searchParams.get('user_id') || ''
  const email = searchParams.get('email') || ''

  const [code, setCode] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [resending, setResending] = useState(false)
  const [cooldown, setCooldown] = useState(0)
  const [error, setError] = useState<string | null>(null)
  const [infoMessage, setInfoMessage] = useState<string | null>(null)

  const { loginWithTokens } = useAuth()
  const navigate = useNavigate()
  const { push } = useToast()

  // Cooldown countdown timer for resend button
  useEffect(() => {
    if (cooldown <= 0) return
    const timer = setInterval(() => {
      setCooldown((prev) => prev - 1)
    }, 1000)
    return () => clearInterval(timer)
  }, [cooldown])

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (!code.trim() || code.length !== 6) {
      setError('Please enter the 6-digit verification code.')
      return
    }

    setSubmitting(true)
    setError(null)

    try {
      const res = await apiClient.post<{ token: string; refresh_token: string }>('/auth/verify', {
        user_id: userId,
        code: code.trim(),
      })

      loginWithTokens(res.data.token, res.data.refresh_token)
      push({ variant: 'success', message: 'Email verified successfully!' })
      navigate('/dashboard', { replace: true })
    } catch (err) {
      setError(extractError(err, 'Invalid or expired verification code.'))
    } finally {
      setSubmitting(false)
    }
  }

  const handleResend = async () => {
    if (cooldown > 0 || resending) return
    setResending(true)
    setError(null)
    setInfoMessage(null)

    try {
      await apiClient.post('/auth/resend-verification', {
        user_id: userId,
        email: email,
      })
      push({ variant: 'success', message: 'A new 6-digit verification code was sent!' })
      setInfoMessage('A new verification code has been dispatched.')
      setCooldown(30)
    } catch (err) {
      setError(extractError(err, 'Failed to resend verification code. Please try again.'))
    } finally {
      setResending(false)
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
              Verify your <br />
              email address.
            </h1>
            <p className="mt-3 text-xs leading-relaxed text-zinc-400 font-sans max-w-sm">
              Enter the 6-digit confirmation code sent to your email to activate your account and start storing files.
            </p>
          </div>
        </div>

        {/* Feature Highlights */}
        <div className="border-t border-arch-border pt-5 space-y-3">
          <div className="flex items-start gap-2.5">
            <div className="mt-0.5 text-amber-400/90 shrink-0">
              <svg className="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <rect width="20" height="16" x="2" y="4" rx="2" />
                <path d="m22 7-8.97 5.7a1.94 1.94 0 0 1-2.06 0L2 7" />
              </svg>
            </div>
            <div>
              <p className="text-xs font-medium text-zinc-300">One-Time Code</p>
              <p className="text-[11px] text-zinc-500 leading-snug">Your code is valid for 15 minutes. Check your spam folder if you don't see it.</p>
            </div>
          </div>
        </div>
      </div>

      {/* RIGHT COLUMN: Verification (60% / 7 cols) */}
      <div className="md:col-span-7 flex flex-col justify-center items-center p-6 md:p-16 bg-arch-900">
        <div className="w-full max-w-sm">
          {/* Header */}
          <div className="mb-8">
            <h2 className="font-display text-2xl font-bold text-white tracking-tight">Verify your email</h2>
            <p className="mt-1 text-sm text-zinc-400">
              Sent to: {email ? <span className="text-amber-400 font-medium">{email}</span> : 'your registered email'}
            </p>
          </div>

          {error && <div className="mb-4"><Alert variant="error">{error}</Alert></div>}
          {infoMessage && <div className="mb-4"><Alert variant="info">{infoMessage}</Alert></div>}

          <form onSubmit={handleSubmit} className="flex flex-col gap-4" noValidate>
            <div>
              <label htmlFor="code" className="block text-xs font-medium text-zinc-300 mb-2">
                Verification code
              </label>
              <input
                id="code"
                type="text"
                maxLength={6}
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))}
                placeholder="123456"
                autoFocus
                className="w-full rounded border border-arch-border bg-arch-950 px-4 py-3 text-center font-mono text-2xl font-bold tracking-[0.4em] text-amber-400 placeholder:text-zinc-700 placeholder:tracking-normal focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500/30 transition-colors"
              />
            </div>

            <Button
              type="submit"
              disabled={submitting || code.length !== 6}
              className="mt-2"
            >
              {submitting ? <Spinner size={16} className="mx-auto" /> : 'Confirm & Continue'}
            </Button>
          </form>

          {/* Resend & Return controls */}
          <div className="mt-6 pt-4 border-t border-arch-border flex flex-col items-center gap-3 text-xs text-zinc-400">
            <div className="flex items-center gap-1.5">
              <span>Didn&apos;t receive code?</span>
              <button
                type="button"
                onClick={handleResend}
                disabled={cooldown > 0 || resending}
                className="font-medium text-amber-400 hover:text-amber-300 hover:underline disabled:opacity-50 disabled:no-underline transition-colors cursor-pointer"
              >
                {resending
                  ? 'Resending…'
                  : cooldown > 0
                  ? `Resend in ${cooldown}s`
                  : 'Resend code'}
              </button>
            </div>

            <Link to="/login" className="text-zinc-400 hover:text-zinc-200 transition-colors hover:underline">
              &larr; Return to sign in
            </Link>
          </div>
        </div>
      </div>
    </main>
  )
}

export default Verify
