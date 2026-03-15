import type { ChangeEvent } from 'react'
import type { BreadcrumbNode } from '../types/file'
import { Breadcrumbs } from './Breadcrumbs'
import { cn } from '../lib/format'

interface NavbarProps {
  breadcrumbs: BreadcrumbNode[]
  onBreadcrumbNavigate: (index: number) => void
  searchQuery: string
  onSearchChange: (query: string) => void
  viewMode: 'grid' | 'list'
  onViewModeToggle: () => void
  isDetailsOpen?: boolean
  onToggleDetails?: () => void
  onToggleMobileSidebar?: () => void
}

/**
 * Top navigation bar containing the breadcrumb trail, search input, and
 * grid/list view toggle.
 */
export function Navbar({
  breadcrumbs,
  onBreadcrumbNavigate,
  searchQuery,
  onSearchChange,
  viewMode,
  onViewModeToggle,
  isDetailsOpen,
  onToggleDetails,
  onToggleMobileSidebar,
}: NavbarProps) {
  const handleSearch = (e: ChangeEvent<HTMLInputElement>) => {
    onSearchChange(e.target.value)
  }

  return (
    <header className="flex h-14 shrink-0 items-center gap-2 sm:gap-4 border-b border-arch-border bg-arch-900 px-3 sm:px-6 select-none">
      {/* Mobile Hamburger */}
      <button
        onClick={onToggleMobileSidebar}
        className="md:hidden flex h-8 w-8 items-center justify-center rounded text-zinc-400 hover:bg-arch-800 hover:text-zinc-100 transition-colors"
        aria-label="Open sidebar"
      >
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <line x1="3" y1="12" x2="21" y2="12"></line>
          <line x1="3" y1="6" x2="21" y2="6"></line>
          <line x1="3" y1="18" x2="21" y2="18"></line>
        </svg>
      </button>

      {/* Breadcrumbs */}
      <div className="min-w-0 flex-1 overflow-x-auto overflow-y-hidden scrollbar-hide flex items-center h-full">
        <Breadcrumbs nodes={breadcrumbs} onNavigate={onBreadcrumbNavigate} />
      </div>

      {/* Search Bar with Key Shortcut Pill */}
      <div className="relative w-32 focus-within:w-48 sm:w-48 sm:focus-within:w-64 md:w-64 shrink-0 transition-all duration-300">
        <svg
          width="13"
          height="13"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-zinc-500"
          aria-hidden="true"
        >
          <circle cx="11" cy="11" r="8" />
          <line x1="21" y1="21" x2="16.65" y2="16.65" />
        </svg>
        <input
          type="search"
          placeholder="Search files..."
          value={searchQuery}
          onChange={handleSearch}
          className="w-full rounded border border-arch-border bg-arch-950 py-1.5 pl-8 pr-7 font-sans text-xs text-zinc-100 placeholder-zinc-500 transition-colors focus:border-amber-500/80 focus:outline-none focus:ring-1 focus:ring-amber-500/30"
          aria-label="Search files"
        />
        <kbd className="hidden md:block pointer-events-none absolute right-2.5 top-1/2 -translate-y-1/2 font-mono text-[10px] font-semibold text-zinc-500 bg-arch-850 border border-arch-700 rounded px-1">
          /
        </kbd>
      </div>

      {/* View Mode Toggle */}
      <div className="flex items-center rounded border border-arch-border bg-arch-950 p-0.5">
        <button
          onClick={onViewModeToggle}
          className={cn(
            'flex items-center justify-center rounded px-2 py-1 transition-colors',
            viewMode === 'list' ? 'bg-arch-850 text-amber-400 font-semibold shadow-xs' : 'text-zinc-500 hover:text-zinc-300',
          )}
          aria-label="Switch to list view"
          aria-pressed={viewMode === 'list'}
          title="List view"
        >
          <svg
            width="15"
            height="15"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.75"
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden="true"
          >
            <line x1="3" y1="6" x2="21" y2="6" />
            <line x1="3" y1="12" x2="21" y2="12" />
            <line x1="3" y1="18" x2="21" y2="18" />
          </svg>
        </button>
        <button
          onClick={onViewModeToggle}
          className={cn(
            'flex items-center justify-center rounded px-2 py-1 transition-colors',
            viewMode === 'grid' ? 'bg-arch-850 text-amber-400 font-semibold shadow-xs' : 'text-zinc-500 hover:text-zinc-300',
          )}
          aria-label="Switch to grid view"
          aria-pressed={viewMode === 'grid'}
          title="Grid view"
        >
          <svg
            width="15"
            height="15"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.75"
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden="true"
          >
            <rect x="3" y="3" width="7" height="7" rx="1" />
            <rect x="14" y="3" width="7" height="7" rx="1" />
            <rect x="3" y="14" width="7" height="7" rx="1" />
          </svg>
        </button>
      </div>

      {/* Detail Panel Toggle */}
      {onToggleDetails && (
        <button
          onClick={onToggleDetails}
          className={cn(
            'flex h-7 w-7 items-center justify-center rounded transition-colors',
            isDetailsOpen
              ? 'bg-violet-500/10 text-violet-500 shadow-xs ring-1 ring-violet-500/30'
              : 'text-zinc-500 hover:text-zinc-300 hover:bg-arch-850',
          )}
          aria-label="Toggle details panel"
          title="View Details"
        >
          <svg
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
          >
            <circle cx="12" cy="12" r="10" />
            <line x1="12" y1="16" x2="12" y2="12" />
            <line x1="12" y1="8" x2="12.01" y2="8" />
          </svg>
        </button>
      )}
    </header>
  )
}

export default Navbar
