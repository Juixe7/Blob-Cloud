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
      {/* LEFT COLUMN: Asymmetrical Brand & Technical Metrics (40% / 5 cols) */}
      <div className="hidden md:flex md:col-span-5 flex-col justify-between border-r border-arch-border bg-arch-950 p-10 relative bg-arch-grid">
        {/* Brand Logotype */}
        <div>
          <div className="flex items-center gap-3">
            <div className="flex h-9 w-9 items-center justify-center rounded bg-amber-500 text-arch-950 font-display font-black text-lg shadow-sharp">
              B
            </div>
            <span className="font-display text-xl font-bold tracking-tight text-white">Blob-Cloud</span>
            <span className="font-mono text-[10px] font-semibold text-amber-400 bg-amber-500/10 border border-amber-500/30 rounded px-2 py-0.5 ml-auto">
              v2.4 ZERO-TRUST
            </span>
          </div>

          <div className="mt-16">
            <h1 className="font-display text-3xl font-extrabold tracking-tight text-white leading-tight">
              High-Precision <br />
              Deduplicated Storage
            </h1>
            <p className="mt-4 text-xs leading-relaxed text-zinc-400 font-sans max-w-sm">
              Cryptographically verified block pipelines with server-authoritative ETag inspection and client memory bounding.
            </p>
          </div>
        </div>

        {/* Technical Status Ticker (JetBrains Mono) */}
        <div className="border-t border-arch-border pt-6 font-mono text-[11px] space-y-2 text-zinc-500">
          <div className="flex items-center justify-between">
            <span className="text-zinc-400">STATUS</span>
            <span className="text-amber-400 font-semibold">• ONLINE</span>
          </div>
          <div className="flex items-center justify-between">
            <span className="text-zinc-400">TOCTOU GUARD</span>
            <span className="text-zinc-300">ACTIVE</span>
          </div>
          <div className="flex items-center justify-between">
            <span className="text-zinc-400">ETAG VERIFIER</span>
            <span className="text-zinc-300">ENFORCED</span>
          </div>
        </div>
      </div>

      {/* RIGHT COLUMN: High-Contrast Auth Engine (60% / 7 cols) */}
      <div className="md:col-span-7 flex flex-col justify-center items-center p-6 md:p-16 bg-arch-900">
        <div className="w-full max-w-sm">
          {/* Header */}
          <div className="mb-8">
            <h2 className="font-display text-xl font-bold text-white tracking-tight">System Authentication</h2>
            <p className="mt-1 font-mono text-xs text-zinc-500">Enter your credentials to access your workspace.</p>
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
            <span className="relative bg-arch-900 px-3 font-mono text-[10px] uppercase tracking-widest text-zinc-500">
              OR EMAIL IDENTITY
            </span>
          </div>

          <form onSubmit={handleSubmit} className="flex flex-col gap-4" noValidate>
            <div>
              <label htmlFor="login-email" className="mb-1 block font-mono text-[10px] font-semibold uppercase tracking-wider text-zinc-400">
                EMAIL ADDRESS
              </label>
              <Input
                id="login-email"
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
              <div className="mb-1 flex items-center justify-between">
                <label htmlFor="login-password" className="block font-mono text-[10px] font-semibold uppercase tracking-wider text-zinc-400">
                  PASSWORD
                </label>
                <Link
                  to="/forgot-password"
                  className="font-mono text-[11px] text-amber-400 hover:text-amber-300 transition-colors"
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

          <p className="mt-8 text-center text-xs text-zinc-500 font-mono">
            New operator?{' '}
            <Link
              to="/register"
              className="text-amber-400 hover:text-amber-300 font-semibold transition-colors underline underline-offset-4"
            >
              Create Account
            </Link>
          </p>
        </div>
      </div>
    </main>
  )
}

export default Login
