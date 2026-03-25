import { useEffect, useState } from 'react'
import { useParams, useNavigate, Link } from 'react-router-dom'
import { apiClient } from '../lib/api'
import { getAccessToken } from '../lib/token'
import { Button } from '../components/ui/Button'
import { Input } from '../components/ui/Input'
import { Alert } from '../components/ui/Alert'
import { Spinner } from '../components/ui/Spinner'
import type { FileItem } from '../types/file'
import { formatFileSize, formatDate } from '../lib/format'
import { DownloadIcon, FolderIcon } from '../components/icons'
import { FileIcon } from '../components/FileIcon'
import { FilePreviewModal } from '../components/FilePreviewModal'

export function PublicShare() {
  const { token } = useParams<{ token: string }>()
  const navigate = useNavigate()
  const isLoggedIn = !!getAccessToken()
  
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  
  const [passwordRequired, setPasswordRequired] = useState(false)
  const [password, setPassword] = useState('')
  const [verifying, setVerifying] = useState(false)
  
  const [file, setFile] = useState<FileItem | null>(null)
  const [children, setChildren] = useState<FileItem[]>([])
  const [previewOpen, setPreviewOpen] = useState(false)

  const fetchShare = async () => {
    setLoading(true)
    setError(null)
    setPasswordRequired(false)
    try {
      const res = await apiClient.get<{ file: FileItem; children?: FileItem[] }>(`/public/shares/${token}`)
      setFile(res.data.file)
      setChildren(res.data.children || [])
    } catch (err: any) {
      if (err.response?.status === 401 && err.response?.data?.error === 'password_required') {
        setPasswordRequired(true)
      } else {
        setError(err.response?.data?.error || 'Failed to load public share. It may have expired or does not exist.')
      }
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (token) {
      void fetchShare()
      // Track view if user is logged in
      if (isLoggedIn) {
        apiClient.post(`/public/shares/${token}/view`).catch(e => console.error('failed to record public view', e))
      }
    }
  }, [token, isLoggedIn])

  const handleVerify = async (e: React.FormEvent) => {
    e.preventDefault()
    setVerifying(true)
    setError(null)
    try {
      await apiClient.post(`/public/shares/${token}/verify`, { password })
      await fetchShare()
    } catch (err: any) {
      setError(err.response?.data?.error || 'Invalid password.')
    } finally {
      setVerifying(false)
    }
  }

  const [saving, setSaving] = useState(false)
  const [saveSuccess, setSaveSuccess] = useState(false)

  const handleSaveToDrive = async () => {
    if (!isLoggedIn) {
      // Prompt user to login, then redirect back here
      navigate(`/login?redirect=${encodeURIComponent(`/shares/${token}`)}`)
      return
    }

    setSaving(true)
    try {
      await apiClient.post(`/public/shares/${token}/save`)
      setSaveSuccess(true)
      setTimeout(() => setSaveSuccess(false), 3000)
    } catch (err: any) {
      alert(err.response?.data?.error || 'Failed to save to drive')
    } finally {
      setSaving(false)
    }
  }

  const handleDownload = () => {
    const base = apiClient.defaults.baseURL ?? '/api'
    window.location.href = `${base}/public/shares/${token}/download`
  }

  if (loading) {
    return (
      <div className="flex h-screen items-center justify-center bg-arch-950">
        <Spinner size={32} />
      </div>
    )
  }

  if (passwordRequired) {
    return (
      <div className="flex h-screen items-center justify-center bg-arch-950 p-4">
        <div className="w-full max-w-md rounded-xl border border-arch-border bg-arch-900 p-8 shadow-2xl">
          <h2 className="mb-2 text-2xl font-bold text-zinc-50">Protected Link</h2>
          <p className="mb-6 text-sm text-zinc-400">
            This public link is password protected. Enter the password to access the contents.
          </p>
          
          {error && <div className="mb-4"><Alert variant="error">{error}</Alert></div>}
          
          <form onSubmit={handleVerify} className="space-y-4">
            <Input
              type="password"
              placeholder="Enter password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoFocus
            />
            <Button type="submit" className="w-full" disabled={verifying || !password}>
              {verifying ? <Spinner /> : 'Unlock'}
            </Button>
          </form>
        </div>
      </div>
    )
  }

  if (error || !file) {
    return (
      <div className="flex h-screen flex-col items-center justify-center bg-arch-950 p-4">
        <div className="mb-4 max-w-md"><Alert variant="error">{error || 'Share not found'}</Alert></div>
        <Button variant="secondary" onClick={() => navigate('/')}>Return Home</Button>
      </div>
    )
  }

  return (
    <div className="min-h-screen bg-arch-950 text-zinc-100 flex flex-col">
      {/* Branded Header */}
      <header className="flex h-14 shrink-0 items-center justify-between border-b border-arch-border bg-arch-900 px-6 select-none">
        <Link to="/" className="flex items-center gap-2 text-zinc-50 font-bold text-lg hover:text-amber-500 transition-colors">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="text-amber-500">
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path>
            <polyline points="7 10 12 15 17 10"></polyline>
            <line x1="12" y1="15" x2="12" y2="3"></line>
          </svg>
          Blob-Cloud
        </Link>
        <div>
          {isLoggedIn ? (
            <Button variant="primary" onClick={() => navigate('/dashboard')} className="px-3 py-1.5 text-sm">
              Go to Dashboard
            </Button>
          ) : (
            <Button variant="secondary" onClick={() => navigate('/login')} className="px-3 py-1.5 text-sm">
              Sign In
            </Button>
          )}
        </div>
      </header>

      <div className="flex-1 mx-auto max-w-4xl p-8 w-full">
        <div className="mb-8 flex items-center justify-between rounded-xl border border-arch-border bg-arch-900 p-6 shadow-lg">
          <div className="flex flex-col gap-1">
            <h1 className="text-2xl font-bold text-zinc-50 flex items-center gap-3">
              {file.is_directory ? <FolderIcon className="text-amber-500" size={28} /> : <FileIcon className="text-amber-500" size={28} />}
              {file.name}
            </h1>
            <p className="text-sm text-zinc-400">
              Shared securely • {formatFileSize(file.size_bytes)}
            </p>
          </div>
          <div className="flex items-center gap-3">
            {!file.is_directory && (
              <Button variant="secondary" onClick={() => setPreviewOpen(true)} className="flex items-center gap-2">
                Preview
              </Button>
            )}
            <Button variant="secondary" onClick={handleSaveToDrive} disabled={saving} className="flex items-center gap-2 border-amber-500/50 text-amber-500 hover:bg-amber-500/10">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                {saveSuccess ? (
                  <path d="M20 6L9 17l-5-5" />
                ) : (
                  <>
                    <path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z" />
                    <polyline points="17 21 17 13 7 13 7 21" />
                    <polyline points="7 3 7 8 15 8" />
                  </>
                )}
              </svg>
              {saving ? 'Saving...' : saveSuccess ? 'Saved to Drive!' : 'Save to my Drive'}
            </Button>
            <Button variant="primary" onClick={handleDownload} className="flex items-center gap-2">
              <DownloadIcon size={16} />
              Download
            </Button>
          </div>
        </div>
        
        <FilePreviewModal
          open={previewOpen}
          onClose={() => setPreviewOpen(false)}
          file={file}
          onDownload={handleDownload}
          publicToken={token}
        />
        
        {file.is_directory && children.length > 0 && (
          <div className="rounded-xl border border-arch-border bg-arch-900 overflow-hidden shadow-lg">
            <table className="w-full text-left text-sm text-zinc-400">
              <thead className="bg-arch-800 text-xs uppercase text-zinc-300">
                <tr>
                  <th className="px-6 py-4 font-semibold">Name</th>
                  <th className="px-6 py-4 font-semibold">Size</th>
                  <th className="px-6 py-4 font-semibold">Date</th>
                </tr>
              </thead>
              <tbody>
                {children.map(child => (
                  <tr key={child.id} className="border-b border-arch-border hover:bg-arch-800 transition-colors">
                    <td className="px-6 py-4 font-medium text-zinc-200 flex items-center gap-3">
                      {child.is_directory ? <FolderIcon className="text-amber-500" /> : <FileIcon className="text-zinc-500" />}
                      {child.name}
                    </td>
                    <td className="px-6 py-4">{formatFileSize(child.size_bytes)}</td>
                    <td className="px-6 py-4">{formatDate(child.updated_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}
