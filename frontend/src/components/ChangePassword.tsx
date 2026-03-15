import { type FormEvent, useState } from 'react'
import { apiClient } from '../lib/api'
import { extractError } from '../context/AuthContext'
import { useToast } from './Toast'
import { Button } from './ui/Button'
import { Input } from './ui/Input'
import { Alert } from './ui/Alert'

export function ChangePassword() {
  const { push } = useToast()
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [serverError, setServerError] = useState<string | null>(null)
  const [successMessage, setSuccessMessage] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  const validate = (): boolean => {
    const errors: Record<string, string> = {}
    if (!currentPassword) {
      errors.currentPassword = 'Current password is required.'
    }
    if (!newPassword) {
      errors.newPassword = 'New password is required.'
    } else if (newPassword.length < 8) {
      errors.newPassword = 'New password must be at least 8 characters.'
    }
    if (!confirmPassword) {
      errors.confirmPassword = 'Please confirm your new password.'
    } else if (newPassword !== confirmPassword) {
      errors.confirmPassword = 'Passwords do not match.'
    }
    setFieldErrors(errors)
    return Object.keys(errors).length === 0
  }

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setServerError(null)
    setSuccessMessage(null)
    if (!validate()) return

    setLoading(true)
    try {
      const res = await apiClient.post<{ message: string }>('/auth/change-password', {
        current_password: currentPassword,
        new_password: newPassword,
      })
      const msg = res.data.message || 'Password changed successfully.'
      setSuccessMessage(msg)
      push({ variant: 'success', message: msg })
      setCurrentPassword('')
      setNewPassword('')
      setConfirmPassword('')
      setFieldErrors({})
    } catch (err) {
      setServerError(extractError(err, 'Failed to change password.'))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="rounded-xl border border-slate-200 bg-slate-50 p-4 space-y-4 dark:border-zinc-800 dark:bg-zinc-900/60">
      <div>
        <h3 className="text-xs font-semibold uppercase tracking-wider text-slate-400 dark:text-zinc-500 mb-1">
          Security & Password
        </h3>
        <p className="text-xs text-slate-500 dark:text-zinc-400">
          Update your account password. Changing your password will log out all other active sessions.
        </p>
      </div>

      {serverError && <Alert variant="error">{serverError}</Alert>}
      {successMessage && <Alert variant="success">{successMessage}</Alert>}

      <form onSubmit={handleSubmit} className="space-y-3" noValidate>
        <div>
          <label htmlFor="change-current-password" className="mb-1 block text-xs font-medium text-slate-700 dark:text-zinc-300">
            Current Password
          </label>
          <Input
            id="change-current-password"
            type="password"
            autoComplete="current-password"
            placeholder="••••••••"
            value={currentPassword}
            onChange={(e) => setCurrentPassword(e.target.value)}
            error={fieldErrors.currentPassword}
            disabled={loading}
          />
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <div>
            <label htmlFor="change-new-password" className="mb-1 block text-xs font-medium text-slate-700 dark:text-zinc-300">
              New Password
            </label>
            <Input
              id="change-new-password"
              type="password"
              autoComplete="new-password"
              placeholder="••••••••"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              error={fieldErrors.newPassword}
              disabled={loading}
            />
          </div>

          <div>
            <label htmlFor="change-confirm-password" className="mb-1 block text-xs font-medium text-slate-700 dark:text-zinc-300">
              Confirm New Password
            </label>
            <Input
              id="change-confirm-password"
              type="password"
              autoComplete="new-password"
              placeholder="••••••••"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              error={fieldErrors.confirmPassword}
              disabled={loading}
            />
          </div>
        </div>

        <div className="pt-1 flex justify-end">
          <Button type="submit" loading={loading} className="py-1.5 px-4 text-xs">
            Update Password
          </Button>
        </div>
      </form>
    </div>
  )
}

export default ChangePassword
