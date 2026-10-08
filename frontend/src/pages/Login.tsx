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
 * Asymmetrical Editorial Login Page.
 * Left Column (40% width): Brand Showcase & Monospaced System Metrics Ticker.
 * Right Column (60% width): Utilitarian High-Contrast Authentication Engine.
 */
export function Login() {
  const navigate = useNavigate()
  const { login, loginWithTokens } = useAuth()
  const { push } = useToast()

  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [serverError, setServerError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  const validate = (): boolean => {
    const errors: Record<string, string> = {}
    if (!email.trim()) errors.email = 'Email is required.'
    else if (!looksLikeEmail(email)) errors.email = 'Enter a valid email address.'
    if (!password) errors.password = 'Password is required.'
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
    const result = await login(email, password)
    setLoading(false)

    if (result.ok) {
      navigate('/dashboard', { replace: true })
    } else if (result.verificationRequired && result.userId) {
      const emailParam = result.email ? `&email=${encodeURIComponent(result.email)}` : ''
      navigate(`/verify?user_id=${result.userId}${emailParam}`)
    } else {
      setServerError(result.error)
    }
  }

  return (
    <main className="min-h-screen grid grid-cols-1 md:grid-cols-12 bg-arch-950 text-zinc-100 font-sans select-none">
      {/* LEFT COLUMN: Brand & Product Highlights (40% / 5 cols) */}
      <div className="hidden md:flex md:col-span-5 flex-col justify-between border-r border-arch-border bg-arch-950 p-8 lg:p-10 relative bg-arch-grid">
        {/* Brand Logotype */}
        <div>
          <div className="flex items-center gap-3">
            <div className="flex h-9 w-9 items-center justify-center rounded bg-amber-500 text-arch-950 font-display font-black text-lg shadow-sharp">
              B
            </div>
            <span className="font-display text-xl font-bold tracking-tight text-white">Blob-Cloud</span>
          </div>

          <div className="mt-10 lg:mt-12">
            <h1 className="font-display text-2xl lg:text-3xl font-extrabold tracking-tight text-white leading-tight">
              All your files. <br />
              Fast, secure, and always in sync.
            </h1>
            <p className="mt-3 text-xs leading-relaxed text-zinc-400 font-sans max-w-sm">
              Upload and preview files at lightning speed, share effortlessly with smart permissions, and keep your workspace organized in one place.
            </p>
          </div>
        </div>

        {/* Feature Highlights */}
        <div className="border-t border-arch-border pt-5 space-y-3">
          <div className="flex items-start gap-2.5">
            <div className="mt-0.5 text-amber-400/90 shrink-0">
              <svg className="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2" />
              </svg>
            </div>
            <div>
              <p className="text-xs font-medium text-zinc-300">Instant Sync</p>
              <p className="text-[11px] text-zinc-500 leading-snug">Smart deduplication and chunked transfers for rapid uploads.</p>
            </div>
          </div>

          <div className="flex items-start gap-2.5">
            <div className="mt-0.5 text-amber-400/90 shrink-0">
              <svg className="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <rect x="3" y="11" width="18" height="11" rx="2" ry="2" />
                <path d="M7 11V7a5 5 0 0 1 10 0v4" />
              </svg>
            </div>
            <div>
              <p className="text-xs font-medium text-zinc-300">Private & Secure</p>
              <p className="text-[11px] text-zinc-500 leading-snug">Encrypted storage with granular access permissions and link controls.</p>
            </div>
          </div>

          <div className="flex items-start gap-2.5">
            <div className="mt-0.5 text-amber-400/90 shrink-0">
              <svg className="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <path d="m12 3-1.9 5.8a2 2 0 0 1-1.3 1.3L3 12l5.8 1.9a2 2 0 0 1 1.3 1.3L12 21l1.9-5.8a2 2 0 0 1 1.3-1.3L21 12l-5.8-1.9a2 2 0 0 1-1.3-1.3Z" />
              </svg>
            </div>
            <div>
              <p className="text-xs font-medium text-zinc-300">Smart File Insights</p>
              <p className="text-[11px] text-zinc-500 leading-snug">Automated summaries, instant document previews, and intelligent search.</p>
            </div>
          </div>
        </div>
      </div>

      {/* RIGHT COLUMN: Authentication Form (60% / 7 cols) */}
      <div className="md:col-span-7 flex flex-col justify-center items-center p-6 md:p-16 bg-arch-900">
        <div className="w-full max-w-sm">
          {/* Header */}
          <div className="mb-8">
            <h2 className="font-display text-2xl font-bold text-white tracking-tight">Welcome back</h2>
            <p className="mt-1 text-sm text-zinc-400">Enter your details to access your workspace.</p>
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
              onError={() => setServerError('Google Login failed.')}
              theme="filled_black"
              shape="rectangular"
              text="signin_with"
            />
          </div>

          <div className="relative mb-6 flex items-center justify-center">
            <div className="absolute inset-0 flex items-center">
              <div className="w-full border-t border-arch-border" />
            </div>
            <span className="relative bg-arch-900 px-3 text-xs text-zinc-400">
              or continue with email
            </span>
          </div>

          <form onSubmit={handleSubmit} className="flex flex-col gap-4" noValidate>
            <div>
              <label htmlFor="login-email" className="mb-1.5 block text-xs font-medium text-zinc-300">
                Email address
              </label>
              <Input
                id="login-email"
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
              <div className="mb-1.5 flex items-center justify-between">
                <label htmlFor="login-password" className="block text-xs font-medium text-zinc-300">
                  Password
                </label>
                <Link
                  to="/forgot-password"
                  className="text-xs text-amber-400 hover:text-amber-300 transition-colors"
                >
                  Forgot password?
                </Link>
              </div>

              <Input
                id="login-password"
                type="password"
                autoComplete="current-password"
                placeholder="••••••••••••"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                error={fieldErrors.password}
                disabled={loading}
              />
            </div>

            <Button type="submit" loading={loading} className="mt-3">
              Sign In
            </Button>
          </form>

          <p className="mt-8 text-center text-xs text-zinc-400">
            Don't have an account?{' '}
            <Link
              to="/register"
              className="text-amber-400 hover:text-amber-300 font-medium transition-colors hover:underline"
            >
              Sign up
            </Link>
          </p>
        </div>
      </div>
    </main>
  )
}

export default Login
