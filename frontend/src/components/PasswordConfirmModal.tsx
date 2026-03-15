import React, { useState } from 'react'
import Modal from './ui/Modal'
import Button from './ui/Button'
import Input from './ui/Input'

interface PasswordConfirmModalProps {
  isOpen: boolean
  onClose: () => void
  onConfirm: (password: string) => Promise<void>
  title: string
  description: string
  confirmButtonText?: string
  isDanger?: boolean
}

export default function PasswordConfirmModal({
  isOpen,
  onClose,
  onConfirm,
  title,
  description,
  confirmButtonText = 'Confirm & Revoke',
  isDanger = true,
}: PasswordConfirmModalProps) {
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!password.trim()) {
      setError('Please enter your password.')
      return
    }

    try {
      setLoading(true)
      setError(null)
      await onConfirm(password)
      setPassword('')
      onClose()
    } catch (err: any) {
      setError(err?.response?.data?.error || err.message || 'Verification failed.')
    } finally {
      setLoading(false)
    }
  }

  return (
    <Modal open={isOpen} onClose={onClose} label={title} maxWidthClass="max-w-md">
      <div className="space-y-4">
        <div>
          <h2 className="text-lg font-semibold text-slate-900 dark:text-zinc-50">{title}</h2>
          <p className="mt-1 text-xs text-slate-500 dark:text-zinc-400">{description}</p>
        </div>

        <form onSubmit={handleSubmit} className="space-y-4 pt-2">
          {error && (
            <div className="rounded-lg bg-rose-500/10 border border-rose-500/20 p-3 text-xs text-rose-600 dark:text-rose-400">
              {error}
            </div>
          )}

          <div>
            <label className="block text-xs font-semibold uppercase tracking-wider text-slate-600 dark:text-zinc-400 mb-1">
              Account Password
            </label>
            <Input
              type="password"
              placeholder="Enter your password..."
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoFocus
              required
            />
          </div>

          <div className="flex justify-end gap-2 border-t border-slate-200 pt-3 dark:border-zinc-800">
            <Button variant="secondary" onClick={onClose} type="button" disabled={loading}>
              Cancel
            </Button>
            <Button
              type="submit"
              disabled={loading || !password.trim()}
              className={isDanger ? 'bg-rose-600 hover:bg-rose-700 text-white' : ''}
            >
              {loading ? 'Verifying...' : confirmButtonText}
            </Button>
          </div>
        </form>
      </div>
    </Modal>
  )
}
