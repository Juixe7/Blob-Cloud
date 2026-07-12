import type { ChangeEvent } from 'react'
import type { BreadcrumbNode } from '../types/file'
import { Breadcrumbs } from './Breadcrumbs'

interface NavbarProps {
  breadcrumbs: BreadcrumbNode[]
  onBreadcrumbNavigate: (index: number) => void
  searchQuery: string
  onSearchChange: (query: string) => void
  viewMode: 'grid' | 'list'
  onViewModeToggle: () => void
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
}: NavbarProps) {
  const handleSearch = (e: ChangeEvent<HTMLInputElement>) => {
    onSearchChange(e.target.value)
  }

  return (
    <header className="flex h-14 shrink-0 items-center gap-4 border-b border-zinc-800 bg-zinc-950 px-6">
      {/* Breadcrumbs */}
      <div className="min-w-0 flex-1">
        <Breadcrumbs nodes={breadcrumbs} onNavigate={onBreadcrumbNavigate} />
      </div>

      {/* Search */}
      <div className="relative w-64">
        <svg
          width="14"
          height="14"
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
          placeholder="Search files…"
          value={searchQuery}
          onChange={handleSearch}
          className="w-full rounded-lg border border-zinc-800 bg-zinc-900/50 py-1.5 pl-9 pr-3 text-sm text-zinc-200 placeholder-zinc-500 transition-all duration-200 focus:outline-none focus:ring-2 focus:ring-violet-500/20 focus:border-violet-500/60"
          aria-label="Search files"
        />
      </div>

      {/* View mode toggle */}
      <div className="flex items-center rounded-lg border border-zinc-800 bg-zinc-900">
        <button
          onClick={onViewModeToggle}
          className="flex items-center justify-center rounded-l-lg px-2.5 py-1.5 transition-colors"
          aria-label="Switch to list view"
          aria-pressed={viewMode === 'list'}
          title="List view"
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
            className={viewMode === 'list' ? 'text-violet-400' : 'text-zinc-500'}
            aria-hidden="true"
          >
            <line x1="3" y1="6" x2="21" y2="6" />
            <line x1="3" y1="12" x2="21" y2="12" />
            <line x1="3" y1="18" x2="21" y2="18" />
          </svg>
        </button>
        <div className="h-5 w-px bg-zinc-800" />
        <button
          onClick={onViewModeToggle}
          className="flex items-center justify-center rounded-r-lg px-2.5 py-1.5 transition-colors"
          aria-label="Switch to grid view"
          aria-pressed={viewMode === 'grid'}
          title="Grid view"
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
            className={viewMode === 'grid' ? 'text-violet-400' : 'text-zinc-500'}
            aria-hidden="true"
          >
            <rect x="3" y="3" width="7" height="7" rx="1" />
            <rect x="14" y="3" width="7" height="7" rx="1" />
            <rect x="3" y="14" width="7" height="7" rx="1" />
            <rect x="14" y="14" width="7" height="7" rx="1" />
          </svg>
        </button>
      </div>
    </header>
  )
}

export default Navbar
