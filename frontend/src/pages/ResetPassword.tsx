import { type FormEvent, useEffect, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { apiClient } from '../lib/api'
import { extractError } from '../context/AuthContext'
import { useToast } from '../components/Toast'
import { Button } from '../components/ui/Button'
import { Input } from '../components/ui/Input'
import { Alert } from '../components/ui/Alert'

/**
 * Precision Architectural Reset Password Page.
 * Asymmetrical 2-Column Editorial Layout.
 */
export function ResetPassword() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const token = searchParams.get('token')
  const { push } = useToast()

  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [serverError, setServerError] = useState<string | null>(null)
  const [successMessage, setSuccessMessage] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (!token) {
      navigate('/login', { replace: true })
    }
  }, [token, navigate])

  const validate = (): boolean => {
    const errors: Record<string, string> = {}
    if (!password) {
      errors.password = 'New password is required.'
    } else if (password.length < 8) {
      errors.password = 'Password must be at least 8 characters.'
    }

    if (!confirmPassword) {
      errors.confirmPassword = 'Please confirm your new password.'
    } else if (password !== confirmPassword) {
      errors.confirmPassword = 'Passwords do not match.'
    }

    setFieldErrors(errors)
    return Object.keys(errors).length === 0
  }

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setServerError(null)
    if (!validate() || !token) return

    setLoading(true)
    try {
      const res = await apiClient.post<{ message: string }>('/auth/reset-password', {
        token,
        password,
      })
      const msg = res.data.message || 'Password has been successfully reset.'
      setSuccessMessage(msg)
      push({ variant: 'success', message: msg })
      setTimeout(() => {
        navigate('/login', { replace: true })
      }, 2000)
    } catch (err) {
      setServerError(extractError(err, 'Failed to reset password. Token may be invalid or expired.'))
    } finally {
      setLoading(false)
    }
  }

  if (!token) return null

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
              NEW CREDENTIALS
            </span>
          </div>

          <div className="mt-16">
            <h1 className="font-display text-3xl font-extrabold tracking-tight text-white leading-tight">
              Update Account <br />
              Credentials
            </h1>
            <p className="mt-4 text-xs leading-relaxed text-zinc-400 font-sans max-w-sm">
              Establishing new password credentials instantly invalidates all existing active device sessions across the network.
            </p>
          </div>
        </div>

        {/* Technical Features Ticker (JetBrains Mono) */}
        <div className="border-t border-arch-border pt-6 font-mono text-[11px] space-y-2 text-zinc-500">
          <div className="flex items-center justify-between">
            <span className="text-zinc-400">SESSION BLACKLIST</span>
            <span className="text-amber-400 font-semibold">AUTOMATIC REVOCATION</span>
          </div>
          <div className="flex items-center justify-between">
            <span className="text-zinc-400">HASHING ALGORITHM</span>
            <span className="text-zinc-300">BCRYPT (COST 10)</span>
          </div>
        </div>
      </div>

      {/* RIGHT COLUMN: Password Reset Engine (60% / 7 cols) */}
      <div className="md:col-span-7 flex flex-col justify-center items-center p-6 md:p-16 bg-arch-900">
        <div className="w-full max-w-sm">
          {/* Header */}
          <div className="mb-8">
            <h2 className="font-display text-xl font-bold text-white tracking-tight">Set New Password</h2>
            <p className="mt-1 font-mono text-xs text-zinc-500">Choose a new password for your account.</p>
          </div>

          {serverError && (
            <div className="mb-5">
              <Alert variant="error">{serverError}</Alert>
            </div>
          )}

          {successMessage ? (
            <div className="space-y-5">
              <Alert variant="success">{successMessage}</Alert>
              <p className="font-mono text-xs text-zinc-400 text-center">
                Redirecting to sign-in page...
              </p>
              <Link to="/login" className="block w-full">
                <Button variant="primary" className="w-full">
                  Go to Sign In
                </Button>
              </Link>
            </div>
          ) : (
            <form onSubmit={handleSubmit} className="flex flex-col gap-4" noValidate>
              <div>
                <label htmlFor="reset-password" className="mb-1 block font-mono text-[10px] font-semibold uppercase tracking-wider text-zinc-400">
                  NEW PASSWORD
                </label>
                <Input
                  id="reset-password"
                  type="password"
                  autoComplete="new-password"
                  placeholder="••••••••••••"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  error={fieldErrors.password}
                  disabled={loading}
                />
              </div>

              <div>
                <label htmlFor="reset-confirm-password" className="mb-1 block font-mono text-[10px] font-semibold uppercase tracking-wider text-zinc-400">
                  CONFIRM NEW PASSWORD
                </label>
                <Input
                  id="reset-confirm-password"
                  type="password"
                  autoComplete="new-password"
                  placeholder="••••••••••••"
                  value={confirmPassword}
                  onChange={(e) => setConfirmPassword(e.target.value)}
                  error={fieldErrors.confirmPassword}
                  disabled={loading}
                />
              </div>

              <Button type="submit" loading={loading} className="mt-3">
                Update Password
              </Button>
            </form>
          )}

          <p className="mt-8 text-center text-xs text-zinc-500 font-mono">
            Back to{' '}
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

export default ResetPassword
