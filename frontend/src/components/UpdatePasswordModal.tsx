import { type FormEvent, useState } from 'react'
import { apiClient } from '../lib/api'
import { extractError } from '../context/AuthContext'
import { Modal } from './ui/Modal'
import { Button } from './ui/Button'
import { Input } from './ui/Input'
import { Alert } from './ui/Alert'
import { useToast } from './Toast'

interface UpdatePasswordModalProps {
  open: boolean
  onClose: () => void
}

export function UpdatePasswordModal({ open, onClose }: UpdatePasswordModalProps) {
  const { push } = useToast()
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [serverError, setServerError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  const resetForm = () => {
    setCurrentPassword('')
    setNewPassword('')
    setConfirmPassword('')
    setFieldErrors({})
    setServerError(null)
  }

  const handleClose = () => {
    resetForm()
    onClose()
  }

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
    if (!validate()) return

    setLoading(true)
    try {
      const res = await apiClient.post<{ message: string }>('/auth/change-password', {
        current_password: currentPassword,
        new_password: newPassword,
      })
      const msg = res.data.message || 'Password changed successfully.'
      push({ variant: 'success', message: msg })
      handleClose()
    } catch (err) {
      setServerError(extractError(err, 'Failed to update password.'))
    } finally {
      setLoading(false)
    }
  }

  if (!open) return null

  return (
    <Modal open={open} onClose={handleClose} label="Update Password" maxWidthClass="max-w-md">
      <div className="space-y-5">
        <div>
          <h2 className="text-xl font-bold text-slate-900 dark:text-zinc-50">Update Password</h2>
          <p className="text-xs text-slate-500 dark:text-zinc-400 mt-1">
            Changing your password will log out all other active sessions for security.
          </p>
        </div>

        {serverError && <Alert variant="error">{serverError}</Alert>}

        <form onSubmit={handleSubmit} className="space-y-4" noValidate>
          <div>
            <label htmlFor="modal-current-password" className="mb-1 block text-xs font-medium text-slate-700 dark:text-zinc-300">
              Current Password
            </label>
            <Input
              id="modal-current-password"
              type="password"
              autoComplete="current-password"
              placeholder="••••••••"
              value={currentPassword}
              onChange={(e) => setCurrentPassword(e.target.value)}
              error={fieldErrors.currentPassword}
              disabled={loading}
            />
          </div>

          <div>
            <label htmlFor="modal-new-password" className="mb-1 block text-xs font-medium text-slate-700 dark:text-zinc-300">
              New Password (min 8 characters)
            </label>
            <Input
              id="modal-new-password"
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
            <label htmlFor="modal-confirm-password" className="mb-1 block text-xs font-medium text-slate-700 dark:text-zinc-300">
              Confirm New Password
            </label>
            <Input
              id="modal-confirm-password"
              type="password"
              autoComplete="new-password"
              placeholder="••••••••"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              error={fieldErrors.confirmPassword}
              disabled={loading}
            />
          </div>

          <div className="flex justify-end gap-3 pt-3 border-t border-slate-200 dark:border-zinc-800">
            <Button type="button" variant="secondary" onClick={handleClose} disabled={loading}>
              Cancel
            </Button>
            <Button type="submit" loading={loading}>
              Update Password
            </Button>
          </div>
        </form>
      </div>
    </Modal>
  )
}

export default UpdatePasswordModal
