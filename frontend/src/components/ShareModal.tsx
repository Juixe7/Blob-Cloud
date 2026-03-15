import { useCallback, useEffect, useState, type FormEvent } from 'react'
import axios from 'axios'
import { apiClient } from '../lib/api'
import { Modal } from './ui/Modal'
import { Button } from './ui/Button'
import { Input } from './ui/Input'
import { Alert } from './ui/Alert'
import { Spinner } from './ui/Spinner'
import { XIcon, UserPlusIcon, TrashIcon } from './icons'
import type {
  CollaboratorPermission,
  CollaboratorRole,
} from '../types/file'
import { cn } from '../lib/format'

interface ShareModalProps {
  open: boolean
  onClose: () => void
  /** The file/folder being shared. null = closed. */
  file: { id: string; name: string } | null
}

/** Roles a new collaborator can be granted (OWNER is not assignable here). */
const ASSIGNABLE_ROLES: Exclude<CollaboratorRole, 'OWNER'>[] = ['VIEWER', 'EDITOR']

/** RFC-5322-ish email sanity check. Backend re-validates; this is just UX. */
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

/** Derive up-to-two uppercase initials for the avatar bubble. */
function initialsFor(email: string): string {
  const local = email.split('@')[0] ?? email
  const parts = local.split(/[.\-_+]/).filter(Boolean)
  if (parts.length === 0) return email.slice(0, 2).toUpperCase()
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase()
  return (parts[0][0] + parts[1][0]).toUpperCase()
}

/** Deterministic gradient per email so avatars feel distinct without assets. */
function avatarGradient(email: string): string {
  const palettes = [
    'from-violet-500 to-fuchsia-500',
    'from-sky-500 to-indigo-500',
    'from-emerald-500 to-teal-500',
    'from-amber-500 to-orange-500',
    'from-rose-500 to-pink-500',
    'from-cyan-500 to-blue-500',
  ]
  let hash = 0
  for (let i = 0; i < email.length; i++) hash = (hash * 31 + email.charCodeAt(i)) | 0
  return palettes[Math.abs(hash) % palettes.length]
}

/** Human label for a role badge. */
function roleBadgeClass(role: CollaboratorRole): string {
  switch (role) {
    case 'OWNER':
      return 'border-amber-500/40 bg-amber-500/10 text-amber-300'
    case 'EDITOR':
      return 'border-sky-500/40 bg-sky-500/10 text-sky-300'
    case 'VIEWER':
    default:
      return 'border-zinc-700 bg-zinc-800/60 text-zinc-400'
  }
}

/**
 * Collaborative sharing panel (Upgrade B).
 *
 * - On open, fetches the current permission list via GET /api/files/{id}/permissions.
 * - The add-collaborator form posts to POST /api/files/{id}/share; the new
 *   permission is appended optimistically on success.
 * - Errors are surfaced inline (invalid email, user not found, duplicate grant).
 */
export function ShareModal({ open, onClose, file }: ShareModalProps) {
  const [collaborators, setCollaborators] = useState<CollaboratorPermission[]>([])
  const [loadingList, setLoadingList] = useState(false)
  const [listError, setListError] = useState<string | null>(null)

  const [email, setEmail] = useState('')
  const [role, setRole] = useState<Exclude<CollaboratorRole, 'OWNER'>>('VIEWER')
  const [inviting, setInviting] = useState(false)
  const [inviteError, setInviteError] = useState<string | null>(null)
  
  // Track loading state for individual updates/revokes
  const [updatingMap, setUpdatingMap] = useState<Record<string, boolean>>({})

  const fileId = file?.id ?? null

  // Reset transient form state whenever the target changes.
  useEffect(() => {
    if (!open) return
    setEmail('')
    setRole('VIEWER')
    setInviteError(null)
  }, [open, fileId])

  // Fetch the existing permission list whenever the modal opens for a file.
  const fetchPermissions = useCallback(async (id: string) => {
    setLoadingList(true)
    setListError(null)
    try {
      const res = await apiClient.get<CollaboratorPermission[]>(`/files/${id}/permissions`)
      setCollaborators(res.data ?? [])
    } catch (err) {
      setListError(extractError(err, 'Failed to load collaborators.'))
    } finally {
      setLoadingList(false)
    }
  }, [])

  useEffect(() => {
    if (open && fileId) {
      void fetchPermissions(fileId)
      // Optimistically clear so a stale list from a previous file doesn't show.
      setCollaborators([])
    } else if (!open) {
      // Tear down on close so a reopen doesn't flash the previous file's data.
      setCollaborators([])
      setListError(null)
    }
  }, [open, fileId, fetchPermissions])

  const handleInvite = async (e: FormEvent) => {
    e.preventDefault()
    if (!fileId) return

    const trimmed = email.trim()
    if (!EMAIL_RE.test(trimmed)) {
      setInviteError('Please enter a valid email address.')
      return
    }

    setInviting(true)
    setInviteError(null)

    try {
      const res = await apiClient.post<CollaboratorPermission>(
        `/files/${fileId}/share`,
        { grantee_email: trimmed, role },
      )
      // Append, de-duping by id (the API enforces uniqueness, but be safe).
      setCollaborators((prev) => {
        const exists = prev.some((c) => c.id === res.data.id)
        return exists ? prev : [...prev, res.data]
      })
      setEmail('')
    } catch (err) {
      setInviteError(extractInviteError(err))
    } finally {
      setInviting(false)
    }
  }

  const handleUpdateRole = async (granteeEmail: string, newRole: CollaboratorRole) => {
    if (!fileId) return
    setUpdatingMap((prev) => ({ ...prev, [granteeEmail]: true }))
    setListError(null)

    try {
      await apiClient.patch(`/files/${fileId}/share/${encodeURIComponent(granteeEmail)}`, {
        role: newRole,
      })
      setCollaborators((prev) =>
        prev.map((c) => (c.grantee_email === granteeEmail ? { ...c, role: newRole } : c))
      )
    } catch (err) {
      setListError(extractError(err, 'Failed to update role.'))
    } finally {
      setUpdatingMap((prev) => ({ ...prev, [granteeEmail]: false }))
    }
  }

  const handleRevoke = async (granteeEmail: string) => {
    if (!fileId) return
    setUpdatingMap((prev) => ({ ...prev, [granteeEmail]: true }))
    setListError(null)

    try {
      await apiClient.delete(`/files/${fileId}/share/${encodeURIComponent(granteeEmail)}`)
      setCollaborators((prev) => prev.filter((c) => c.grantee_email !== granteeEmail))
    } catch (err) {
      setListError(extractError(err, 'Failed to revoke access.'))
    } finally {
      setUpdatingMap((prev) => ({ ...prev, [granteeEmail]: false }))
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      label={`Share ${file?.name ?? 'item'}`}
      maxWidthClass="max-w-lg"
    >
      {/* Header */}
      <div className="mb-5 flex items-start justify-between gap-4">
        <div className="min-w-0">
          <h2 className="text-lg font-semibold text-zinc-50">Share</h2>
          <p className="mt-0.5 truncate text-sm text-zinc-500" title={file?.name}>
            {file?.name ?? ''}
          </p>
        </div>
        <button
          type="button"
          onClick={onClose}
          aria-label="Close share dialog"
          className="rounded-md p-1.5 text-zinc-500 transition-colors hover:bg-zinc-800 hover:text-zinc-300 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-violet-500/60"
        >
          <XIcon size={18} />
        </button>
      </div>

      {/* Add collaborator form */}
      <form onSubmit={handleInvite} noValidate className="mb-5">
        <label htmlFor="collab-email" className="mb-1.5 block text-sm font-medium text-zinc-300">
          Invite by email
        </label>
        <div className="flex gap-2">
          <div className="flex-1">
            <Input
              id="collab-email"
              type="email"
              inputMode="email"
              autoComplete="email"
              placeholder="name@example.com"
              value={email}
              onChange={(e) => {
                setEmail(e.target.value)
                if (inviteError) setInviteError(null)
              }}
              disabled={inviting}
            />
          </div>
          <select
            value={role}
            onChange={(e) => setRole(e.target.value as Exclude<CollaboratorRole, 'OWNER'>)}
            disabled={inviting}
            aria-label="Role"
            className="rounded-lg border border-zinc-800 bg-zinc-900/50 px-3 py-2.5 text-sm text-zinc-200 transition-colors hover:border-zinc-700 focus:border-violet-500/60 focus:outline-none focus:ring-2 focus:ring-violet-500/20"
          >
            {ASSIGNABLE_ROLES.map((r) => (
              <option key={r} value={r}>
                {r.charAt(0) + r.slice(1).toLowerCase()}
              </option>
            ))}
          </select>
          <Button type="submit" loading={inviting} className="whitespace-nowrap">
            <UserPlusIcon size={16} />
            Invite
          </Button>
        </div>
        {inviteError && (
          <div className="mt-2.5">
            <Alert variant="error">{inviteError}</Alert>
          </div>
        )}
      </form>

      {/* Collaborator list */}
      <div>
        <div className="mb-2 flex items-center justify-between">
          <h3 className="text-xs font-medium uppercase tracking-wider text-zinc-500">
            People with access
          </h3>
          {collaborators.length > 0 && (
            <span className="text-xs text-zinc-600">{collaborators.length}</span>
          )}
        </div>

        {loadingList ? (
          <div className="flex items-center justify-center py-8 text-zinc-500">
            <Spinner size={18} className="text-violet-500" />
          </div>
        ) : listError ? (
          <Alert variant="error">{listError}</Alert>
        ) : collaborators.length === 0 ? (
          <div className="rounded-lg border border-dashed border-zinc-800 px-4 py-6 text-center">
            <p className="text-sm text-zinc-500">No collaborators yet</p>
            <p className="mt-1 text-xs text-zinc-600">
              Invite someone above to share this {file ? '' : 'item'}.
            </p>
          </div>
        ) : (
          <ul className="-mx-1.5 max-h-64 space-y-0.5 overflow-y-auto px-1.5">
            {collaborators.map((c) => (
              <li
                key={c.id}
                className="flex items-center gap-3 rounded-lg px-2 py-2 transition-colors hover:bg-zinc-800/40"
              >
                <span
                  className={cn(
                    'flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-full bg-gradient-to-br text-xs font-semibold text-white',
                    avatarGradient(c.grantee_email),
                  )}
                  aria-hidden="true"
                >
                  {initialsFor(c.grantee_email)}
                </span>
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm text-zinc-200" title={c.grantee_email}>
                    {c.grantee_email}
                  </p>
                </div>
                
                {/* Role indicator / selector */}
                {c.role === 'OWNER' ? (
                  <span
                    className={cn(
                      'flex-shrink-0 rounded-full border px-2.5 py-0.5 text-[10px] font-medium uppercase tracking-wider',
                      roleBadgeClass(c.role),
                    )}
                  >
                    {c.role}
                  </span>
                ) : updatingMap[c.grantee_email] ? (
                  <div className="flex w-[120px] justify-end pr-4">
                    <Spinner size={16} className="text-zinc-500" />
                  </div>
                ) : (
                  <div className="flex items-center gap-2">
                    <select
                      value={c.role}
                      onChange={(e) => handleUpdateRole(c.grantee_email, e.target.value as CollaboratorRole)}
                      className="rounded bg-transparent py-1 pl-2 pr-6 text-xs text-zinc-300 hover:bg-zinc-800 focus:outline-none focus:ring-2 focus:ring-violet-500/40"
                    >
                      <option value="VIEWER">Viewer</option>
                      <option value="EDITOR">Editor</option>
                    </select>
                    
                    <button
                      type="button"
                      onClick={() => handleRevoke(c.grantee_email)}
                      title="Remove access"
                      className="rounded p-1 text-zinc-500 transition-colors hover:bg-red-500/10 hover:text-red-400 focus:outline-none focus:ring-2 focus:ring-red-500/40"
                    >
                      <TrashIcon size={14} />
                    </button>
                  </div>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>
    </Modal>
  )
}

export default ShareModal

/* --------------------------- error extraction --------------------------- */

function extractError(err: unknown, fallback: string): string {
  if (axios.isAxiosError(err)) {
    const status = err.response?.status
    const data = err.response?.data as { error?: string; message?: string } | undefined
    if (status === 401) return 'Session expired. Please sign in again.'
    if (status === 404) return 'Item not found.'
    return data?.error || data?.message || fallback
  }
  return 'An unexpected error occurred.'
}

/**
 * Invite-specific error mapping. We surface backend signals (duplicate grant,
 * unknown user) as actionable copy rather than generic messages.
 */
function extractInviteError(err: unknown): string {
  if (axios.isAxiosError(err)) {
    const status = err.response?.status
    const data = err.response?.data as { error?: string; message?: string } | undefined
    if (status === 409) return 'This person already has access.'
    if (status === 400) return data?.error || 'Invalid request. Check the email and role.'
    if (status === 404) return 'No user found with that email.'
    if (status === 401) return 'Session expired. Please sign in again.'
    return data?.error || data?.message || 'Failed to send invite.'
  }
  return 'An unexpected error occurred.'
}
