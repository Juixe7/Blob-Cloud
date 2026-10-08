import { useState, useRef, useEffect, useMemo } from 'react'
import { cn } from '../lib/format'

export type TypeFilter = 'all' | 'folders' | 'documents' | 'spreadsheets' | 'pdfs' | 'images' | 'videos' | 'audio'
export type DateFilter = 'all' | 'today' | '7days' | '30days' | 'this_year'
export type SortField = 'name' | 'date' | 'size'
export type SortDirection = 'asc' | 'desc'

export interface FilterChipsBarProps {
  typeFilter: TypeFilter
  onTypeFilterChange: (type: TypeFilter) => void
  personFilter: string | null
  onPersonFilterChange: (person: string | null) => void
  dateFilter: DateFilter
  onDateFilterChange: (date: DateFilter) => void
  availablePeople: string[]
  totalItems: number
  filteredItemsCount: number
  onResetFilters: () => void
  sortField?: SortField
  onSortFieldChange?: (field: SortField) => void
  sortDirection?: SortDirection
  onToggleSortDirection?: () => void
  isSharedView?: boolean
  className?: string
}

function getAvatarColor(email: string) {
  const colors = [
    { bg: 'bg-zinc-800', text: 'text-zinc-200', border: 'border-zinc-700' },
    { bg: 'bg-blue-900/60', text: 'text-blue-300', border: 'border-blue-700/60' },
    { bg: 'bg-emerald-900/60', text: 'text-emerald-300', border: 'border-emerald-700/60' },
    { bg: 'bg-sky-900/60', text: 'text-sky-300', border: 'border-sky-700/60' },
    { bg: 'bg-indigo-900/60', text: 'text-indigo-300', border: 'border-indigo-700/60' },
    { bg: 'bg-rose-900/60', text: 'text-rose-300', border: 'border-rose-700/60' },
    { bg: 'bg-teal-900/60', text: 'text-teal-300', border: 'border-teal-700/60' },
  ]
  let hash = 0
  for (let i = 0; i < email.length; i++) {
    hash = (hash << 5) - hash + email.charCodeAt(i)
    hash |= 0
  }
  return colors[Math.abs(hash) % colors.length]
}

export function FilterChipsBar({
  typeFilter,
  onTypeFilterChange,
  personFilter,
  onPersonFilterChange,
  dateFilter,
  onDateFilterChange,
  availablePeople,
  totalItems,
  filteredItemsCount,
  onResetFilters,
  sortField,
  onSortFieldChange,
  sortDirection = 'asc',
  onToggleSortDirection,
  isSharedView,
  className,
}: FilterChipsBarProps) {
  const [openDropdown, setOpenDropdown] = useState<'type' | 'people' | 'date' | 'sort' | null>(null)
  const [peopleSearch, setPeopleSearch] = useState('')
  const barRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    function handlePointerDown(e: PointerEvent) {
      if (barRef.current && !barRef.current.contains(e.target as Node)) {
        setOpenDropdown(null)
      }
    }
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') setOpenDropdown(null)
    }
    document.addEventListener('pointerdown', handlePointerDown)
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('pointerdown', handlePointerDown)
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [])

  const hasActiveFilters = typeFilter !== 'all' || personFilter !== null || dateFilter !== 'all'

  const typeLabels: Record<TypeFilter, string> = {
    all: 'All types',
    folders: 'Folders',
    documents: 'Documents',
    spreadsheets: 'Spreadsheets',
    pdfs: 'PDFs',
    images: 'Images',
    videos: 'Videos',
    audio: 'Audio',
  }

  const dateLabels: Record<DateFilter, string> = {
    all: 'Any time',
    today: 'Today',
    '7days': 'Last 7 days',
    '30days': 'Last 30 days',
    this_year: 'This year',
  }

  const sortFieldLabels: Record<SortField, string> = {
    name: 'Name',
    date: isSharedView ? 'Date shared' : 'Last modified',
    size: 'File size',
  }

  const filteredPeople = useMemo(() => {
    if (!peopleSearch.trim()) return availablePeople
    const q = peopleSearch.toLowerCase().trim()
    return availablePeople.filter((p) => p.toLowerCase().includes(q))
  }, [availablePeople, peopleSearch])

  return (
    <div
      ref={barRef}
      className={cn(
        'relative z-30 overflow-visible flex flex-wrap items-center gap-2 px-4 py-2 border-b border-arch-border bg-arch-950/90 backdrop-blur-md text-xs shrink-0 select-none',
        className
      )}
    >
      {/* ----------------- 1. Type Filter Chip ----------------- */}
      <div className="relative inline-block">
        <button
          type="button"
          onClick={() => setOpenDropdown((prev) => (prev === 'type' ? null : 'type'))}
          className={cn(
            'inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full text-xs font-medium border transition-colors cursor-pointer',
            typeFilter !== 'all'
              ? 'bg-zinc-800 border-zinc-600 text-zinc-100 shadow-xs'
              : 'bg-arch-900 border-arch-border text-zinc-300 hover:bg-arch-850 hover:text-white'
          )}
          aria-expanded={openDropdown === 'type'}
        >
          <span>{typeFilter !== 'all' ? `Type: ${typeLabels[typeFilter]}` : 'Type'}</span>
          {typeFilter !== 'all' ? (
            <span
              onClick={(e) => {
                e.stopPropagation()
                onTypeFilterChange('all')
              }}
              className="ml-0.5 text-zinc-400 hover:text-white rounded-full p-0.5 hover:bg-zinc-700/80 transition-colors"
              title="Clear type filter"
            >
              ✕
            </span>
          ) : (
            <svg
              width="12"
              height="12"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
              className={cn('text-zinc-500 transition-transform', openDropdown === 'type' && 'rotate-180')}
            >
              <polyline points="6 9 12 15 18 9" />
            </svg>
          )}
        </button>

        {openDropdown === 'type' && (
          <div className="absolute left-0 top-full mt-1.5 w-52 rounded-xl bg-zinc-900 border border-zinc-700/80 shadow-2xl py-1.5 z-50 animate-in fade-in zoom-in-95 duration-100">
            <div className="px-3 py-1 text-[10px] font-semibold uppercase tracking-wider text-zinc-500 border-b border-zinc-800 mb-1">
              Filter by Type
            </div>
            {(['all', 'pdfs', 'documents', 'spreadsheets', 'images', 'videos', 'audio', 'folders'] as TypeFilter[]).map((t) => (
              <button
                key={t}
                type="button"
                onClick={() => {
                  onTypeFilterChange(t)
                  setOpenDropdown(null)
                }}
                className={cn(
                  'w-full flex items-center justify-between px-3 py-2 text-xs text-left transition-colors cursor-pointer',
                  typeFilter === t
                    ? 'bg-zinc-800 text-white font-medium'
                    : 'text-zinc-300 hover:bg-zinc-850 hover:text-white'
                )}
              >
                <div className="flex items-center gap-2.5">
                  {t === 'all' && (
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-zinc-400">
                      <polygon points="12 2 2 7 12 12 22 7 12 2" />
                      <polyline points="2 17 12 22 22 17" />
                      <polyline points="2 12 12 17 22 12" />
                    </svg>
                  )}
                  {t === 'pdfs' && (
                    <span className="w-3.5 h-3.5 rounded bg-red-600/80 text-[8px] font-bold text-white flex items-center justify-center">
                      P
                    </span>
                  )}
                  {t === 'documents' && (
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-blue-400">
                      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
                      <polyline points="14 2 14 8 20 8" />
                      <line x1="16" y1="13" x2="8" y2="13" />
                      <line x1="16" y1="17" x2="8" y2="17" />
                    </svg>
                  )}
                  {t === 'spreadsheets' && (
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-emerald-400">
                      <rect width="18" height="18" x="3" y="3" rx="2" />
                      <path d="M3 9h18" />
                      <path d="M3 15h18" />
                      <path d="M9 3v18" />
                      <path d="M15 3v18" />
                    </svg>
                  )}
                  {t === 'images' && (
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-pink-400">
                      <rect width="18" height="18" x="3" y="3" rx="2" />
                      <circle cx="9" cy="9" r="2" />
                      <path d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21" />
                    </svg>
                  )}
                  {t === 'videos' && (
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-purple-400">
                      <polygon points="23 7 16 12 23 17 23 7" />
                      <rect width="14" height="14" x="1" y="5" rx="2" />
                    </svg>
                  )}
                  {t === 'audio' && (
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-amber-400">
                      <path d="M9 18V5l12-2v13" />
                      <circle cx="6" cy="18" r="3" />
                      <circle cx="18" cy="16" r="3" />
                    </svg>
                  )}
                  {t === 'folders' && (
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-zinc-400">
                      <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
                    </svg>
                  )}
                  <span>{typeLabels[t]}</span>
                </div>
                {typeFilter === t && (
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" className="text-zinc-300">
                    <polyline points="20 6 9 17 4 12" />
                  </svg>
                )}
              </button>
            ))}
          </div>
        )}
      </div>

      {/* ----------------- 2. People Filter Chip ----------------- */}
      <div className="relative inline-block">
        <button
          type="button"
          onClick={() => {
            setOpenDropdown((prev) => (prev === 'people' ? null : 'people'))
            setPeopleSearch('')
          }}
          className={cn(
            'inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full text-xs font-medium border transition-colors cursor-pointer',
            personFilter !== null
              ? 'bg-zinc-800 border-zinc-600 text-zinc-100 shadow-xs'
              : 'bg-arch-900 border-arch-border text-zinc-300 hover:bg-arch-850 hover:text-white'
          )}
          aria-expanded={openDropdown === 'people'}
        >
          <span className="truncate max-w-[150px]">{personFilter !== null ? `People: ${personFilter}` : 'People'}</span>
          {personFilter !== null ? (
            <span
              onClick={(e) => {
                e.stopPropagation()
                onPersonFilterChange(null)
              }}
              className="ml-0.5 text-zinc-400 hover:text-white rounded-full p-0.5 hover:bg-zinc-700/80 transition-colors"
              title="Clear people filter"
            >
              ✕
            </span>
          ) : (
            <svg
              width="12"
              height="12"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
              className={cn('text-zinc-500 transition-transform', openDropdown === 'people' && 'rotate-180')}
            >
              <polyline points="6 9 12 15 18 9" />
            </svg>
          )}
        </button>

        {openDropdown === 'people' && (
          <div className="absolute left-0 top-full mt-1.5 w-72 rounded-xl bg-zinc-900 border border-zinc-700/80 shadow-2xl py-2 z-50 animate-in fade-in zoom-in-95 duration-100">
            <div className="px-3 pb-2 border-b border-zinc-800">
              <div className="relative">
                <input
                  type="text"
                  placeholder="Search by email..."
                  value={peopleSearch}
                  onChange={(e) => setPeopleSearch(e.target.value)}
                  onClick={(e) => e.stopPropagation()}
                  className="w-full bg-zinc-950 border border-zinc-700 rounded-lg px-2.5 py-1.5 text-xs text-zinc-200 placeholder-zinc-500 focus:outline-none focus:border-zinc-500"
                  autoFocus
                />
                {peopleSearch && (
                  <button
                    type="button"
                    onClick={() => setPeopleSearch('')}
                    className="absolute right-2 top-2 text-zinc-400 hover:text-zinc-200 cursor-pointer"
                  >
                    ✕
                  </button>
                )}
              </div>
            </div>

            <div className="max-h-60 overflow-y-auto py-1">
              {/* Option: Anyone / All people */}
              <button
                type="button"
                onClick={() => {
                  onPersonFilterChange(null)
                  setOpenDropdown(null)
                }}
                className={cn(
                  'w-full flex items-center justify-between px-3 py-2 text-xs text-left transition-colors cursor-pointer',
                  personFilter === null
                    ? 'bg-zinc-800 text-white font-medium'
                    : 'text-zinc-300 hover:bg-zinc-850 hover:text-white'
                )}
              >
                <div className="flex items-center gap-2.5">
                  <span className="w-6 h-6 rounded-full bg-zinc-800 border border-zinc-700 flex items-center justify-center text-[10px] font-bold text-zinc-400">
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                      <path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2" />
                      <circle cx="9" cy="7" r="4" />
                      <path d="M22 21v-2a4 4 0 0 0-3-3.87" />
                      <path d="M16 3.13a4 4 0 0 1 0 7.75" />
                    </svg>
                  </span>
                  <span>All people</span>
                </div>
                {personFilter === null && (
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" className="text-zinc-300">
                    <polyline points="20 6 9 17 4 12" />
                  </svg>
                )}
              </button>

              <div className="my-1 h-px bg-zinc-800" />

              {/* People list */}
              {filteredPeople.length > 0 ? (
                filteredPeople.map((person) => {
                  const color = getAvatarColor(person)
                  const initial = person.charAt(0).toUpperCase()
                  return (
                    <button
                      key={person}
                      type="button"
                      onClick={() => {
                        onPersonFilterChange(person)
                        setOpenDropdown(null)
                      }}
                      className={cn(
                        'w-full flex items-center justify-between px-3 py-2 text-xs text-left transition-colors cursor-pointer',
                        personFilter === person
                          ? 'bg-zinc-800 text-white font-medium'
                          : 'text-zinc-300 hover:bg-zinc-850 hover:text-white'
                      )}
                      title={person}
                    >
                      <div className="flex items-center gap-2.5 min-w-0">
                        <span
                          className={cn(
                            'w-6 h-6 shrink-0 rounded-full border flex items-center justify-center text-[10px] font-bold',
                            color.bg,
                            color.text,
                            color.border
                          )}
                        >
                          {initial}
                        </span>
                        <span className="truncate">{person}</span>
                      </div>
                      {personFilter === person && (
                        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" className="text-zinc-300 shrink-0 ml-2">
                          <polyline points="20 6 9 17 4 12" />
                        </svg>
                      )}
                    </button>
                  )
                })
              ) : (
                <div className="px-3 py-3 text-center text-xs text-zinc-500">
                  {peopleSearch.trim() ? (
                    <div>
                      <p>No contact found with &ldquo;{peopleSearch}&rdquo;</p>
                      <button
                        type="button"
                        onClick={() => {
                          onPersonFilterChange(peopleSearch.trim())
                          setOpenDropdown(null)
                        }}
                        className="mt-2 text-xs font-medium text-zinc-300 hover:text-white underline cursor-pointer"
                      >
                        Filter by &ldquo;{peopleSearch.trim()}&rdquo;
                      </button>
                    </div>
                  ) : (
                    'No shared contacts found'
                  )}
                </div>
              )}
            </div>
          </div>
        )}
      </div>

      {/* ----------------- 3. Modified Filter Chip ----------------- */}
      <div className="relative inline-block">
        <button
          type="button"
          onClick={() => setOpenDropdown((prev) => (prev === 'date' ? null : 'date'))}
          className={cn(
            'inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full text-xs font-medium border transition-colors cursor-pointer',
            dateFilter !== 'all'
              ? 'bg-zinc-800 border-zinc-600 text-zinc-100 shadow-xs'
              : 'bg-arch-900 border-arch-border text-zinc-300 hover:bg-arch-850 hover:text-white'
          )}
          aria-expanded={openDropdown === 'date'}
        >
          <span>{dateFilter !== 'all' ? `Modified: ${dateLabels[dateFilter]}` : 'Modified'}</span>
          {dateFilter !== 'all' ? (
            <span
              onClick={(e) => {
                e.stopPropagation()
                onDateFilterChange('all')
              }}
              className="ml-0.5 text-zinc-400 hover:text-white rounded-full p-0.5 hover:bg-zinc-700/80 transition-colors"
              title="Clear date filter"
            >
              ✕
            </span>
          ) : (
            <svg
              width="12"
              height="12"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
              className={cn('text-zinc-500 transition-transform', openDropdown === 'date' && 'rotate-180')}
            >
              <polyline points="6 9 12 15 18 9" />
            </svg>
          )}
        </button>

        {openDropdown === 'date' && (
          <div className="absolute left-0 top-full mt-1.5 w-48 rounded-xl bg-zinc-900 border border-zinc-700/80 shadow-2xl py-1.5 z-50 animate-in fade-in zoom-in-95 duration-100">
            <div className="px-3 py-1 text-[10px] font-semibold uppercase tracking-wider text-zinc-500 border-b border-zinc-800 mb-1">
              Date Modified
            </div>
            {(['all', 'today', '7days', '30days', 'this_year'] as DateFilter[]).map((d) => (
              <button
                key={d}
                type="button"
                onClick={() => {
                  onDateFilterChange(d)
                  setOpenDropdown(null)
                }}
                className={cn(
                  'w-full flex items-center justify-between px-3 py-2 text-xs text-left transition-colors cursor-pointer',
                  dateFilter === d
                    ? 'bg-zinc-800 text-white font-medium'
                    : 'text-zinc-300 hover:bg-zinc-850 hover:text-white'
                )}
              >
                <div className="flex items-center gap-2">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-zinc-400">
                    <circle cx="12" cy="12" r="10" />
                    <polyline points="12 6 12 12 16 14" />
                  </svg>
                  <span>{dateLabels[d]}</span>
                </div>
                {dateFilter === d && (
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" className="text-zinc-300">
                    <polyline points="20 6 9 17 4 12" />
                  </svg>
                )}
              </button>
            ))}
          </div>
        )}
      </div>

      {/* ----------------- Clear All Filters Button (Renamed & Sleek Zinc Aesthetic) ----------------- */}
      {hasActiveFilters && (
        <button
          type="button"
          onClick={onResetFilters}
          className="inline-flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium text-zinc-300 hover:text-white bg-zinc-800/90 hover:bg-zinc-700 border border-zinc-700 hover:border-zinc-600 rounded-full transition-colors cursor-pointer shadow-xs"
          title="Clear all active filters"
        >
          <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" className="text-zinc-400">
            <line x1="18" y1="6" x2="6" y2="18" />
            <line x1="6" y1="6" x2="18" y2="18" />
          </svg>
          Clear
        </button>
      )}

      {/* ----------------- Divider ----------------- */}
      <div className="hidden sm:block h-4 w-px bg-zinc-800/80 mx-1 shrink-0" />

      {/* ----------------- 4. Sort Controls (Field + Asc/Desc) ----------------- */}
      {sortField && onSortFieldChange && (
        <div className="relative inline-flex items-center gap-1">
          <button
            type="button"
            onClick={() => setOpenDropdown((prev) => (prev === 'sort' ? null : 'sort'))}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full text-xs font-medium border bg-arch-900 border-arch-border text-zinc-300 hover:bg-arch-850 hover:text-white transition-colors cursor-pointer"
            aria-expanded={openDropdown === 'sort'}
          >
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="text-zinc-400">
              <line x1="4" y1="6" x2="20" y2="6" />
              <line x1="4" y1="12" x2="14" y2="12" />
              <line x1="4" y1="18" x2="8" y2="18" />
            </svg>
            <span>Sort: {sortFieldLabels[sortField]}</span>
            <svg
              width="12"
              height="12"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
              className={cn('text-zinc-500 transition-transform', openDropdown === 'sort' && 'rotate-180')}
            >
              <polyline points="6 9 12 15 18 9" />
            </svg>
          </button>

          {onToggleSortDirection && (
            <button
              type="button"
              onClick={onToggleSortDirection}
              className="inline-flex items-center justify-center w-7 h-7 rounded-full border border-arch-border bg-arch-900 text-zinc-300 hover:bg-arch-850 hover:text-white transition-colors cursor-pointer"
              title={sortDirection === 'asc' ? 'Ascending (click to switch to descending)' : 'Descending (click to switch to ascending)'}
              aria-label="Toggle sort direction"
            >
              {sortDirection === 'asc' ? (
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                  <line x1="12" y1="19" x2="12" y2="5" />
                  <polyline points="5 12 12 5 19 12" />
                </svg>
              ) : (
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                  <line x1="12" y1="5" x2="12" y2="19" />
                  <polyline points="19 12 12 19 5 12" />
                </svg>
              )}
            </button>
          )}

          {openDropdown === 'sort' && (
            <div className="absolute left-0 top-full mt-1.5 w-52 rounded-xl bg-zinc-900 border border-zinc-700/80 shadow-2xl py-1.5 z-50 animate-in fade-in zoom-in-95 duration-100">
              <div className="px-3 py-1 text-[10px] font-semibold uppercase tracking-wider text-zinc-500 border-b border-zinc-800 mb-1">
                Sort By
              </div>
              {(['name', 'date', 'size'] as SortField[]).map((f) => (
                <button
                  key={f}
                  type="button"
                  onClick={() => {
                    onSortFieldChange(f)
                    setOpenDropdown(null)
                  }}
                  className={cn(
                    'w-full flex items-center justify-between px-3 py-2 text-xs text-left transition-colors cursor-pointer',
                    sortField === f
                      ? 'bg-zinc-800 text-white font-medium'
                      : 'text-zinc-300 hover:bg-zinc-850 hover:text-white'
                  )}
                >
                  <span>{sortFieldLabels[f]}</span>
                  {sortField === f && (
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" className="text-zinc-300">
                      <polyline points="20 6 9 17 4 12" />
                    </svg>
                  )}
                </button>
              ))}

              <div className="my-1 border-t border-zinc-800" />
              <div className="px-3 py-1 text-[10px] font-semibold uppercase tracking-wider text-zinc-500 mb-1">
                Order
              </div>
              <button
                type="button"
                onClick={() => {
                  if (sortDirection !== 'asc') onToggleSortDirection?.()
                  setOpenDropdown(null)
                }}
                className={cn(
                  'w-full flex items-center justify-between px-3 py-2 text-xs text-left transition-colors cursor-pointer',
                  sortDirection === 'asc'
                    ? 'bg-zinc-800 text-white font-medium'
                    : 'text-zinc-300 hover:bg-zinc-850 hover:text-white'
                )}
              >
                <div className="flex items-center gap-2">
                  <span className="font-mono text-xs">↑</span>
                  <span>Ascending {sortField === 'name' ? '(A to Z)' : sortField === 'date' ? '(Oldest first)' : '(Smallest first)'}</span>
                </div>
                {sortDirection === 'asc' && (
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" className="text-zinc-300">
                    <polyline points="20 6 9 17 4 12" />
                  </svg>
                )}
              </button>
              <button
                type="button"
                onClick={() => {
                  if (sortDirection !== 'desc') onToggleSortDirection?.()
                  setOpenDropdown(null)
                }}
                className={cn(
                  'w-full flex items-center justify-between px-3 py-2 text-xs text-left transition-colors cursor-pointer',
                  sortDirection === 'desc'
                    ? 'bg-zinc-800 text-white font-medium'
                    : 'text-zinc-300 hover:bg-zinc-850 hover:text-white'
                )}
              >
                <div className="flex items-center gap-2">
                  <span className="font-mono text-xs">↓</span>
                  <span>Descending {sortField === 'name' ? '(Z to A)' : sortField === 'date' ? '(Newest first)' : '(Largest first)'}</span>
                </div>
                {sortDirection === 'desc' && (
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" className="text-zinc-300">
                    <polyline points="20 6 9 17 4 12" />
                  </svg>
                )}
              </button>
            </div>
          )}
        </div>
      )}

      {/* ----------------- Filtered Count indicator ----------------- */}
      <span className="ml-auto text-[11px] font-mono text-zinc-500 hidden sm:inline-block">
        {hasActiveFilters
          ? `${filteredItemsCount} of ${totalItems} items`
          : `${totalItems} item${totalItems === 1 ? '' : 's'}`}
      </span>
    </div>
  )
}
