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
              Cloud storage <br />
              built for speed and simplicity.
            </h1>
            <p className="mt-3 text-xs leading-relaxed text-zinc-400 font-sans max-w-sm">
              Get your private cloud drive up and running in seconds with generous storage, real-time sync, and seamless sharing.
            </p>
          </div>
        </div>

        {/* Feature Highlights */}
        <div className="border-t border-arch-border pt-5 space-y-3">
          <div className="flex items-start gap-2.5">
            <div className="mt-0.5 text-amber-400/90 shrink-0">
              <svg className="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M4.5 16.5c-1.5 1.26-2 5-2 5s3.74-.5 5-2c.71-.84.7-2.13-.09-2.91a2.18 2.18 0 0 0-2.91-.09z" />
                <path d="m12 15-3-3a22 22 0 0 1 2-3.95A12.88 12.88 0 0 1 22 2c0 2.72-.78 7.5-6 11a22.35 22.35 0 0 1-4 2z" />
              </svg>
            </div>
            <div>
              <p className="text-xs font-medium text-zinc-300">Zero Setup</p>
              <p className="text-[11px] text-zinc-500 leading-snug">Create your account and start uploading files in seconds.</p>
            </div>
          </div>

          <div className="flex items-start gap-2.5">
            <div className="mt-0.5 text-amber-400/90 shrink-0">
              <svg className="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2" />
                <circle cx="9" cy="7" r="4" />
                <path d="M22 21v-2a4 4 0 0 0-3-3.87" />
                <path d="M16 3.13a4 4 0 0 1 0 7.75" />
              </svg>
            </div>
            <div>
              <p className="text-xs font-medium text-zinc-300">Seamless Collaboration</p>
              <p className="text-[11px] text-zinc-500 leading-snug">Granular viewer/editor permissions and secure public links.</p>
            </div>
          </div>

          <div className="flex items-start gap-2.5">
            <div className="mt-0.5 text-amber-400/90 shrink-0">
              <svg className="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M21.5 2v6h-6M21.34 15.57a10 10 0 1 1-.57-8.38l5.67-5.67" />
              </svg>
            </div>
            <div>
              <p className="text-xs font-medium text-zinc-300">Real-Time Sync</p>
              <p className="text-[11px] text-zinc-500 leading-snug">Instant updates and delta sync across all your tabs and devices.</p>
            </div>
          </div>
        </div>
      </div>

      {/* RIGHT COLUMN: Registration Form (60% / 7 cols) */}
      <div className="md:col-span-7 flex flex-col justify-center items-center p-6 md:p-16 bg-arch-900">
        <div className="w-full max-w-sm">
          {/* Header */}
          <div className="mb-8">
            <h2 className="font-display text-2xl font-bold text-white tracking-tight">Create your account</h2>
            <p className="mt-1 text-sm text-zinc-400">Start storing and organizing your files today.</p>
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
            <span className="relative bg-arch-900 px-3 text-xs text-zinc-400">
              or register with email
            </span>
          </div>

          <form onSubmit={handleSubmit} className="flex flex-col gap-4" noValidate>
            <div>
              <label htmlFor="reg-email" className="mb-1.5 block text-xs font-medium text-zinc-300">
                Email address
              </label>
              <Input
                id="reg-email"
                type="email"
                autoComplete="email"
                placeholder="name@example.com"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                error={fieldErrors.email}
                disabled={loading}
              />
            </div>

            <div>
              <label htmlFor="reg-password" className="mb-1.5 block text-xs font-medium text-zinc-300">
                Password
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
              <label htmlFor="reg-confirm" className="mb-1.5 block text-xs font-medium text-zinc-300">
                Confirm password
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
              Create Account
            </Button>
          </form>

          <p className="mt-8 text-center text-xs text-zinc-400">
            Already have an account?{' '}
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

export default Register
