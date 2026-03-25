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
      {/* LEFT COLUMN: Asymmetrical Brand & Technical Specs (40% / 5 cols) */}
      <div className="hidden md:flex md:col-span-5 flex-col justify-between border-r border-arch-border bg-arch-950 p-10 relative bg-arch-grid">
        <div>
          <div className="flex items-center gap-3">
            <div className="flex h-9 w-9 items-center justify-center rounded bg-amber-500 text-arch-950 font-display font-black text-lg shadow-sharp">
              B
            </div>
            <span className="font-display text-xl font-bold tracking-tight text-white">Blob-Cloud</span>
            <span className="font-mono text-[10px] font-semibold text-amber-400 bg-amber-500/10 border border-amber-500/30 rounded px-2 py-0.5 ml-auto">
              EMAIL VERIFY
            </span>
          </div>

          <div className="mt-16">
            <h1 className="font-display text-3xl font-extrabold tracking-tight text-white leading-tight">
              Multi-Factor <br />
              Activation Gate
            </h1>
            <p className="mt-4 text-xs leading-relaxed text-zinc-400 font-sans max-w-sm">
              Cryptographically generated 6-digit verification token dispatched to your inbox to activate your workspace.
            </p>
          </div>
        </div>

        {/* Technical Features Ticker (JetBrains Mono) */}
        <div className="border-t border-arch-border pt-6 font-mono text-[11px] space-y-2 text-zinc-500">
          <div className="flex items-center justify-between">
            <span className="text-zinc-400">ACTIVATION CODE</span>
            <span className="text-amber-400 font-semibold">6-DIGIT NUMERIC</span>
          </div>
          <div className="flex items-center justify-between">
            <span className="text-zinc-400">EXPIRATION TTL</span>
            <span className="text-zinc-300">15 MINUTES</span>
          </div>
        </div>
      </div>

      {/* RIGHT COLUMN: Verification Engine (60% / 7 cols) */}
      <div className="md:col-span-7 flex flex-col justify-center items-center p-6 md:p-16 bg-arch-900">
        <div className="w-full max-w-sm">
          {/* Header */}
          <div className="mb-8">
            <h2 className="font-display text-xl font-bold text-white tracking-tight">Verify Your Email</h2>
            <p className="mt-1 font-mono text-xs text-zinc-500">
              Dispatched to: {email ? <span className="text-amber-400 font-semibold">{email}</span> : 'registered email'}
            </p>
          </div>

          {error && <div className="mb-4"><Alert variant="error">{error}</Alert></div>}
          {infoMessage && <div className="mb-4"><Alert variant="info">{infoMessage}</Alert></div>}

          <form onSubmit={handleSubmit} className="flex flex-col gap-4" noValidate>
            <div>
              <label htmlFor="code" className="block font-mono text-[10px] font-semibold uppercase tracking-wider text-zinc-400 mb-2">
                VERIFICATION CODE
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
              {submitting ? <Spinner size={16} className="mx-auto" /> : 'Complete Activation'}
            </Button>
          </form>

          {/* Resend & Return controls */}
          <div className="mt-6 pt-4 border-t border-arch-border flex flex-col items-center gap-3 font-mono text-xs text-zinc-500">
            <div className="flex items-center gap-1.5">
              <span>Didn&apos;t receive code?</span>
              <button
                type="button"
                onClick={handleResend}
                disabled={cooldown > 0 || resending}
                className="font-semibold text-amber-400 hover:text-amber-300 hover:underline disabled:opacity-50 disabled:no-underline transition-colors"
              >
                {resending
                  ? 'Resending…'
                  : cooldown > 0
                  ? `Resend in ${cooldown}s`
                  : 'Resend Code'}
              </button>
            </div>

            <Link to="/login" className="text-zinc-400 hover:text-zinc-200 transition-colors">
              &larr; Return to Sign In
            </Link>
          </div>
        </div>
      </div>
    </main>
  )
}

export default Verify
