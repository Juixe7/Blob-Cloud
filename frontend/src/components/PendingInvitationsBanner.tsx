import { useState } from 'react'
import { Button } from './ui/Button'
import { Spinner } from './ui/Spinner'
import { formatFileSize, cn } from '../lib/format'
import type { ShareInvitation } from '../types/file'
import { FileIcon } from './FileIcon'

interface PendingInvitationsBannerProps {
  invitations: ShareInvitation[]
  onAccept: (invitation: ShareInvitation) => Promise<void>
  onDecline: (invitation: ShareInvitation) => Promise<void>
  onBlockSender: (invitation: ShareInvitation) => Promise<void>
  onPreview: (invitation: ShareInvitation) => void
  loadingId?: string | null
}

function formatExpiresIn(expiresAtStr: string): string {
  const expiresAt = new Date(expiresAtStr).getTime()
  const diffMs = expiresAt - Date.now()
  if (diffMs <= 0) return 'Expired'
  const diffHours = Math.floor(diffMs / (1000 * 60 * 60))
  if (diffHours < 24) {
    return `Expires in ${Math.max(1, diffHours)}h`
  }
  const diffDays = Math.ceil(diffHours / 24)
  return `Expires in ${diffDays}d`
}

function initialsFor(email: string): string {
  const local = email.split('@')[0] ?? email
  const parts = local.split(/[.\-_+]/).filter(Boolean)
  if (parts.length === 0) return email.slice(0, 2).toUpperCase()
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase()
  return (parts[0][0] + parts[1][0]).toUpperCase()
}

export function PendingInvitationsBanner({
  invitations,
  onAccept,
  onDecline,
  onBlockSender,
  onPreview,
  loadingId,
}: PendingInvitationsBannerProps) {
  const [collapsed, setCollapsed] = useState(false)
  const [activeDropdownId, setActiveDropdownId] = useState<string | null>(null)

  if (invitations.length === 0) return null

  return (
    <div className="mb-6 overflow-hidden rounded-xl border border-indigo-500/30 bg-gradient-to-r from-indigo-950/40 via-zinc-900/60 to-purple-950/30 shadow-lg shadow-indigo-950/20 backdrop-blur-sm">
      {/* Header Banner */}
      <div className="flex items-center justify-between px-4 py-3 border-b border-indigo-500/20 bg-indigo-950/30">
        <div className="flex items-center gap-2.5">
          <div className="flex h-7 w-7 items-center justify-center rounded-lg bg-indigo-500/20 text-indigo-300">
            <svg className="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z" />
              <polyline points="22,6 12,13 2,6" />
            </svg>
          </div>
          <div>
            <div className="flex items-center gap-2">
              <h3 className="text-sm font-semibold text-zinc-100">
                Pending Share Invitations
              </h3>
              <span className="inline-flex items-center justify-center rounded-full bg-indigo-500/30 px-2 py-0.5 text-xs font-semibold text-indigo-300 border border-indigo-500/40">
                {invitations.length}
              </span>
            </div>
            <p className="text-xs text-zinc-400">
              Files shared with you requiring your review before adding to your Drive.
            </p>
          </div>
        </div>

        <button
          type="button"
          onClick={() => setCollapsed((prev) => !prev)}
          className="rounded-lg p-1.5 text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200 transition-colors"
          title={collapsed ? 'Expand invitations' : 'Collapse invitations'}
        >
          <svg
            className={cn('h-4 w-4 transition-transform duration-200', collapsed && '-rotate-90')}
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
          >
            <polyline points="6 9 12 15 18 9" />
          </svg>
        </button>
      </div>

      {/* Invitations List */}
      {!collapsed && (
        <div className="divide-y divide-zinc-800/60 p-2 space-y-2">
          {invitations.map((inv) => {
            const isLoading = loadingId === inv.id
            const isDropdownOpen = activeDropdownId === inv.id

            return (
              <div
                key={inv.id}
                className="flex flex-col sm:flex-row sm:items-center justify-between gap-3.5 rounded-lg border border-zinc-800/80 bg-zinc-900/40 p-3.5 transition-colors hover:bg-zinc-800/40"
              >
                {/* File info and Sender Details */}
                <div className="flex items-start gap-3 min-w-0 flex-1">
                  <div className="mt-0.5 flex-shrink-0">
                    <FileIcon filename={inv.file_name} isDirectory={inv.is_directory} size={28} />
                  </div>

                  <div className="min-w-0 flex-1 space-y-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span
                        className="font-medium text-sm text-zinc-100 truncate max-w-[200px] sm:max-w-xs md:max-w-md"
                        title={inv.file_name}
                      >
                        {inv.file_name}
                      </span>
                      <span className="text-xs text-zinc-500">
                        ({formatFileSize(inv.size_bytes)})
                      </span>
                      <span className="rounded border border-zinc-700 bg-zinc-800/60 px-1.5 py-0.2 text-[10px] uppercase font-semibold text-zinc-300">
                        {inv.role}
                      </span>
                      <span className="inline-flex items-center gap-1 rounded border border-amber-500/30 bg-amber-500/10 px-1.5 py-0.2 text-[10px] font-medium text-amber-300">
                        <svg className="h-3 w-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                          <circle cx="12" cy="12" r="10" />
                          <polyline points="12 6 12 12 16 14" />
                        </svg>
                        {formatExpiresIn(inv.expires_at)}
                      </span>
                    </div>

                    {/* Sender Identity */}
                    <div className="flex items-center gap-1.5 text-xs text-zinc-400">
                      <span className="flex h-4 w-4 items-center justify-center rounded-full bg-indigo-600/80 text-[9px] font-bold text-white">
                        {initialsFor(inv.sender_email)}
                      </span>
                      <span>Shared by</span>
                      <span className="font-medium text-zinc-200">{inv.sender_email}</span>
                    </div>

                    {/* Optional Note */}
                    {inv.message && (
                      <p className="text-xs text-indigo-300/90 italic bg-indigo-950/30 border border-indigo-500/20 rounded px-2 py-1 inline-block">
                        "{inv.message}"
                      </p>
                    )}

                    {/* AI Summary Snippet */}
                    {inv.summary && (
                      <p className="text-xs text-zinc-400 line-clamp-1 flex items-center gap-1">
                        <span className="text-purple-400 font-medium">AI Insights:</span>
                        <span>{inv.summary}</span>
                      </p>
                    )}
                  </div>
                </div>

                {/* Actions Toolbar */}
                <div className="flex items-center gap-2 self-end sm:self-center flex-shrink-0">
                  <Button
                    variant="ghost"
                    onClick={() => onPreview(inv)}
                    disabled={isLoading}
                    title="Safe sandboxed preview"
                    className="px-3 py-1.5 text-xs text-zinc-300 hover:text-white"
                  >
                    <svg className="h-4 w-4 mr-1.5 text-zinc-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                      <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
                      <circle cx="12" cy="12" r="3" />
                    </svg>
                    Preview
                  </Button>

                  <Button
                    variant="danger"
                    onClick={() => onDecline(inv)}
                    disabled={isLoading}
                    title="Decline invitation"
                    className="px-3 py-1.5 text-xs"
                  >
                    Decline
                  </Button>

                  <Button
                    onClick={() => onAccept(inv)}
                    disabled={isLoading}
                    className="px-3.5 py-1.5 text-xs bg-indigo-600 hover:bg-indigo-500 text-white"
                  >
                    {isLoading ? <Spinner size={14} className="mr-1.5" /> : (
                      <svg className="h-3.5 w-3.5 mr-1.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
                        <polyline points="20 6 9 17 4 12" />
                      </svg>
                    )}
                    Accept
                  </Button>

                  {/* Overflow More Menu (Block Sender) */}
                  <div className="relative">
                    <button
                      type="button"
                      onClick={() => setActiveDropdownId(isDropdownOpen ? null : inv.id)}
                      className="rounded-lg p-1.5 text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200 transition-colors"
                      title="More options"
                    >
                      <svg className="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                        <circle cx="12" cy="12" r="1" />
                        <circle cx="12" cy="5" r="1" />
                        <circle cx="12" cy="19" r="1" />
                      </svg>
                    </button>

                    {isDropdownOpen && (
                      <div className="absolute right-0 top-full mt-1 w-48 rounded-lg border border-zinc-800 bg-zinc-900 py-1 shadow-xl z-20">
                        <button
                          type="button"
                          onClick={() => {
                            setActiveDropdownId(null)
                            void onBlockSender(inv)
                          }}
                          className="flex w-full items-center gap-2 px-3 py-2 text-xs text-red-400 hover:bg-red-500/10 transition-colors text-left"
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
            )
          })}
        </div>
      )}
    </div>
  )
}
