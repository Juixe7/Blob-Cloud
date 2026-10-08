import { useState } from 'react'
import { Button } from './ui/Button'
import { Spinner } from './ui/Spinner'
import { formatFileSize, cn } from '../lib/format'
import type { ShareInvitation } from '../types/file'
import { FileIcon } from './FileIcon'

interface NotificationsViewProps {
  invitations: ShareInvitation[]
  onAccept: (invitation: ShareInvitation) => Promise<void>
  onDecline: (invitation: ShareInvitation) => Promise<void>
  onBlockSender: (invitation: ShareInvitation) => Promise<void>
  onPreview: (invitation: ShareInvitation) => void
  onAcceptAll?: () => Promise<void>
  onDeclineAll?: () => Promise<void>
  loadingId?: string | null
  isBatchLoading?: boolean
}

function formatExpiresIn(expiresAtStr: string): string {
  const expiresAt = new Date(expiresAtStr).getTime()
  const diffMs = expiresAt - Date.now()
  if (diffMs <= 0) return 'Expired'
  const diffHours = Math.floor(diffMs / (1000 * 60 * 60))
  if (diffHours < 1) {
    const diffMinutes = Math.max(1, Math.floor(diffMs / (1000 * 60)))
    return `Expires in ${diffMinutes}m`
  }
  if (diffHours < 24) {
    return `Expires in ${diffHours} hour${diffHours === 1 ? '' : 's'}`
  }
  const diffDays = Math.ceil(diffHours / 24)
  return `Expires in ${diffDays} day${diffDays === 1 ? '' : 's'}`
}

function initialsFor(email: string): string {
  const local = email.split('@')[0] ?? email
  const parts = local.split(/[.\-_+]/).filter(Boolean)
  if (parts.length === 0) return email.slice(0, 2).toUpperCase()
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase()
  return (parts[0][0] + parts[1][0]).toUpperCase()
}

export function NotificationsView({
  invitations,
  onAccept,
  onDecline,
  onBlockSender,
  onPreview,
  onAcceptAll,
  onDeclineAll,
  loadingId,
  isBatchLoading = false,
}: NotificationsViewProps) {
  const [expandedSummaryIds, setExpandedSummaryIds] = useState<Set<string>>(new Set())
  const [activeDropdownId, setActiveDropdownId] = useState<string | null>(null)

  const toggleSummary = (id: string) => {
    setExpandedSummaryIds((prev) => {
      const next = new Set(prev)
      if (next.has(id)) {
        next.delete(id)
      } else {
        next.add(id)
      }
      return next
    })
  }

  return (
    <div
      className={cn(
        'flex-1 flex flex-col p-4 sm:p-6 max-w-6xl w-full mx-auto animate-fade-in',
        invitations.length === 0 && 'h-full min-h-[65vh] justify-center',
      )}
    >
      {/* Invitations List or Clean Empty State */}
      {invitations.length === 0 ? (
        <div className="flex flex-col items-center justify-center text-center my-auto py-8">
          <div className="relative flex items-center justify-center mb-5">
            <div className="absolute -inset-1.5 rounded-2xl bg-amber-500/10 blur-md pointer-events-none" />
            <div className="relative flex h-16 w-16 items-center justify-center rounded-2xl bg-zinc-900/90 border border-zinc-800 text-zinc-300 shadow-xl ring-1 ring-white/5">
              <svg className="h-8 w-8 text-zinc-300" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
                <path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9" />
                <path d="M13.73 21a2 2 0 0 1-3.46 0" />
              </svg>
            </div>
          </div>
          <h3 className="text-base sm:text-lg font-semibold tracking-tight text-zinc-100 font-display">All caught up</h3>
          <p className="mt-1.5 text-xs sm:text-sm text-zinc-400 max-w-sm mx-auto leading-relaxed">
            You don't have any pending share invitations or notifications right now.
          </p>
        </div>
      ) : (
        <>
          {/* Top Batch Controls (shown only when invitations exist) */}
          <div className="flex items-center justify-between gap-4 pb-4 mb-4 border-b border-zinc-800/80">
            <span className="inline-flex items-center px-2.5 py-1 rounded-full text-xs font-semibold bg-amber-500/15 text-amber-400 border border-amber-500/30">
              {invitations.length} pending invitation{invitations.length === 1 ? '' : 's'}
            </span>

            <div className="flex items-center gap-2.5">
              {onDeclineAll && (
                <Button
                  variant="ghost"
                  onClick={() => void onDeclineAll()}
                  disabled={isBatchLoading || !!loadingId}
                  className="text-xs text-zinc-400 hover:text-rose-400 hover:bg-rose-950/20 border border-zinc-800 hover:border-rose-900/40 px-3 py-1.5 h-8 transition-colors"
                >
                  Decline All
                </Button>
              )}
              {onAcceptAll && (
                <Button
                  onClick={() => void onAcceptAll()}
                  disabled={isBatchLoading || !!loadingId}
                  className="text-xs bg-amber-500 hover:bg-amber-400 text-arch-950 font-semibold px-3.5 py-1.5 h-8 shadow-sm transition-colors flex items-center gap-1.5"
                >
                  {isBatchLoading ? (
                    <Spinner size={14} className="text-arch-950" />
                  ) : (
                    <svg className="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
                      <polyline points="20 6 9 17 4 12" />
                    </svg>
                  )}
                  <span>Accept All ({invitations.length})</span>
                </Button>
              )}
            </div>
          </div>

          <div className="space-y-3">
          {invitations.map((inv) => {
            const isLoading = loadingId === inv.id || isBatchLoading
            const isDropdownOpen = activeDropdownId === inv.id
            const isSummaryExpanded = expandedSummaryIds.has(inv.id)
            const hasSummary = Boolean(inv.summary && inv.summary.trim().length > 0)

            return (
              <div
                key={inv.id}
                className="group relative rounded-xl border border-zinc-800/90 bg-zinc-900/60 shadow-sm transition-all duration-200 hover:border-zinc-700/80 hover:bg-zinc-900/90"
              >
                {/* Main Card Body */}
                <div className="p-3.5 sm:p-4">
                  {/* Fixed Header Row */}
                  <div className="flex items-center justify-between gap-3">
                    <div className="flex items-center gap-2.5 min-w-0 flex-1">
                      <div className="flex-shrink-0">
                        <FileIcon filename={inv.file_name} isDirectory={inv.is_directory} size={22} />
                      </div>
                      <span
                        className="font-medium text-sm text-zinc-100 truncate"
                        title={inv.file_name}
                      >
                        {inv.file_name}
                      </span>
                      <span className="text-xs text-zinc-500 flex-shrink-0">
                        ({formatFileSize(inv.size_bytes)})
                      </span>
                      <span className="flex-shrink-0 rounded bg-zinc-800/90 border border-zinc-700/60 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-zinc-300">
                        {inv.role}
                      </span>
                    </div>

                    {/* Expiration Indicator (Neutral Sleek Clock) */}
                    <div className="flex-shrink-0 flex items-center gap-1.5 text-xs text-zinc-400 font-normal">
                      <svg className="h-3.5 w-3.5 text-zinc-500" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                        <circle cx="12" cy="12" r="10" />
                        <polyline points="12 6 12 12 16 14" />
                      </svg>
                      <span>{formatExpiresIn(inv.expires_at)}</span>
                    </div>
                  </div>

                  {/* Sub-header / Sender Row */}
                  <div className="mt-2 flex flex-wrap items-center gap-1.5 text-xs text-zinc-400 pl-8">
                    <span className="flex h-4 w-4 items-center justify-center rounded-full bg-zinc-800 border border-zinc-700/70 text-[9px] font-bold text-zinc-300">
                      {initialsFor(inv.sender_email)}
                    </span>
                    <span>Shared by</span>
                    <span className="font-medium text-zinc-200">{inv.sender_email}</span>

                    {inv.message && (
                      <>
                        <span className="text-zinc-600">•</span>
                        <span className="italic text-zinc-400">"{inv.message}"</span>
                      </>
                    )}
                  </div>

                  {/* Uniform Action Footer */}
                  <div className="mt-3.5 flex items-center justify-between gap-3 pt-3 border-t border-zinc-800/60 pl-8">
                    {/* Left: Summary Toggle Chip */}
                    <div>
                      {hasSummary ? (
                        <button
                          type="button"
                          onClick={() => toggleSummary(inv.id)}
                          className={cn(
                            'inline-flex items-center gap-1.5 px-2.5 py-1 rounded text-xs font-medium transition-colors border',
                            isSummaryExpanded
                              ? 'bg-zinc-800 text-amber-400 border-amber-500/30'
                              : 'bg-zinc-800/60 text-zinc-300 border-zinc-700/60 hover:bg-zinc-800 hover:text-white'
                          )}
                        >
                          <svg className="h-3 w-3 text-amber-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                            <polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2" />
                          </svg>
                          <span>Summary</span>
                          <svg
                            className={cn('h-3 w-3 text-zinc-400 transition-transform duration-200', isSummaryExpanded && 'rotate-180')}
                            viewBox="0 0 24 24"
                            fill="none"
                            stroke="currentColor"
                            strokeWidth="2"
                          >
                            <polyline points="6 9 12 15 18 9" />
                          </svg>
                        </button>
                      ) : (
                        <div />
                      )}
                    </div>

                    {/* Right: Action Buttons Hierarchy */}
                    <div className="flex items-center gap-2">
                      <Button
                        variant="ghost"
                        onClick={() => onPreview(inv)}
                        disabled={isLoading}
                        title="Safe sandboxed preview"
                        className="px-2.5 py-1.5 text-xs text-zinc-400 hover:text-zinc-200 hover:bg-zinc-800/70 h-7"
                      >
                        <svg className="h-3.5 w-3.5 mr-1 text-zinc-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                          <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
                          <circle cx="12" cy="12" r="3" />
                        </svg>
                        Preview
                      </Button>

                      <Button
                        variant="ghost"
                        onClick={() => void onDecline(inv)}
                        disabled={isLoading}
                        title="Decline invitation"
                        className="px-2.5 py-1.5 text-xs text-zinc-400 hover:text-rose-400 hover:bg-rose-950/20 border border-transparent hover:border-rose-900/30 h-7 transition-colors"
                      >
                        Decline
                      </Button>

                      <Button
                        onClick={() => void onAccept(inv)}
                        disabled={isLoading}
                        className="px-3 py-1.5 text-xs bg-amber-500 hover:bg-amber-400 text-arch-950 font-semibold h-7 shadow-xs flex items-center gap-1.5 transition-colors"
                      >
                        {isLoading ? (
                          <Spinner size={12} className="text-arch-950" />
                        ) : (
                          <svg className="h-3 w-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
                            <polyline points="20 6 9 17 4 12" />
                          </svg>
                        )}
                        <span>Accept</span>
                      </Button>

                      {/* More Menu (Block Sender) */}
                      <div className="relative">
                        <button
                          type="button"
                          onClick={() => setActiveDropdownId(isDropdownOpen ? null : inv.id)}
                          className="rounded-lg p-1 text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200 transition-colors h-7 w-7 flex items-center justify-center"
                          title="More options"
                        >
                          <svg className="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                            <circle cx="12" cy="12" r="1" />
                            <circle cx="12" cy="5" r="1" />
                            <circle cx="12" cy="19" r="1" />
                          </svg>
                        </button>

                        {isDropdownOpen && (
                          <div className="absolute right-0 top-full mt-1 w-44 rounded-lg border border-zinc-800 bg-zinc-900 py-1 shadow-xl z-20">
                            <button
                              type="button"
                              onClick={() => {
                                setActiveDropdownId(null)
                                void onBlockSender(inv)
                              }}
                              className="flex w-full items-center gap-2 px-3 py-2 text-xs text-rose-400 hover:bg-rose-500/10 transition-colors text-left"
                            >
                              <svg className="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                                <circle cx="12" cy="12" r="10" />
                                <line x1="4.93" y1="4.93" x2="19.07" y2="19.07" />
                              </svg>
                              <span>Block Sender</span>
                            </button>
                          </div>
                        )}
                      </div>
                    </div>
                  </div>
                </div>

                {/* Collapsible Accordion Drawer (AI Summary) */}
                {hasSummary && isSummaryExpanded && (
                  <div className="border-t border-zinc-800/80 bg-zinc-950/50 px-4 py-3 rounded-b-xl transition-all duration-200">
                    <div className="flex items-start gap-2">
                      <span className="text-[10px] uppercase font-semibold tracking-wider text-amber-400/90 bg-amber-400/10 border border-amber-400/20 px-1.5 py-0.5 rounded flex-shrink-0">
                        AI Digest
                      </span>
                      <p className="text-xs text-zinc-300 leading-relaxed">
                        {inv.summary}
                      </p>
                    </div>
                  </div>
                )}
              </div>
            )
          })}
        </div>
        </>
      )}
    </div>
  )
}
