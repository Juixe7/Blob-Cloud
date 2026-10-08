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
              Set a new <br />
              password.
            </h1>
            <p className="mt-3 text-xs leading-relaxed text-zinc-400 font-sans max-w-sm">
              Choose a strong, secure password to protect your account and cloud files.
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
              <p className="text-xs font-medium text-zinc-300">Session Security</p>
              <p className="text-[11px] text-zinc-500 leading-snug">Updating your password safely signs out all other active sessions.</p>
            </div>
          </div>
        </div>
      </div>

      {/* RIGHT COLUMN: Password Reset (60% / 7 cols) */}
      <div className="md:col-span-7 flex flex-col justify-center items-center p-6 md:p-16 bg-arch-900">
        <div className="w-full max-w-sm">
          {/* Header */}
          <div className="mb-8">
            <h2 className="font-display text-2xl font-bold text-white tracking-tight">Set new password</h2>
            <p className="mt-1 text-sm text-zinc-400">Choose a new password for your account.</p>
          </div>

          {serverError && (
            <div className="mb-5">
              <Alert variant="error">{serverError}</Alert>
            </div>
          )}

          {successMessage ? (
            <div className="space-y-5">
              <Alert variant="success">{successMessage}</Alert>
              <p className="text-sm text-zinc-400 text-center">
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
                <label htmlFor="reset-password" className="mb-1.5 block text-xs font-medium text-zinc-300">
                  New password
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
                <label htmlFor="reset-confirm-password" className="mb-1.5 block text-xs font-medium text-zinc-300">
                  Confirm new password
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

          <p className="mt-8 text-center text-xs text-zinc-400">
            Back to{' '}
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

export default ResetPassword
