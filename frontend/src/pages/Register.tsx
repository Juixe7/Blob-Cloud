import { type FormEvent, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { GoogleLogin, type CredentialResponse } from '@react-oauth/google'
import { useAuth, extractError } from '../context/AuthContext'
import { apiClient } from '../lib/api'
import { useToast } from '../components/Toast'
import { Button } from '../components/ui/Button'
import { Input } from '../components/ui/Input'
import { Alert } from '../components/ui/Alert'

function looksLikeEmail(value: string): boolean {
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value)
}

/**
 * Asymmetrical Editorial Register Page.
 * Left Column (40% width): Brand Showcase & Architectural Specs.
 * Right Column (60% width): Registration Form Engine.
 */
export function Register() {
  const navigate = useNavigate()
  const { register, loginWithTokens } = useAuth()
  const { push } = useToast()

  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [serverError, setServerError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  const validate = (): boolean => {
    const errors: Record<string, string> = {}
    if (!email.trim()) errors.email = 'Email is required.'
    else if (!looksLikeEmail(email)) errors.email = 'Enter a valid email address.'
    if (!password) errors.password = 'Password is required.'
    else if (password.length < 8) errors.password = 'Password must be at least 8 characters.'
    if (!confirmPassword) errors.confirmPassword = 'Please confirm your password.'
    else if (password !== confirmPassword) errors.confirmPassword = 'Passwords do not match.'
    setFieldErrors(errors)
    return Object.keys(errors).length === 0
  }

  const handleGoogleSuccess = async (credentialResponse: CredentialResponse) => {
    if (!credentialResponse.credential) return
    setLoading(true)
    setServerError(null)
    try {
      const res = await apiClient.post<{ token: string; refresh_token: string }>('/auth/google', {
        id_token: credentialResponse.credential,
      })
      loginWithTokens(res.data.token, res.data.refresh_token)
      push({ variant: 'success', message: 'Signed in with Google!' })
      navigate('/dashboard', { replace: true })
    } catch (err) {
      setServerError(extractError(err, 'Google OAuth authentication failed.'))
    } finally {
      setLoading(false)
    }
  }

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setServerError(null)
    if (!validate()) return

    setLoading(true)
    const result = await register(email, password)
    setLoading(false)

    if (result.verificationRequired && result.userId) {
      const emailParam = result.email ? `&email=${encodeURIComponent(result.email)}` : ''
      navigate(`/verify?user_id=${result.userId}${emailParam}`)
    } else if (result.ok) {
      navigate('/dashboard', { replace: true })
    } else {
      setServerError(result.error)
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
              NEW OPERATOR
            </span>
          </div>

          <div className="mt-16">
            <h1 className="font-display text-3xl font-extrabold tracking-tight text-white leading-tight">
              Create Your <br />
              Secure Workspace
            </h1>
            <p className="mt-4 text-xs leading-relaxed text-zinc-400 font-sans max-w-sm">
              Provision an encrypted cloud drive featuring instant block deduplication, background workers, and real-time tab sync.
            </p>
          </div>
        </div>

        {/* Technical Features Ticker (JetBrains Mono) */}
        <div className="border-t border-arch-border pt-6 font-mono text-[11px] space-y-2 text-zinc-500">
          <div className="flex items-center justify-between">
            <span className="text-zinc-400">STORAGE DEPLOYMENT</span>
            <span className="text-amber-400 font-semibold">AUTOMATED</span>
          </div>
          <div className="flex items-center justify-between">
            <span className="text-zinc-400">SESSION BLACKLISTING</span>
            <span className="text-zinc-300">ACTIVE</span>
          </div>
          <div className="flex items-center justify-between">
            <span className="text-zinc-400">CONCURRENCY SECTOR</span>
            <span className="text-zinc-300">BOUNDED (3x)</span>
          </div>
        </div>
      </div>

      {/* RIGHT COLUMN: Registration Form Engine (60% / 7 cols) */}
      <div className="md:col-span-7 flex flex-col justify-center items-center p-6 md:p-16 bg-arch-900">
        <div className="w-full max-w-sm">
          {/* Header */}
          <div className="mb-8">
            <h2 className="font-display text-xl font-bold text-white tracking-tight">Register Account</h2>
            <p className="mt-1 font-mono text-xs text-zinc-500">Set up your credentials to deploy your workspace.</p>
          </div>

          {/* Server error */}
          {serverError && (
            <div className="mb-5">
              <Alert variant="error">{serverError}</Alert>
            </div>
          )}

          <div className="mb-6 flex justify-center">
            <GoogleLogin
              onSuccess={handleGoogleSuccess}
              onError={() => setServerError('Google Sign-Up failed.')}
              theme="filled_black"
              shape="rectangular"
              text="signup_with"
            />
          </div>

          <div className="relative mb-6 flex items-center justify-center">
            <div className="absolute inset-0 flex items-center">
              <div className="w-full border-t border-arch-border" />
            </div>
            <span className="relative bg-arch-900 px-3 font-mono text-[10px] uppercase tracking-widest text-zinc-500">
              OR REGISTER WITH EMAIL
            </span>
          </div>

          <form onSubmit={handleSubmit} className="flex flex-col gap-4" noValidate>
            <div>
              <label htmlFor="reg-email" className="mb-1 block font-mono text-[10px] font-semibold uppercase tracking-wider text-zinc-400">
                EMAIL ADDRESS
              </label>
              <Input
                id="reg-email"
                type="email"
                autoComplete="email"
                placeholder="operator@blobcloud.dev"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                error={fieldErrors.email}
                disabled={loading}
              />
            </div>

            <div>
              <label htmlFor="reg-password" className="mb-1 block font-mono text-[10px] font-semibold uppercase tracking-wider text-zinc-400">
                PASSWORD
              </label>
              <Input
                id="reg-password"
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
              <label htmlFor="reg-confirm" className="mb-1 block font-mono text-[10px] font-semibold uppercase tracking-wider text-zinc-400">
                CONFIRM PASSWORD
              </label>
              <Input
                id="reg-confirm"
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
              Register Account
            </Button>
          </form>

          <p className="mt-8 text-center text-xs text-zinc-500 font-mono">
            Already registered?{' '}
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

export default Register
