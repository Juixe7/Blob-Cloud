import { useEffect, useState } from 'react'
import { apiClient } from '../lib/api'
import { useAuth } from '../hooks/useAuth'
import { formatFileSize } from '../lib/format'
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
      <Modal open={open} onClose={onClose} label="Settings" maxWidthClass="max-w-2xl">
        <div className="space-y-6">
          <div>
            <h2 className="text-xl font-bold text-slate-900 dark:text-zinc-50">Settings</h2>
            <p className="text-xs text-slate-500 dark:text-zinc-400">
              Manage account, storage, devices, and preferences
            </p>
          </div>

          {/* Nav Tabs */}
          <div className="flex border-b border-slate-200 text-sm dark:border-zinc-800">
            <button
              type="button"
              onClick={() => setActiveTab('storage')}
              className={`pb-2.5 px-4 font-medium transition-colors border-b-2 -mb-px ${
                activeTab === 'storage'
                  ? 'border-amber-500 text-amber-600 dark:border-amber-400 dark:text-amber-400 font-semibold'
                  : 'border-transparent text-slate-500 hover:text-slate-900 dark:text-zinc-400 dark:hover:text-zinc-200'
              }`}
            >
              Storage Usage
            </button>
            <button
              type="button"
              onClick={() => setActiveTab('account')}
              className={`pb-2.5 px-4 font-medium transition-colors border-b-2 -mb-px ${
                activeTab === 'account'
                  ? 'border-amber-500 text-amber-600 dark:border-amber-400 dark:text-amber-400 font-semibold'
                  : 'border-transparent text-slate-500 hover:text-slate-900 dark:text-zinc-400 dark:hover:text-zinc-200'
              }`}
            >
              Account & Devices
            </button>
          </div>

          {/* Tab 1: Storage Usage */}
          {activeTab === 'storage' && (
            <div className="space-y-6">
              {loadingStorage ? (
                <div className="flex h-32 items-center justify-center">
                  <Spinner size={24} />
                </div>
              ) : (
                <div className="rounded-xl border border-slate-200 bg-slate-50 p-4 space-y-3 dark:border-zinc-800 dark:bg-zinc-900/60">
                  <div className="flex items-center justify-between text-sm">
                    <span className="font-semibold text-slate-800 dark:text-zinc-200">Used Storage</span>
                    <span className="font-mono text-xs text-slate-500 dark:text-zinc-400">
                      {formatFileSize(used)} / {formatFileSize(limit)} ({totalPct}%)
                    </span>
                  </div>

                  <div className="flex h-3.5 w-full overflow-hidden rounded-full bg-slate-200 p-0.5 dark:bg-zinc-800">
                    <div className="h-full rounded-l-full bg-zinc-600 transition-all" style={{ width: `${imagesPct}%` }} title="Images" />
                    <div className="h-full bg-zinc-500 transition-all" style={{ width: `${docsPct}%` }} title="Documents" />
                    <div className="h-full bg-slate-400 transition-all" style={{ width: `${mediaPct}%` }} title="Media" />
                    <div className="h-full bg-amber-500 transition-all" style={{ width: `${codePct}%` }} title="Code" />
                    <div className="h-full rounded-r-full bg-zinc-700 transition-all" style={{ width: `${otherPct}%` }} title="Other" />
                  </div>

                  <div className="grid grid-cols-2 gap-2 pt-2 text-xs text-slate-600 dark:text-zinc-400 sm:grid-cols-3">
                    <div className="flex items-center gap-1.5">
                      <span className="h-2.5 w-2.5 rounded-full bg-zinc-600" />
                      <span>Images ({formatFileSize(images)})</span>
                    </div>
                    <div className="flex items-center gap-1.5">
                      <span className="h-2.5 w-2.5 rounded-full bg-zinc-500" />
                      <span>Documents ({formatFileSize(docs)})</span>
                    </div>
                    <div className="flex items-center gap-1.5">
                      <span className="h-2.5 w-2.5 rounded-full bg-slate-400" />
                      <span>Media ({formatFileSize(media)})</span>
                    </div>
                    <div className="flex items-center gap-1.5">
                      <span className="h-2.5 w-2.5 rounded-full bg-amber-500" />
                      <span>Code ({formatFileSize(code)})</span>
                    </div>
                    <div className="flex items-center gap-1.5">
                      <span className="h-2.5 w-2.5 rounded-full bg-zinc-700 dark:bg-zinc-400" />
                      <span>Other ({formatFileSize(other)})</span>
                    </div>
                  </div>
                </div>
              )}
            </div>
          )}

          {/* Tab 2: Account & Devices */}
          {activeTab === 'account' && (
            <div className="space-y-5">
              {/* User Credentials */}
              <div>
                <h3 className="text-xs font-semibold uppercase tracking-wider text-slate-400 dark:text-zinc-500 mb-2">
                  User Credentials
                </h3>
                <div className="rounded-xl border border-slate-200 bg-slate-50 p-4 flex items-center justify-between dark:border-zinc-800 dark:bg-zinc-900/60">
                  <div className="flex items-center gap-3">
                    <div className="flex h-10 w-10 items-center justify-center rounded-full border-2 border-zinc-700 bg-zinc-800 font-bold text-zinc-100 text-sm">
                      {user?.user_id ? 'U' : 'A'}
                    </div>
                    <div>
                      <p className="text-sm font-semibold text-slate-900 dark:text-zinc-100">Authenticated Account</p>
                      <p className="text-xs font-mono text-slate-500 dark:text-zinc-400">User ID: {user?.user_id ?? 'Unknown'}</p>
                    </div>
                  </div>
                  <Button variant="secondary" className="py-1 px-3 text-xs" onClick={logout}>
                    Sign Out
                  </Button>
                </div>
              </div>

              {/* Security & Password */}
              <div>
                <h3 className="text-xs font-semibold uppercase tracking-wider text-slate-400 dark:text-zinc-500 mb-2">
                  Security & Password
                </h3>
                <div className="rounded-xl border border-slate-200 bg-slate-50 p-4 flex items-center justify-between dark:border-zinc-800 dark:bg-zinc-900/60">
                  <div>
                    <p className="text-sm font-semibold text-slate-900 dark:text-zinc-100">Account Password</p>
                    <p className="text-xs text-slate-500 dark:text-zinc-400">
                      Update your account password or revoke other active sessions
                    </p>
                  </div>
                  <Button
                    variant="primary"
                    className="py-1.5 px-4 text-xs font-semibold"
                    onClick={() => setUpdatePasswordModalOpen(true)}
                  >
                    Update Password
                  </Button>
                </div>
              </div>

              {/* Active Device Sessions */}
              <div>
                <div className="flex items-center justify-between mb-2">
                  <h3 className="text-xs font-semibold uppercase tracking-wider text-slate-400 dark:text-zinc-500">
                    Active Device Sessions
                  </h3>
                </div>
                <div className="rounded-xl border border-slate-200 bg-slate-50 p-4 flex items-center justify-between dark:border-zinc-800 dark:bg-zinc-900/60">
                  <div>
                    <div className="flex items-center gap-2">
                      <p className="text-sm font-semibold text-slate-900 dark:text-zinc-100">Device Management</p>
                    </div>
                    <p className="text-xs text-slate-500 dark:text-zinc-400 mt-0.5">
                      Review all devices and IP addresses logged into your account
                    </p>
                  </div>
                  <Button
                    variant="secondary"
                    className="py-1.5 px-4 text-xs font-semibold"
                    onClick={() => setActiveSessionsModalOpen(true)}
                  >
                    View Active Sessions
                  </Button>
                </div>
              </div>
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="mt-6 flex justify-end border-t border-slate-200 pt-3 dark:border-zinc-800">
          <Button variant="secondary" onClick={onClose}>
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
