import { useState } from 'react'
import { Modal } from './ui/Modal'
import { Button } from './ui/Button'
import { Input } from './ui/Input'

interface PassphraseModalProps {
  open: boolean
  onClose: () => void
  onSubmit: (passphrase: string) => void
  title?: string
  description?: string
  actionLabel?: string
}

export function PassphraseModal({
  open,
  onClose,
  onSubmit,
  title = 'Enter Passphrase',
  description = 'This file is encrypted. Please enter the passphrase to decrypt it.',
  actionLabel = 'Decrypt',
}: PassphraseModalProps) {
  const [passphrase, setPassphrase] = useState('')

  if (!open) return null

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    if (!passphrase.trim()) return
    onSubmit(passphrase)
    setPassphrase('')
  }

  return (
    <Modal open={open} onClose={onClose} label={title} maxWidthClass="max-w-md">
      <form onSubmit={handleSubmit} className="space-y-6">
        <div>
          <h2 className="text-xl font-bold text-slate-900 dark:text-zinc-50">{title}</h2>
          <p className="mt-1 text-sm text-slate-500 dark:text-zinc-400">
            {description}
          </p>
        </div>

        <div>
          <Input
            autoFocus
            type="password"
            value={passphrase}
            onChange={(e) => setPassphrase(e.target.value)}
            placeholder="Enter your secret passphrase"
            required
          />
        </div>

        <div className="flex justify-end gap-3 pt-2">
          <Button type="button" variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" disabled={!passphrase.trim()}>
            {actionLabel}
          </Button>
        </div>
      </form>
    </Modal>
  )
}
