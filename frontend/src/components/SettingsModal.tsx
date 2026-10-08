import { useEffect, useState } from 'react'
import { apiClient } from '../lib/api'
import { useAuth } from '../hooks/useAuth'
import { formatFileSize, cn } from '../lib/format'
import { Modal } from './ui/Modal'
import { Button } from './ui/Button'
import { Spinner } from './ui/Spinner'
import UpdatePasswordModal from './UpdatePasswordModal'
import ActiveSessionsModal, { type UserSession } from './ActiveSessionsModal'

interface StorageMetrics {
  total_used_bytes: number
  storage_limit_bytes: number
  active_sessions_count?: number
  categories: {
    images: number
    documents: number
    media: number
    code: number
    other: number
  }
}

interface SettingsModalProps {
  open: boolean
  onClose: () => void
}

export function SettingsModal({ open, onClose }: SettingsModalProps) {
  const { user, logout } = useAuth()
  const [activeTab, setActiveTab] = useState<'storage' | 'account'>('storage')
  const [storage, setStorage] = useState<StorageMetrics | null>(null)
  const [sessions, setSessions] = useState<UserSession[]>([])
  const [loadingStorage, setLoadingStorage] = useState(false)
  const [loadingSessions, setLoadingSessions] = useState(false)

  // Sub-modal states
  const [updatePasswordModalOpen, setUpdatePasswordModalOpen] = useState(false)
  const [activeSessionsModalOpen, setActiveSessionsModalOpen] = useState(false)

  // Fetch storage metrics & sessions when modal opens or tab switches
  const fetchSessions = async () => {
    setLoadingSessions(true)
    try {
      const res = await apiClient.get<{ sessions: UserSession[] }>('/user/sessions')
      setSessions(res.data.sessions || [])
    } catch {
      // Fallback if fetch fails
    } finally {
      setLoadingSessions(false)
    }
  }

  useEffect(() => {
    if (!open) return

    async function fetchStorageMetrics() {
      setLoadingStorage(true)
      try {
        const res = await apiClient.get<StorageMetrics>('/user/storage')
        setStorage(res.data)
      } catch {
        // Fallback if fetch fails
      } finally {
        setLoadingStorage(false)
      }
    }

    void fetchStorageMetrics()
    if (activeTab === 'account') {
      void fetchSessions()
    }
  }, [open, activeTab])

  if (!open) return null

  const limit = storage?.storage_limit_bytes || 15 * 1_073_741_824
  const used = storage?.total_used_bytes || 0
  const images = storage?.categories.images || 0
  const docs = storage?.categories.documents || 0
  const media = storage?.categories.media || 0
  const code = storage?.categories.code || 0
  const other = storage?.categories.other || 0

  const imagesPct = (images / limit) * 100
  const docsPct = (docs / limit) * 100
  const mediaPct = (media / limit) * 100
  const codePct = (code / limit) * 100
  const otherPct = (other / limit) * 100
  const totalPct = Math.min(100, Math.round((used / limit) * 100))

  return (
    <>
      <Modal open={open} onClose={onClose} label="Settings" maxWidthClass="max-w-lg p-5">
        <div className="space-y-4">
          {/* Header with Close ✕ Button */}
          <div className="flex items-start justify-between pb-3 border-b border-zinc-800/80">
            <div>
              <h2 className="text-base font-bold text-zinc-100 font-display">Settings</h2>
              <p className="text-xs text-zinc-400 mt-0.5">
                Manage account, storage, devices, and preferences
              </p>
            </div>
            <button
              type="button"
              onClick={onClose}
              className="rounded-lg p-1 text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200 transition-colors"
              title="Close settings"
            >
              <svg className="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <line x1="18" y1="6" x2="6" y2="18" />
                <line x1="6" y1="6" x2="18" y2="18" />
              </svg>
            </button>
          </div>

          {/* Segmented Pill Tabs */}
          <div className="flex rounded-lg bg-zinc-950/70 p-1 border border-zinc-800/80 text-xs font-medium">
            <button
              type="button"
              onClick={() => setActiveTab('storage')}
              className={cn(
                'flex-1 py-1.5 px-3 rounded-md transition-all text-center',
                activeTab === 'storage'
                  ? 'bg-zinc-800 text-amber-400 font-semibold shadow-xs'
                  : 'text-zinc-400 hover:text-zinc-200'
              )}
            >
              Storage Usage
            </button>
            <button
              type="button"
              onClick={() => setActiveTab('account')}
              className={cn(
                'flex-1 py-1.5 px-3 rounded-md transition-all text-center',
                activeTab === 'account'
                  ? 'bg-zinc-800 text-amber-400 font-semibold shadow-xs'
                  : 'text-zinc-400 hover:text-zinc-200'
              )}
            >
              Account & Devices
            </button>
          </div>

          {/* Tab 1: Storage Usage */}
          {activeTab === 'storage' && (
            <div className="space-y-3">
              {loadingStorage ? (
                <div className="flex h-28 items-center justify-center">
                  <Spinner size={20} />
                </div>
              ) : (
                <div className="rounded-xl border border-zinc-800/80 bg-zinc-950/40 p-3.5 space-y-3">
                  <div className="flex items-center justify-between text-xs">
                    <span className="font-semibold text-zinc-200">Used Storage</span>
                    <span className="font-mono text-zinc-400">
                      {formatFileSize(used)} / {formatFileSize(limit)} ({totalPct}%)
                    </span>
                  </div>

                  {/* Refined Progress Bar */}
                  <div className="flex h-2.5 w-full overflow-hidden rounded-full bg-zinc-800 p-0.5">
                    {imagesPct > 0 && <div className="h-full rounded-l-full bg-indigo-500 transition-all" style={{ width: `${imagesPct}%` }} title="Images" />}
                    {docsPct > 0 && <div className="h-full bg-sky-500 transition-all" style={{ width: `${docsPct}%` }} title="Documents" />}
                    {mediaPct > 0 && <div className="h-full bg-purple-500 transition-all" style={{ width: `${mediaPct}%` }} title="Media" />}
                    {codePct > 0 && <div className="h-full bg-amber-500 transition-all" style={{ width: `${codePct}%` }} title="Code" />}
                    {otherPct > 0 && <div className="h-full rounded-r-full bg-zinc-500 transition-all" style={{ width: `${otherPct}%` }} title="Other" />}
                  </div>

                  {/* Category Breakdown Legend */}
                  <div className="grid grid-cols-2 sm:grid-cols-3 gap-2 pt-1 text-[11px] text-zinc-400">
                    <div className="flex items-center gap-1.5">
                      <span className="h-2 w-2 rounded-full bg-indigo-500 shrink-0" />
                      <span className="truncate">Images ({images > 0 ? formatFileSize(images) : '0 B'})</span>
                    </div>
                    <div className="flex items-center gap-1.5">
                      <span className="h-2 w-2 rounded-full bg-sky-500 shrink-0" />
                      <span className="truncate">Docs ({docs > 0 ? formatFileSize(docs) : '0 B'})</span>
                    </div>
                    <div className="flex items-center gap-1.5">
                      <span className="h-2 w-2 rounded-full bg-purple-500 shrink-0" />
                      <span className="truncate">Media ({media > 0 ? formatFileSize(media) : '0 B'})</span>
                    </div>
                    <div className="flex items-center gap-1.5">
                      <span className="h-2 w-2 rounded-full bg-amber-500 shrink-0" />
                      <span className="truncate">Code ({code > 0 ? formatFileSize(code) : '0 B'})</span>
                    </div>
                    <div className="flex items-center gap-1.5">
                      <span className="h-2 w-2 rounded-full bg-zinc-500 shrink-0" />
                      <span className="truncate">Other ({other > 0 ? formatFileSize(other) : '0 B'})</span>
                    </div>
                  </div>
                </div>
              )}
            </div>
          )}

          {/* Tab 2: Account & Devices */}
          {activeTab === 'account' && (
            <div className="space-y-3">
              {/* User Credentials */}
              <div className="rounded-xl border border-zinc-800/80 bg-zinc-950/40 p-3 flex items-center justify-between gap-3">
                <div className="flex items-center gap-2.5 min-w-0">
                  <div className="flex h-8 w-8 items-center justify-center rounded-full border border-zinc-700 bg-zinc-800 font-bold text-zinc-200 text-xs shrink-0">
                    {user?.user_id ? 'U' : 'A'}
                  </div>
                  <div className="min-w-0">
                    <p className="text-xs font-semibold text-zinc-100 truncate">Authenticated Account</p>
                    <p className="text-[11px] font-mono text-zinc-400 truncate">User ID: {user?.user_id ?? 'Unknown'}</p>
                  </div>
                </div>
                <Button variant="secondary" className="py-1 px-2.5 text-xs h-7 shrink-0" onClick={logout}>
                  Sign Out
                </Button>
              </div>

              {/* Security & Password */}
              <div className="rounded-xl border border-zinc-800/80 bg-zinc-950/40 p-3 flex items-center justify-between gap-3">
                <div className="min-w-0">
                  <p className="text-xs font-semibold text-zinc-100">Account Password</p>
                  <p className="text-[11px] text-zinc-400 mt-0.5 truncate">
                    Update your account password or revoke sessions
                  </p>
                </div>
                <Button
                  variant="primary"
                  className="py-1 px-3 text-xs font-semibold h-7 shrink-0"
                  onClick={() => setUpdatePasswordModalOpen(true)}
                >
                  Update
                </Button>
              </div>

              {/* Active Device Sessions */}
              <div className="rounded-xl border border-zinc-800/80 bg-zinc-950/40 p-3 flex items-center justify-between gap-3">
                <div className="min-w-0">
                  <p className="text-xs font-semibold text-zinc-100">Active Sessions</p>
                  <p className="text-[11px] text-zinc-400 mt-0.5 truncate">
                    Review logged-in devices and active IPs
                  </p>
                </div>
                <Button
                  variant="secondary"
                  className="py-1 px-3 text-xs font-semibold h-7 shrink-0"
                  onClick={() => setActiveSessionsModalOpen(true)}
                >
                  Manage
                </Button>
              </div>
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="mt-4 flex justify-end border-t border-zinc-800/80 pt-3">
          <Button variant="secondary" onClick={onClose} className="h-8 px-4 text-xs font-medium">
            Close
          </Button>
        </div>
      </Modal>

      {/* Update Password Modal */}
      <UpdatePasswordModal
        open={updatePasswordModalOpen}
        onClose={() => setUpdatePasswordModalOpen(false)}
      />

      {/* Active Sessions Modal */}
      <ActiveSessionsModal
        open={activeSessionsModalOpen}
        onClose={() => setActiveSessionsModalOpen(false)}
        sessions={sessions}
        loading={loadingSessions}
        onRefreshSessions={fetchSessions}
      />
    </>
  )
}

export default SettingsModal
