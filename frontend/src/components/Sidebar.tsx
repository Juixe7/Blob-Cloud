import { useState, useRef, useEffect, type ReactElement } from 'react'
import { cn } from '../lib/format'

interface SidebarProps {
  /** Whether the sidebar is in collapsed (icon-only) mode. */
  collapsed: boolean
  onToggleCollapse: () => void
  onNewFolder: () => void
  onUploadFile: () => void
  onSignOut: () => void
  /** Currently used storage in bytes (mocked). */
  storageUsed: number
  /** Total storage limit in bytes (mocked). */
  storageLimit: number
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
  onToggleCollapse,
  onNewFolder,
  onUploadFile,
  onSignOut,
  storageUsed,
  storageLimit,
}: SidebarProps) {
  const [dropdownOpen, setDropdownOpen] = useState(false)
  const dropdownRef = useRef<HTMLDivElement>(null)

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

  const storagePercent = storageLimit > 0 ? Math.min((storageUsed / storageLimit) * 100, 100) : 0
  const storageLabel = `${(storageUsed / 1_073_741_824).toFixed(1)} GB of ${(storageLimit / 1_073_741_824).toFixed(0)} GB used`

  const navItems: NavItem[] = [
    {
      id: 'drive',
      label: 'My Drive',
      active: true,
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z" />
        </svg>
      ),
    },
    {
      id: 'shared',
      label: 'Shared with me',
      disabled: true,
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
      id: 'settings',
      label: 'Settings',
      disabled: true,
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
        'flex h-full flex-col border-r border-zinc-900 bg-zinc-950 transition-all duration-200',
        collapsed ? 'w-16' : 'w-64',
      )}
      aria-label="Sidebar navigation"
    >
      {/* Header: toggle + brand */}
      <div className="flex h-14 items-center border-b border-zinc-900 px-3">
        {!collapsed && (
          <span className="ml-2 text-sm font-semibold text-zinc-50">Blob-Cloud</span>
        )}
        <button
          onClick={onToggleCollapse}
          className={cn(
            'ml-auto rounded-lg p-1.5 text-zinc-500 transition-colors hover:bg-zinc-900 hover:text-zinc-300',
            collapsed && 'mx-auto ml-0',
          )}
          aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
        >
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            {collapsed ? (
              <path d="M9 18l6-6-6-6" />
            ) : (
              <path d="M15 18l-6-6 6-6" />
            )}
          </svg>
        </button>
      </div>

      {/* "+ New" button + dropdown */}
      <div className="relative px-3 pt-4" ref={dropdownRef}>
        <button
          onClick={() => setDropdownOpen((o) => !o)}
          disabled={collapsed}
          className={cn(
            'flex w-full items-center gap-2 rounded-lg bg-violet-600 px-3 py-2 text-sm font-medium text-zinc-50 transition-all duration-200 hover:bg-violet-500 disabled:opacity-40',
            collapsed && 'justify-center px-0',
          )}
          aria-expanded={dropdownOpen}
          aria-haspopup="true"
          aria-label="Create new"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" aria-hidden="true">
            <path d="M12 5v14M5 12h14" />
          </svg>
          {!collapsed && 'New'}
        </button>

        {dropdownOpen && (
          <div
            role="menu"
            className="absolute left-3 z-50 mt-1 w-48 rounded-lg border border-zinc-800 bg-zinc-900 py-1 shadow-xl animate-fade-in"
          >
            <button
              role="menuitem"
              onClick={() => { onNewFolder(); setDropdownOpen(false) }}
              className="flex w-full items-center gap-2 px-3 py-2 text-sm text-zinc-300 transition-colors hover:bg-zinc-800 hover:text-zinc-50"
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" aria-hidden="true">
                <path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z" />
              </svg>
              New Folder
            </button>
            <button
              role="menuitem"
              onClick={() => { onUploadFile(); setDropdownOpen(false) }}
              className="flex w-full items-center gap-2 px-3 py-2 text-sm text-zinc-300 transition-colors hover:bg-zinc-800 hover:text-zinc-50"
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" aria-hidden="true">
                <path d="M12 16V4m0 0l-4 4m4-4l4 4" />
                <path d="M4 17v2a2 2 0 002 2h12a2 2 0 002-2v-2" />
              </svg>
              Upload File
            </button>
          </div>
        )}
      </div>

      {/* Navigation */}
      <nav className="mt-4 flex flex-col gap-1 px-2" aria-label="Main navigation">
        {navItems.map((item) => (
          <button
            key={item.id}
            disabled={item.disabled}
            onClick={() => {/* Phase 7.4+ will wire shared/settings routes */}}
            className={cn(
              'flex items-center gap-3 rounded-lg px-3 py-2 text-sm transition-all duration-200',
              item.active && !item.disabled && 'bg-zinc-900 text-zinc-50',
              !item.active && !item.disabled && 'text-zinc-400 hover:bg-zinc-900 hover:text-zinc-200',
              item.disabled && 'cursor-not-allowed text-zinc-600',
              collapsed && 'justify-center px-0',
            )}
            aria-current={item.active ? 'page' : undefined}
            title={collapsed ? item.label : undefined}
          >
            {item.icon}
            {!collapsed && item.label}
          </button>
        ))}
      </nav>

      {/* Spacer */}
      <div className="flex-1" />

      {/* Storage gauge */}
      {!collapsed && (
        <div className="px-4 pb-4">
          <div className="mb-1.5 flex items-center justify-between text-xs text-zinc-500">
            <span>Storage</span>
            <span>{Math.round(storagePercent)}%</span>
          </div>
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-zinc-900">
            <div
              className="h-full rounded-full bg-violet-500 transition-all duration-300"
              style={{ width: `${storagePercent}%` }}
              role="progressbar"
              aria-valuenow={storagePercent}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-label={storageLabel}
            />
          </div>
          <p className="mt-1 text-[11px] text-zinc-600">{storageLabel}</p>
        </div>
      )}

      {/* Sign-out */}
      <div className="border-t border-zinc-900 px-2 py-3">
        <button
          onClick={onSignOut}
          className={cn(
            'flex w-full items-center gap-3 rounded-lg px-3 py-2 text-sm text-zinc-400 transition-colors hover:bg-zinc-900 hover:text-zinc-200',
            collapsed && 'justify-center px-0',
          )}
          title={collapsed ? 'Sign out' : undefined}
          aria-label="Sign out"
        >
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
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

export default Sidebar
