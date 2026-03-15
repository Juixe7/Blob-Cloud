import { useState, useRef, useEffect, type ReactElement } from 'react'
import { cn, formatFileSize } from '../lib/format'
import { apiClient } from '../lib/api'
import { UPLOAD_COMPLETE_EVENT } from '../context/UploadContext'
import type { WebSocketStatus } from '../types/sync'

interface SidebarProps {
  /** Whether the sidebar is in collapsed (icon-only) mode. */
  collapsed: boolean
  mobileOpen?: boolean
  onCloseMobile?: () => void
  onToggleCollapse: () => void
  onNewFolder: () => void
  onUploadFile: () => void
  onUploadFolder?: (files: File[]) => void
  isE2EEnabled?: boolean
  onToggleE2E?: () => void
  onSignOut: () => void
  onOpenSettings?: () => void
  onSelectNav?: (navId: string) => void
  activeNav?: string
  disableNew?: boolean
  /** Currently used storage in bytes (optional fallback). */
  storageUsed?: number
  /** Total storage limit in bytes (optional fallback). */
  storageLimit?: number
  /** Real-time WebSocket connection status (Phase 7.5). */
  syncStatus?: WebSocketStatus
}

/** Navigation item definition. */
interface NavItem {
  id: string
  label: string
  icon: ReactElement
  active?: boolean
  disabled?: boolean
}

/**
 * Collapsible sidebar with navigation, "+ New" dropdown, storage gauge, and
 * sign-out button. Follows the Linear/Vercel dark palette.
 */
export function Sidebar({
  collapsed,
  mobileOpen = false,
  onCloseMobile,
  onToggleCollapse,
  onNewFolder,
  onUploadFile,
  onUploadFolder,
  isE2EEnabled = false,
  onToggleE2E,
  onSignOut,
  onOpenSettings,
  onSelectNav,
  activeNav = 'drive',
  disableNew = false,
  syncStatus = 'DISCONNECTED',
}: SidebarProps) {
  const [dropdownOpen, setDropdownOpen] = useState(false)
  const dropdownRef = useRef<HTMLDivElement>(null)
  const folderInputRef = useRef<HTMLInputElement>(null)


  const handleFolderChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = e.target.files
    if (files && files.length > 0) {
      onUploadFolder?.(Array.from(files))
    }
    e.target.value = ''
  }



  // Real database-calculated storage metrics
  const [realUsed, setRealUsed] = useState<number>(0)
  const [realLimit, setRealLimit] = useState<number>(15 * 1073741824)

  const fetchRealStorage = async () => {
    try {
      const res = await apiClient.get<{ total_used_bytes: number; storage_limit_bytes: number }>('/user/storage')
      setRealUsed(res.data.total_used_bytes)
      setRealLimit(res.data.storage_limit_bytes || 15 * 1073741824)
    } catch {
      // Keep previous or zero on error
    }
  }

  useEffect(() => {
    void fetchRealStorage()
    window.addEventListener(UPLOAD_COMPLETE_EVENT, fetchRealStorage)
    return () => window.removeEventListener(UPLOAD_COMPLETE_EVENT, fetchRealStorage)
  }, [])

  // Close dropdown on outside click (native DOM event, not React synthetic)
  useEffect(() => {
    function handleClick(e: globalThis.MouseEvent) {
      if (dropdownRef.current && !dropdownRef.current.contains(e.target as Node)) {
        setDropdownOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [])

  // Close dropdown on Escape (native DOM event)
  useEffect(() => {
    function handleKey(e: globalThis.KeyboardEvent) {
      if (e.key === 'Escape' && dropdownOpen) setDropdownOpen(false)
    }
    document.addEventListener('keydown', handleKey)
    return () => document.removeEventListener('keydown', handleKey)
  }, [dropdownOpen])

  const storagePercent = realLimit > 0 ? Math.min((realUsed / realLimit) * 100, 100) : 0
  const storageLabel = `${formatFileSize(realUsed)} of ${formatFileSize(realLimit)} used`

  const navItems: NavItem[] = [
    {
      id: 'drive',
      label: 'My Drive',
      active: activeNav === 'drive',
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z" />
        </svg>
      ),
    },
    {
      id: 'shared',
      label: 'Shared with me',
      active: activeNav === 'shared',
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M16 21v-2a4 4 0 00-4-4H6a4 4 0 00-4 4v2" />
          <circle cx="9" cy="7" r="4" />
          <path d="M22 21v-2a4 4 0 00-3-3.87" />
          <path d="M16 3.13a4 4 0 010 7.75" />
        </svg>
      ),
    },
    {
      id: 'recent',
      label: 'Recent',
      active: activeNav === 'recent',
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <circle cx="12" cy="12" r="10" />
          <polyline points="12 6 12 12 16 14" />
        </svg>
      ),
    },
    {
      id: 'trash',
      label: 'Trash',
      active: activeNav === 'trash',
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M3 6h18" />
          <path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2" />
          <line x1="10" y1="11" x2="10" y2="17" />
          <line x1="14" y1="11" x2="14" y2="17" />
        </svg>
      ),
    },
    {
      id: 'settings',
      label: 'Settings',
      active: activeNav === 'settings',
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <circle cx="12" cy="12" r="3" />
          <path d="M12 1v2m0 18v2M4.22 4.22l1.42 1.42m12.72 12.72l1.42 1.42M1 12h2m18 0h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42" />
        </svg>
      ),
    },
  ]

  return (
    <aside
      className={cn(
        'fixed inset-y-0 left-0 z-50 md:relative flex h-full flex-col border-r border-arch-border bg-arch-900 transition-all duration-300 select-none',
        collapsed ? 'w-16' : 'w-48',
        mobileOpen ? 'translate-x-0' : '-translate-x-full md:translate-x-0'
      )}
      aria-label="Sidebar navigation"
    >
      {/* Header: brand + collapse toggle */}
      <div className="flex h-14 items-center border-b border-arch-border px-3">
        {!collapsed && (
          <div className="flex items-center gap-2.5 ml-1">
            <div className="flex h-7 w-7 items-center justify-center rounded bg-amber-500 text-arch-950 font-display font-black text-sm shadow-sharp">
              B
            </div>
            <span className="font-display text-sm font-bold tracking-tight text-white">Blob-Cloud</span>
          </div>
        )}
        <button
          onClick={onToggleCollapse}
          className={cn(
            'hidden md:flex ml-auto rounded p-1.5 text-zinc-400 transition-colors hover:bg-arch-800 hover:text-zinc-100',
            collapsed && 'mx-auto ml-0',
          )}
          aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            {collapsed ? (
              <path d="M9 18l6-6-6-6" />
            ) : (
              <path d="M15 18l-6-6 6-6" />
            )}
          </svg>
        </button>
        <button
          onClick={onCloseMobile}
          className="md:hidden ml-auto rounded p-1.5 text-zinc-400 transition-colors hover:bg-arch-800 hover:text-zinc-100"
          aria-label="Close sidebar"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <line x1="18" y1="6" x2="6" y2="18" />
            <line x1="6" y1="6" x2="18" y2="18" />
          </svg>
        </button>
      </div>

      {/* "+ New" Action Button & Dropdown */}
      <div className="relative px-3 pt-4" ref={dropdownRef}>
        <button
          type="button"
          disabled={disableNew}
          onClick={() => !disableNew && setDropdownOpen((v) => !v)}
          className={cn(
            'flex h-9 w-full items-center justify-center gap-2 rounded bg-amber-500 font-bold text-xs text-arch-950 shadow-sharp border border-amber-400/90 transition-all hover:bg-amber-400 focus:outline-none focus:ring-1 focus:ring-amber-400',
            disableNew && 'opacity-40 cursor-not-allowed bg-arch-800 border-arch-700 text-zinc-500 shadow-none focus:ring-0',
            collapsed && 'justify-center px-0',
          )}
          aria-expanded={dropdownOpen}
          aria-haspopup="true"
          aria-label="Create new"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" aria-hidden="true">
            <path d="M12 5v14M5 12h14" />
          </svg>
          {!collapsed && 'New Entry'}
        </button>

        {dropdownOpen && (
          <div
            role="menu"
            className="absolute left-3 right-3 z-50 mt-1.5 rounded border border-[#282e3b] bg-[#15181e] py-1.5 shadow-2xl animate-fade-in opacity-100"
          >
            <button
              role="menuitem"
              onClick={() => { onUploadFile(); setDropdownOpen(false) }}
              className="flex w-full items-center gap-2.5 px-3 py-2 text-xs font-medium text-zinc-200 transition-colors hover:bg-[#242830] hover:text-white"
            >
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" aria-hidden="true">
                <path d="M12 16V4m0 0l-4 4m4-4l4 4" />
                <path d="M4 17v2a2 2 0 002 2h12a2 2 0 002-2v-2" />
              </svg>
              Upload File
            </button>
            <button
              role="menuitem"
              onClick={() => { folderInputRef.current?.click(); setDropdownOpen(false) }}
              className="flex w-full items-center gap-2.5 px-3 py-2 text-xs font-medium text-zinc-200 transition-colors hover:bg-[#242830] hover:text-white"
            >
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" aria-hidden="true">
                <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
                <path d="M12 11v6M9 14l3-3 3 3" />
              </svg>
              Upload Folder
            </button>
            <button
              role="menuitem"
              onClick={() => { onNewFolder(); setDropdownOpen(false) }}
              className="flex w-full items-center gap-2.5 px-3 py-2 text-xs font-medium text-zinc-200 transition-colors hover:bg-[#242830] hover:text-white"
            >
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" aria-hidden="true">
                <path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z" />
              </svg>
              New Folder
            </button>

          </div>
        )}

        <input
          ref={folderInputRef}
          type="file"
          // @ts-expect-error webkitdirectory is standard in browsers but untyped in React HTMLAttributes
          webkitdirectory=""
          directory=""
          multiple
          className="hidden"
          onChange={handleFolderChange}
          aria-hidden="true"
          tabIndex={-1}
        />
      </div>

      {/* Navigation */}
      <div className="mt-5 px-3">
        {!collapsed && (
          <p className="px-2 pb-1.5 font-mono text-[10px] font-semibold tracking-[0.15em] text-zinc-500 uppercase">
            EXPLORER
          </p>
        )}
        <nav className="flex flex-col gap-0.5" aria-label="Main navigation">
          {navItems.map((item) => (
            <button
              key={item.id}
              disabled={item.disabled}
              onClick={() => {
                if (item.id === 'settings') {
                  onOpenSettings?.()
                } else if (onSelectNav) {
                  onSelectNav(item.id)
                }
                onCloseMobile?.()
              }}
              className={cn(
                'flex items-center gap-3 rounded px-2.5 py-2 text-xs font-medium transition-all duration-150',
                item.active && !item.disabled && 'bg-arch-850 text-white font-semibold border-l-2 border-amber-500 pl-2 shadow-sm',
                !item.active && !item.disabled && 'text-zinc-400 hover:bg-arch-850/60 hover:text-zinc-200',
                item.disabled && 'cursor-not-allowed text-zinc-600',
                collapsed && 'justify-center px-0 border-l-0',
              )}
              aria-current={item.active ? 'page' : undefined}
              title={collapsed ? item.label : undefined}
            >
              <span className={cn(item.active ? 'text-amber-400' : 'text-zinc-400')}>{item.icon}</span>
              {!collapsed && <span>{item.label}</span>}
            </button>
          ))}
        </nav>
        </div>

      {/* Spacer */}
      <div className="flex-1" />

      {/* Global E2EE Shield */}
      {!collapsed && (
        <div className="mx-3 mb-4 rounded border border-zinc-800 bg-zinc-900 p-2.5 shadow-sm">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className={cn(isE2EEnabled ? 'text-violet-500' : 'text-zinc-500')}>
                <rect x="3" y="11" width="18" height="11" rx="2" ry="2"></rect>
                <path d="M7 11V7a5 5 0 0 1 10 0v4"></path>
              </svg>
              <span className="text-xs font-semibold text-zinc-300">end-to-end encryption</span>
            </div>
            <button
              type="button"
              role="switch"
              aria-checked={isE2EEnabled}
              onClick={onToggleE2E}
              className={cn(
                'relative inline-flex h-4 w-7 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none',
                isE2EEnabled ? 'bg-violet-500' : 'bg-zinc-700'
              )}
            >
              <span
                aria-hidden="true"
                className={cn(
                  'pointer-events-none inline-block h-3 w-3 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
                  isE2EEnabled ? 'translate-x-3' : 'translate-x-0'
                )}
              />
            </button>
          </div>
        </div>
      )}

      {/* Storage gauge */}
      {!collapsed && (
        <div className="px-4 pb-3">
          <div className="mb-1.5 flex items-center justify-between font-mono text-[10px] text-zinc-400">
            <span>STORAGE</span>
            <span className="font-semibold text-amber-400">{Math.round(storagePercent)}%</span>
          </div>
          <div className="h-1.5 w-full overflow-hidden rounded-xs bg-arch-950 border border-arch-border">
            <div
              className="h-full bg-amber-500 transition-all duration-300"
              style={{ width: `${storagePercent}%` }}
              role="progressbar"
              aria-valuenow={storagePercent}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-label={storageLabel}
            />
          </div>
          <p className="mt-1.5 font-mono text-[10px] text-zinc-500">{storageLabel}</p>
        </div>
      )}

      {/* Connection status indicator */}
      <ConnectionStatus status={syncStatus} collapsed={collapsed} />

      {/* Sign-out */}
      <div className="border-t border-arch-border px-2 py-2.5">
        <button
          onClick={onSignOut}
          className={cn(
            'flex w-full items-center gap-2.5 rounded px-2.5 py-1.5 text-xs text-zinc-400 transition-colors hover:bg-arch-850 hover:text-zinc-200',
            collapsed && 'justify-center px-0',
          )}
          title={collapsed ? 'Sign out' : undefined}
          aria-label="Sign out"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M9 21H5a2 2 0 01-2-2V5a2 2 0 012-2h4" />
            <polyline points="16,17 21,12 16,7" />
            <line x1="21" y1="12" x2="9" y2="12" />
          </svg>
          {!collapsed && 'Sign out'}
        </button>
      </div>
    </aside>
  )
}

/** Maps a WebSocket status to its style + human label. */
function statusMeta(status: WebSocketStatus): { dot: string; label: string; pulse: boolean } {
  switch (status) {
    case 'CONNECTED':
      return {
        dot: 'bg-amber-500',
        label: 'SYNCED',
        pulse: false,
      }
    case 'CONNECTING':
    case 'RECONNECTING':
      return {
        dot: 'bg-amber-400',
        label: 'SYNCING…',
        pulse: true,
      }
    case 'DISCONNECTED':
    default:
      return { dot: 'bg-rose-500', label: 'OFFLINE', pulse: false }
  }
}

/** Compact connection health indicator shown above the sign-out button. */
function ConnectionStatus({
  status,
  collapsed,
}: {
  status: WebSocketStatus
  collapsed: boolean
}) {
  const meta = statusMeta(status)
  return (
    <div className="px-4 py-2" title={meta.label}>
      <div className={cn('flex items-center gap-2', collapsed && 'justify-center')}>
        <span className="relative flex h-1.5 w-1.5 flex-shrink-0">
          {meta.pulse && (
            <span
              className={cn(
                'absolute inline-flex h-full w-full animate-ping rounded-full opacity-75',
                meta.dot,
              )}
            />
          )}
          <span className={cn('relative inline-flex h-1.5 w-1.5 rounded-full', meta.dot)} />
        </span>
        {!collapsed && (
          <span className="font-mono text-[10px] font-semibold text-zinc-400 uppercase tracking-wider">{meta.label}</span>
        )}
      </div>
    </div>
  )
}

export default Sidebar
