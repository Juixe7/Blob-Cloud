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
import { UPLOAD_COMPLETE_EVENT } from '../context/UploadContext'
import { Dashboard } from './Dashboard'

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
        apiClient.post(`/public/shares/${token}/view`).catch((e) => console.error('failed to record public view', e))
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
      // Prompt user to login, then redirect back to this share link
      navigate(`/login?redirect=${encodeURIComponent(`/share/${token}`)}`)
      return
    }

    setSaving(true)
    try {
      await apiClient.post(`/public/shares/${token}/save`)
      setSaveSuccess(true)
      // Notify background drive view to refresh storage / files
      window.dispatchEvent(new Event(UPLOAD_COMPLETE_EVENT))
      setTimeout(() => setSaveSuccess(false), 4000)
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

  // --------------------------------------------------------------------------
  // Password prompt dialog
  // --------------------------------------------------------------------------
  const renderPasswordModal = () => (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm p-4 animate-fade-in">
      <div className="w-full max-w-md rounded-xl border border-arch-border bg-arch-900 p-8 shadow-2xl">
        <div className="mb-4 flex h-10 w-10 items-center justify-center rounded-lg bg-amber-500/10 text-amber-400">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <rect x="3" y="11" width="18" height="11" rx="2" ry="2" />
            <path d="M7 11V7a5 5 0 0 1 10 0v4" />
          </svg>
        </div>
        <h2 className="mb-1.5 text-xl font-bold text-zinc-50">Protected Link</h2>
        <p className="mb-6 text-xs text-zinc-400">
          This shared link is password protected. Enter the access password to view its contents.
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
          <div className="flex items-center justify-end gap-3 pt-2">
            {isLoggedIn && (
              <Button type="button" variant="secondary" onClick={() => navigate('/dashboard')}>
                Cancel
              </Button>
            )}
            <Button type="submit" disabled={verifying || !password}>
              {verifying ? <Spinner /> : 'Unlock File'}
            </Button>
          </div>
        </form>
      </div>
    </div>
  )

  // --------------------------------------------------------------------------
  // Error dialog
  // --------------------------------------------------------------------------
  const renderErrorModal = () => (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm p-4 animate-fade-in">
      <div className="w-full max-w-md rounded-xl border border-arch-border bg-arch-900 p-6 shadow-2xl text-center">
        <div className="mb-4 mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-rose-500/10 text-rose-400">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <circle cx="12" cy="12" r="10" />
            <line x1="15" y1="9" x2="9" y2="15" />
            <line x1="9" y1="9" x2="15" y2="15" />
          </svg>
        </div>
        <h2 className="mb-2 text-lg font-bold text-zinc-50">Share Link Unavailable</h2>
        <p className="mb-6 text-xs text-zinc-400">
          {error || 'This link may have expired, been revoked by the owner, or does not exist.'}
        </p>
        <Button variant="secondary" onClick={() => navigate(isLoggedIn ? '/dashboard' : '/')} className="w-full">
          {isLoggedIn ? 'Return to My Drive' : 'Return Home'}
        </Button>
      </div>
    </div>
  )

  // ==========================================================================
  // CASE A: USER IS AUTHENTICATED (LOGGED IN)
  // Show user's active account/dashboard in background, with shared file overlaid
  // ==========================================================================
  if (isLoggedIn) {
    return (
      <div className="relative h-screen w-screen overflow-hidden">
        {/* Background: User's live account drive */}
        <Dashboard />

        {/* Loading overlay */}
        {loading && (
          <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs">
            <Spinner size={36} />
          </div>
        )}

        {/* Password Required Modal */}
        {passwordRequired && renderPasswordModal()}

        {/* Error Modal */}
        {!loading && (error || !file) && !passwordRequired && renderErrorModal()}

        {/* Active Shared File Overlay */}
        {!loading && file && (
          <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm p-4 animate-fade-in">
            <div className="w-full max-w-2xl rounded-2xl border border-arch-border bg-arch-900 p-6 shadow-2xl space-y-6 relative">
              {/* Header: Title & Close to Drive button */}
              <div className="flex items-center justify-between border-b border-arch-border pb-4">
                <div className="flex items-center gap-2.5">
                  <span className="flex items-center gap-1.5 rounded-full border border-amber-500/30 bg-amber-500/10 px-2.5 py-0.5 text-[11px] font-semibold text-amber-400 uppercase tracking-wider">
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                      <circle cx="18" cy="5" r="3" />
                      <circle cx="6" cy="12" r="3" />
                      <circle cx="18" cy="19" r="3" />
                      <line x1="8.59" y1="13.51" x2="15.42" y2="17.49" />
                      <line x1="15.41" y1="6.51" x2="8.59" y2="10.49" />
                    </svg>
                    Shared File
                  </span>
                  <span className="text-xs text-zinc-400">Viewing from external link</span>
                </div>
                <button
                  type="button"
                  onClick={() => navigate('/dashboard')}
                  className="rounded p-1 text-zinc-400 hover:bg-arch-800 hover:text-zinc-100 transition-colors"
                  aria-label="Close and return to My Drive"
                  title="Close to My Drive"
                >
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <line x1="18" y1="6" x2="6" y2="18" />
                    <line x1="6" y1="6" x2="18" y2="18" />
                  </svg>
                </button>
              </div>

              {/* File Info Box */}
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 rounded-xl border border-arch-border bg-arch-950 p-4">
                <div className="flex items-center gap-3.5 min-w-0 flex-1">
                  {file.is_directory ? (
                    <FolderIcon className="text-amber-500 shrink-0" size={32} />
                  ) : (
                    <FileIcon className="text-amber-500 shrink-0" size={32} />
                  )}
                  <div className="min-w-0 flex-1">
                    <h2 className="text-base font-semibold text-zinc-100 truncate" title={file.name}>
                      {file.name}
                    </h2>
                    <p className="text-xs text-zinc-400 mt-0.5">
                      Shared securely • {formatFileSize(file.size_bytes)}
                    </p>
                  </div>
                </div>

                {/* Actions */}
                <div className="shrink-0 flex items-center gap-2 flex-wrap sm:flex-nowrap">
                  {!file.is_directory && (
                    <Button variant="secondary" onClick={() => setPreviewOpen(true)}>
                      Preview
                    </Button>
                  )}
                  <Button
                    variant="secondary"
                    onClick={handleSaveToDrive}
                    disabled={saving}
                    className="border-amber-500/40 text-amber-400 hover:bg-amber-500/10"
                  >
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
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
                  <Button variant="primary" onClick={handleDownload}>
                    <DownloadIcon size={14} />
                    Download
                  </Button>
                </div>
              </div>

              {/* Directory contents table if shared item is a folder */}
              {file.is_directory && children.length > 0 && (
                <div className="max-h-60 overflow-y-auto rounded-xl border border-arch-border bg-arch-950 scrollbar-thin">
                  <table className="w-full text-left text-xs text-zinc-400">
                    <thead className="bg-arch-850 sticky top-0 uppercase text-[10px] text-zinc-400 font-medium">
                      <tr>
                        <th className="px-4 py-2.5">Name</th>
                        <th className="px-4 py-2.5">Size</th>
                        <th className="px-4 py-2.5">Date</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-arch-border">
                      {children.map((child) => (
                        <tr key={child.id} className="hover:bg-arch-900/60 transition-colors">
                          <td className="px-4 py-2 text-zinc-200 flex items-center gap-2 truncate max-w-xs">
                            {child.is_directory ? <FolderIcon className="text-amber-500 shrink-0" size={16} /> : <FileIcon className="text-zinc-400 shrink-0" size={16} />}
                            <span className="truncate">{child.name}</span>
                          </td>
                          <td className="px-4 py-2 whitespace-nowrap">{formatFileSize(child.size_bytes)}</td>
                          <td className="px-4 py-2 whitespace-nowrap">{formatDate(child.updated_at)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}

              {/* Footer navigation info */}
              <div className="flex items-center justify-between pt-2 text-xs text-zinc-500">
                <span>Saving adds a shortcut directly into your Drive workspace.</span>
                <Button variant="ghost" onClick={() => navigate('/dashboard')} className="text-xs">
                  Continue to Drive &rarr;
                </Button>
              </div>
            </div>
          </div>
        )}

        {file && (
          <FilePreviewModal
            open={previewOpen}
            onClose={() => setPreviewOpen(false)}
            file={file}
            onDownload={handleDownload}
            publicToken={token}
          />
        )}
      </div>
    )
  }

  // ==========================================================================
  // CASE B: USER IS GUEST (NOT LOGGED IN)
  // Show polished public layout with sign-in prompt banner & non-overflowing file card
  // ==========================================================================
  if (loading) {
    return (
      <div className="flex h-screen items-center justify-center bg-arch-950">
        <Spinner size={36} />
      </div>
    )
  }

  if (passwordRequired) {
    return renderPasswordModal()
  }

  if (error || !file) {
    return (
      <div className="flex h-screen flex-col items-center justify-center bg-arch-950 p-4">
        <div className="mb-4 max-w-md w-full"><Alert variant="error">{error || 'Share not found'}</Alert></div>
        <Button variant="secondary" onClick={() => navigate('/')}>Return Home</Button>
      </div>
    )
  }

  return (
    <div className="min-h-screen bg-arch-950 text-zinc-100 flex flex-col font-sans select-none">
      {/* Branded Header */}
      <header className="flex h-14 shrink-0 items-center justify-between border-b border-arch-border bg-arch-900 px-6">
        <Link to="/" className="flex items-center gap-2.5 text-zinc-50 font-bold text-base hover:text-amber-500 transition-colors">
          <div className="flex h-7 w-7 items-center justify-center rounded bg-amber-500 text-arch-950 font-black text-sm">
            B
          </div>
          Blob-Cloud
        </Link>
        <div className="flex items-center gap-3">
          <Button
            variant="ghost"
            onClick={() => navigate(`/login?redirect=${encodeURIComponent(`/share/${token}`)}`)}
            className="px-3 py-1.5 text-xs text-zinc-300 hover:text-white"
          >
            Sign In
          </Button>
          <Button
            variant="primary"
            onClick={() => navigate('/register')}
            className="px-3.5 py-1.5 text-xs"
          >
            Create Account
          </Button>
        </div>
      </header>

      {/* Main Content Area */}
      <div className="flex-1 mx-auto max-w-3xl p-6 sm:p-10 w-full flex flex-col justify-center">
        {/* Guest Prompt Banner */}
        <div className="mb-6 rounded-xl border border-amber-500/20 bg-amber-500/5 p-4 text-xs sm:text-sm text-zinc-300 flex items-start gap-3 shadow-sm">
          <div className="rounded-full bg-amber-500/10 p-2 text-amber-400 shrink-0 mt-0.5">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="12" cy="12" r="10" />
              <line x1="12" y1="8" x2="12" y2="12" />
              <line x1="12" y1="16" x2="12.01" y2="16" />
            </svg>
          </div>
          <div className="flex-1 min-w-0">
            <h3 className="font-semibold text-zinc-100 text-sm">Viewing as a Guest</h3>
            <p className="text-xs text-zinc-400 mt-1">
              You are viewing a shared link. <Link to={`/login?redirect=${encodeURIComponent(`/share/${token}`)}`} className="text-amber-400 hover:underline font-medium">Sign in</Link> or <Link to="/register" className="text-amber-400 hover:underline font-medium">create an account</Link> to save this file directly to your Blob-Cloud Drive, access version history, and collaborate.
            </p>
          </div>
        </div>

        {/* Shared File Card */}
        <div className="mb-6 flex flex-col sm:flex-row sm:items-center justify-between gap-4 rounded-xl border border-arch-border bg-arch-900 p-5 sm:p-6 shadow-xl">
          <div className="flex items-center gap-3.5 min-w-0 flex-1">
            {file.is_directory ? (
              <FolderIcon className="text-amber-500 shrink-0" size={32} />
            ) : (
              <FileIcon className="text-amber-500 shrink-0" size={32} />
            )}
            <div className="min-w-0 flex-1">
              <h1 className="text-base sm:text-lg font-semibold text-zinc-100 truncate" title={file.name}>
                {file.name}
              </h1>
              <p className="text-xs text-zinc-400 mt-0.5">
                Shared securely • {formatFileSize(file.size_bytes)}
              </p>
            </div>
          </div>

          {/* Non-overflowing Buttons Group */}
          <div className="shrink-0 flex items-center gap-2.5 flex-wrap sm:flex-nowrap">
            {!file.is_directory && (
              <Button variant="secondary" onClick={() => setPreviewOpen(true)} className="flex items-center gap-1.5">
                Preview
              </Button>
            )}
            <Button
              variant="secondary"
              onClick={handleSaveToDrive}
              className="flex items-center gap-2 border-amber-500/40 text-amber-400 hover:bg-amber-500/10"
            >
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z" />
                <polyline points="17 21 17 13 7 13 7 21" />
                <polyline points="7 3 7 8 15 8" />
              </svg>
              Save to my Drive
            </Button>
            <Button variant="primary" onClick={handleDownload} className="flex items-center gap-1.5">
              <DownloadIcon size={15} />
              Download
            </Button>
          </div>
        </div>

        {/* Directory Contents Table */}
        {file.is_directory && children.length > 0 && (
          <div className="rounded-xl border border-arch-border bg-arch-900 overflow-hidden shadow-lg">
            <table className="w-full text-left text-xs text-zinc-400">
              <thead className="bg-arch-800 text-[10px] uppercase text-zinc-300 font-semibold">
                <tr>
                  <th className="px-5 py-3">Name</th>
                  <th className="px-5 py-3">Size</th>
                  <th className="px-5 py-3">Date</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-arch-border">
                {children.map((child) => (
                  <tr key={child.id} className="hover:bg-arch-850 transition-colors">
                    <td className="px-5 py-3 font-medium text-zinc-200 flex items-center gap-2.5">
                      {child.is_directory ? <FolderIcon className="text-amber-500 shrink-0" size={18} /> : <FileIcon className="text-zinc-400 shrink-0" size={18} />}
                      <span className="truncate">{child.name}</span>
                    </td>
                    <td className="px-5 py-3 whitespace-nowrap">{formatFileSize(child.size_bytes)}</td>
                    <td className="px-5 py-3 whitespace-nowrap">{formatDate(child.updated_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <FilePreviewModal
        open={previewOpen}
        onClose={() => setPreviewOpen(false)}
        file={file}
        onDownload={handleDownload}
        publicToken={token}
      />
    </div>
  )
}
