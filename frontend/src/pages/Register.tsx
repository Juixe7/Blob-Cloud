import { type FormEvent, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useAuth } from '../hooks/useAuth'
import { Button } from '../components/ui/Button'
import { Input } from '../components/ui/Input'
import { Alert } from '../components/ui/Alert'

function looksLikeEmail(value: string): boolean {
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value)
}

export function Register() {
  const navigate = useNavigate()
  const { register } = useAuth()

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

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setServerError(null)
    if (!validate()) return

    setLoading(true)
    const result = await register(email, password)
    setLoading(false)

    if (result.ok) {
      // Auto-login handled by register() saving tokens. Navigate to dashboard.
      navigate('/dashboard', { replace: true })
    } else {
      setServerError(result.error)
    }
  }

  return (
    <main className="relative flex min-h-screen items-center justify-center overflow-hidden bg-zinc-950">
      {/* Grid gradient backdrop */}
      <div className="pointer-events-none absolute inset-0 bg-grid opacity-60" />

      {/* Soft radial glow behind the card */}
      <div className="pointer-events-none absolute left-1/2 top-1/2 h-[500px] w-[500px] -translate-x-1/2 -translate-y-1/2 rounded-full bg-blue-600/10 blur-[120px]" />

      {/* Card */}
      <div className="relative z-10 w-full max-w-md animate-fade-in rounded-xl border border-zinc-800 bg-zinc-900 p-8 shadow-2xl">
        {/* Brand */}
        <div className="mb-8 text-center">
          <h1 className="text-2xl font-semibold tracking-tight text-zinc-50">
            Create your account
          </h1>
          <p className="mt-2 text-sm text-zinc-400">
            Join Blob-Cloud — fast, secure, deduplicated cloud storage.
          </p>
        </div>

        {/* Server error */}
        {serverError && (
          <div className="mb-6">
            <Alert variant="error">{serverError}</Alert>
          </div>
        )}

        <form onSubmit={handleSubmit} className="flex flex-col gap-5" noValidate>
          <div>
            <label htmlFor="reg-email" className="mb-1.5 block text-sm font-medium text-zinc-300">
              Email
            </label>
            <Input
              id="reg-email"
              type="email"
              autoComplete="email"
              placeholder="you@example.com"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              error={fieldErrors.email}
              disabled={loading}
            />
          </div>

          <div>
            <label htmlFor="reg-password" className="mb-1.5 block text-sm font-medium text-zinc-300">
              Password
            </label>
            <Input
              id="reg-password"
              type="password"
              autoComplete="new-password"
              placeholder="••••••••"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              error={fieldErrors.password}
              disabled={loading}
            />
          </div>

          <div>
            <label
              htmlFor="reg-confirm"
              className="mb-1.5 block text-sm font-medium text-zinc-300"
            >
              Confirm password
            </label>
            <Input
              id="reg-confirm"
              type="password"
              autoComplete="new-password"
              placeholder="••••••••"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              error={fieldErrors.confirmPassword}
              disabled={loading}
            />
          </div>

          <Button type="submit" loading={loading} className="mt-2">
            Create account
          </Button>
        </form>

        <p className="mt-6 text-center text-sm text-zinc-500">
          Already have an account?{' '}
          <Link
            to="/login"
            className="font-medium text-violet-400 transition-colors hover:text-violet-300"
          >
            Sign in
          </Link>
        </p>
      </div>
    </main>
  )
}

export default Register
