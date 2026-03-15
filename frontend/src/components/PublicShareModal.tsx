import { useState, useEffect } from 'react'
import { apiClient } from '../lib/api'
import { Modal } from './ui/Modal'
import { Button } from './ui/Button'
import { Input } from './ui/Input'
import { Alert } from './ui/Alert'
import { Spinner } from './ui/Spinner'
import { useToast } from './Toast'
import type { FileItem } from '../types/file'

interface PublicShareModalProps {
  open: boolean
  onClose: () => void
  file: FileItem | null
}

export function PublicShareModal({ open, onClose, file }: PublicShareModalProps) {
  const { push } = useToast()
  
  const [accessTier, setAccessTier] = useState<'VIEWER' | 'EDITOR'>('VIEWER')
  const [password, setPassword] = useState('')
  const [expiresInDays, setExpiresInDays] = useState('0')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  
  const [generatedUrl, setGeneratedUrl] = useState<string | null>(null)

  useEffect(() => {
    if (open) {
      setAccessTier('VIEWER')
      setPassword('')
      setExpiresInDays('0')
      setError(null)
      setGeneratedUrl(null)
    }
  }, [open, file])

  if (!open || !file) return null

  const handleGenerate = async (e: React.FormEvent) => {
    e.preventDefault()
    setLoading(true)
    setError(null)
    
    let expiresAt: string | null = null
    const days = parseInt(expiresInDays, 10)
    if (days > 0) {
      const d = new Date()
      d.setDate(d.getDate() + days)
      expiresAt = d.toISOString()
    }

    try {
      const res = await apiClient.post<{ share_url: string }>(`/files/${file.id}/links`, {
        access_tier: accessTier,
        password: password || undefined,
        expires_at: expiresAt,
      })
      setGeneratedUrl(res.data.share_url)
      push({ message: 'Public link generated.', variant: 'success' })
    } catch (err: any) {
      setError(err.response?.data?.error || 'Failed to generate public link')
    } finally {
      setLoading(false)
    }
  }

  const handleCopy = () => {
    if (generatedUrl) {
      navigator.clipboard.writeText(generatedUrl)
      push({ message: 'Link copied to clipboard.', variant: 'success' })
    }
  }

  return (
    <Modal open={open} onClose={onClose} label="Create Public Link" maxWidthClass="max-w-md">
      <div className="space-y-6">
        <div>
          <h2 className="text-xl font-bold text-slate-900 dark:text-zinc-50">Public Link</h2>
          <p className="mt-1 text-sm text-slate-500 dark:text-zinc-400">
            Generate a secure, shareable link for <strong className="text-zinc-200">{file.name}</strong>.
          </p>
        </div>

        {error && <Alert variant="error">{error}</Alert>}

        {!generatedUrl ? (
          <form onSubmit={handleGenerate} className="space-y-4">
            <div>
              <label className="mb-1.5 block text-sm font-medium text-slate-700 dark:text-zinc-300">
                Access Tier
              </label>
              <select
                value={accessTier}
                onChange={(e) => setAccessTier(e.target.value as any)}
                className="w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500 dark:border-zinc-700 dark:bg-[#15181e] dark:text-zinc-100 dark:focus:border-amber-500 dark:focus:ring-amber-500/50"
              >
                <option value="VIEWER">Viewer (Read-only)</option>
                <option value="EDITOR">Editor (Can modify)</option>
              </select>
            </div>

            <div>
              <label className="mb-1.5 block text-sm font-medium text-slate-700 dark:text-zinc-300">
                Password (Optional)
              </label>
              <Input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="Leave blank for no password"
              />
            </div>

            <div>
              <label className="mb-1.5 block text-sm font-medium text-slate-700 dark:text-zinc-300">
                Expires In (Days)
              </label>
              <select
                value={expiresInDays}
                onChange={(e) => setExpiresInDays(e.target.value)}
                className="w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500 dark:border-zinc-700 dark:bg-[#15181e] dark:text-zinc-100 dark:focus:border-amber-500 dark:focus:ring-amber-500/50"
              >
                <option value="0">Never</option>
                <option value="1">1 Day</option>
                <option value="7">7 Days</option>
                <option value="30">30 Days</option>
              </select>
            </div>

            <div className="flex justify-end gap-3 pt-4">
              <Button type="button" variant="secondary" onClick={onClose} disabled={loading}>
                Cancel
              </Button>
              <Button type="submit" disabled={loading}>
                {loading ? <Spinner /> : 'Generate Link'}
              </Button>
            </div>
          </form>
        ) : (
          <div className="space-y-4">
            <div className="rounded-md border border-amber-500/30 bg-amber-500/10 p-4">
              <label className="block text-xs font-semibold text-amber-500 uppercase tracking-wider mb-2">
                Shareable URL
              </label>
              <div className="flex items-center gap-2">
                <input
                  type="text"
                  readOnly
                  value={generatedUrl}
                  className="flex-1 rounded border border-zinc-700 bg-black/40 px-3 py-2 text-sm text-zinc-100 focus:outline-none focus:ring-1 focus:ring-amber-500"
                />
                <Button type="button" variant="primary" onClick={handleCopy}>
                  Copy
                </Button>
              </div>
            </div>
            
            {password && (
              <Alert variant="warning">
                Remember to share the password securely with the recipient. We cannot recover it if lost.
              </Alert>
            )}

            <div className="flex justify-end pt-2">
              <Button type="button" variant="secondary" onClick={onClose}>
                Done
              </Button>
            </div>
          </div>
        )}
      </div>
    </Modal>
  )
}
