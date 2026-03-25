import { useCallback, useEffect, useMemo, useRef, useState, type ChangeEvent, type DragEvent, type MouseEvent } from 'react'
import { useSearchParams } from 'react-router-dom'
import axios from 'axios'
import { apiClient } from '../lib/api'
import { getAccessToken } from '../lib/token'
import { useAuth } from '../hooks/useAuth'
import { useWebSocket } from '../hooks/useWebSocket'
import { useUpload, UPLOAD_COMPLETE_EVENT } from '../context/UploadContext'
import { useToast } from '../components/Toast'
import type { FileItem, BreadcrumbNode } from '../types/file'
import type {
  WSMessage,
  ThumbnailReadyPayload,
  UploadCompletedPayload,
  FileSharedPayload,
} from '../types/sync'
import { Sidebar } from '../components/Sidebar'
import { Navbar } from '../components/Navbar'
import { MobileNav } from '../components/MobileNav'
import { ListView } from '../components/ListView'
import { GridView } from '../components/GridView'
import { DirectorySkeleton } from '../components/DirectorySkeleton'
import { NewFolderModal } from '../components/NewFolderModal'
import { ContextMenu, type ContextMenuActions } from '../components/ContextMenu'
import { ShareModal } from '../components/ShareModal'
import { MoveModal } from '../components/MoveModal'
import { RenameModal } from '../components/RenameModal'
import { DeleteModal } from '../components/DeleteModal'
import { SettingsModal } from '../components/SettingsModal'
import { FilePreviewModal } from '../components/FilePreviewModal'
import { ShortcutModal } from '../components/ShortcutModal'
import { BulkActionBar } from '../components/BulkActionBar'
import { Alert } from '../components/ui/Alert'
import { PassphraseModal } from '../components/PassphraseModal'
import { PublicShareModal } from '../components/PublicShareModal'
import { UploadQueue } from '../components/UploadQueue'
import { downloadEncryptedFile } from '../lib/download'
import { VersionHistoryModal } from '../components/VersionHistoryModal'
import { GetInfoModal } from '../components/GetInfoModal'
import { DetailPanel } from '../components/DetailPanel'

/** Mocked storage limit for the gauge (15 GB in bytes). */
const STORAGE_LIMIT = 15 * 1_073_741_824
/** Mocked storage used (2.4 GB). */
const STORAGE_USED = 2.4 * 1_073_741_824

/**
 * File Explorer Dashboard.
 *
 * Manages the directory navigation state machine, API fetch lifecycle,
 * local search filtering, grid/list view toggle, the new-folder modal, and —
 * as of Phase 7.4 — the right-click context menu plus the share / move /
 * rename / delete action pipelines.
 */
export function Dashboard() {
  const { logout, user } = useAuth()
  const [searchParams, setSearchParams] = useSearchParams()

  // ---- Navigation state (driven by URL search params for Chrome Back/Forward support) ----
  const rawNav = searchParams.get('nav')
  const activeNav = rawNav === 'shared' ? 'shared' : rawNav === 'trash' ? 'trash' : 'drive'
  const currentFolderId = activeNav !== 'drive' ? null : searchParams.get('folder')
  const [breadcrumbs, setBreadcrumbs] = useState<BreadcrumbNode[]>([
    { id: null, name: 'My Drive' },
  ])
  const [items, setItems] = useState<FileItem[]>([])
  const [isTrashContext, setIsTrashContext] = useState(false)
  const [viewMode, setViewMode] = useState<'grid' | 'list'>(() => {
    return (localStorage.getItem('blobcloud_view_mode') as 'grid' | 'list') || 'grid'
  })
  const [searchQuery, setSearchQuery] = useState('')
  const [isLoading, setIsLoading] = useState(true)
  const [fetchError, setFetchError] = useState<string | null>(null)
  const [lastFetchedKey, setLastFetchedKey] = useState<string>('')

  const isInTrash = activeNav === 'trash' || isTrashContext

  const currentKey = activeNav + ':' + (currentFolderId || '')
  const isCurrentStateLoaded = lastFetchedKey === currentKey

  const effectiveBreadcrumbs = useMemo<BreadcrumbNode[]>(() => {
    if (activeNav === 'shared') {
      return [{ id: null, name: 'Shared with me' }]
    }
    if (activeNav === 'trash' || isTrashContext) {
      const base: BreadcrumbNode[] = [
        { id: null, name: 'My Drive' },
        { id: null, name: 'Trash' },
      ]
      if (!currentFolderId) return base
      const idx = breadcrumbs.findIndex((node) => node.id === currentFolderId)
      if (idx !== -1) {
        const subNodes = breadcrumbs.slice(0, idx + 1).filter((n) => n.name !== 'My Drive' && n.name !== 'Trash')
        return [...base, ...subNodes]
      }
      const lastNode = breadcrumbs[breadcrumbs.length - 1]
      return [...base, { id: currentFolderId, name: lastNode?.id === currentFolderId ? lastNode.name : '...' }]
    }
    if (!currentFolderId) {
      return [{ id: null, name: 'My Drive' }]
    }

    const idx = breadcrumbs.findIndex((node) => node.id === currentFolderId)
    if (idx !== -1) {
      return breadcrumbs.slice(0, idx + 1)
    }

    const lastNode = breadcrumbs[breadcrumbs.length - 1]
    if (lastNode && lastNode.id === currentFolderId) {
      return breadcrumbs
    }

    return [
      { id: null, name: 'My Drive' },
      { id: currentFolderId, name: '...' },
    ]
  }, [activeNav, isTrashContext, currentFolderId, breadcrumbs])

  // Sync breadcrumbs when activeNav or currentFolderId changes
  useEffect(() => {
    if (activeNav === 'shared') {
      setBreadcrumbs([{ id: null, name: 'Shared with me' }])
    } else if (activeNav === 'trash') {
      setBreadcrumbs([
        { id: null, name: 'My Drive' },
        { id: null, name: 'Trash' },
      ])
    } else if (!currentFolderId) {
      setBreadcrumbs([{ id: null, name: 'My Drive' }])
    }
  }, [activeNav, currentFolderId])

  // ---- Sidebar state ----
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false)

  // ---- Modal states ----
  const [folderModalOpen, setFolderModalOpen] = useState(false)
  const [settingsModalOpen, setSettingsModalOpen] = useState(false)

  // ---- Context menu + action-modal state (Phase 7.4 + Trash) ----
  const [menuTarget, setFileItem] = useState<FileItem | null>(null)
  const [menuPosition, setMenuPosition] = useState<{ x: number; y: number } | null>(null)

  const [shareTarget, setShareTarget] = useState<FileItem | null>(null)
  const [renameTarget, setRenameTarget] = useState<FileItem | null>(null)
  const [moveTarget, setMoveTarget] = useState<FileItem | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<FileItem | null>(null)
  const [permanentDeleteTarget, setPermanentDeleteTarget] = useState<FileItem | null>(null)
  const [shortcutTarget, setShortcutTarget] = useState<FileItem | null>(null)
  const [previewTarget, setPreviewTarget] = useState<FileItem | null>(null)
  const [publicShareTarget, setPublicShareTarget] = useState<FileItem | null>(null)
  const [versionHistoryTarget, setVersionHistoryTarget] = useState<FileItem | null>(null)
  const [infoTarget, setInfoTarget] = useState<FileItem | null>(null)
  const [isDetailsOpen, setIsDetailsOpen] = useState(false)

  // Phase 11 E2EE state
  const [downloadDecryptTarget, setDownloadDecryptTarget] = useState<FileItem | null>(null)
  const [isDecrypting, setIsDecrypting] = useState(false)
  const [pendingEncryptedUpload, setPendingEncryptedUpload] = useState<{ files: File[], isFolder: boolean } | null>(null)

  // ---- Phase C.1: Multi-Select & Bulk Operations State ----
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [lastSelectedId, setLastSelectedId] = useState<string | null>(null)
  const [bulkMoveOpen, setBulkMoveOpen] = useState(false)
  const [bulkDeletePermanentOpen, setBulkDeletePermanentOpen] = useState(false)

  const hasSelectedViewerItem = useMemo(() => {
    return Array.from(selectedIds).some(id => {
      const item = items.find(it => it.id === id)
      if (!item) return false
      const role = item.user_id === user?.user_id ? 'OWNER' : (item.role || 'VIEWER')
      return role === 'VIEWER'
    })
  }, [selectedIds, items, user])

  // ---- Abort controller ref for fetch cleanup ----
  const abortRef = useRef<AbortController | null>(null)

  // ---- Fetch directory contents ----
  const fetchDirectory = useCallback(async (folderId: string | null = currentFolderId, navMode: string = activeNav, query: string = searchQuery) => {
    // Abort any in-flight request
    abortRef.current?.abort()
    const controller = new AbortController()
    abortRef.current = controller

    setIsLoading(true)
    setFetchError(null)

    try {
      let url = '/files'
      if (query.trim() !== '') {
        url = `/files/search?q=${encodeURIComponent(query.trim())}`
      } else if (navMode === 'shared') {
        url = '/files?filter=shared'
      } else if (navMode === 'recent') {
        url = '/files/recent'
      } else if (navMode === 'trash') {
        url = '/files/trash'
      } else if (folderId) {
        url = `/files?parent_id=${folderId}`
      }

      const res = await apiClient.get<{ files?: FileItem[] } | FileItem[]>(url, {
        signal: controller.signal,
      })

      const isDeletedDir = res.headers['x-directory-deleted'] === 'true'
      if (isDeletedDir || navMode === 'trash') {
        setIsTrashContext(true)
      } else {
        setIsTrashContext(false)
      }

      const data = res.data
      let rawData: FileItem[] = []
      
      // Handle array or wrapped object response (search might wrap in { files: [] })
      if (Array.isArray(data)) {
        rawData = data
      } else if (data && typeof data === 'object' && Array.isArray((data as any).files)) {
        rawData = (data as any).files
      }

      const token = getAccessToken() || ''
      const sorted = rawData.map(item => {
        const isImage = /\.(jpg|jpeg|png|webp|gif)$/i.test(item.name)
        if (isImage && !item.thumbnail_url) {
          return {
             ...item,
             thumbnail_url: `${apiClient.defaults.baseURL}/files/${item.id}/thumbnail?token=${token}`
          }
        }
        return item
      }).sort((a, b) => {
        if (a.is_directory !== b.is_directory) return a.is_directory ? -1 : 1
        return a.name.localeCompare(b.name)
      })

      setItems(sorted)
      setLastFetchedKey(navMode + ':' + (folderId || ''))

      // Track folder view if we successfully loaded a specific folder
      if (folderId && navMode === 'files') {
        apiClient.post(`/files/${folderId}/view`).catch(e => console.error("failed to track folder view", e))
      }
    } catch (err) {
      // Don't overwrite previous items on abort
      if (axios.isCancel(err)) return

      let message = 'Failed to load directory contents.'
      if (axios.isAxiosError(err)) {
        message = err.response?.status === 503
          ? 'Storage is not available right now.'
          : err.response?.status === 401
            ? 'Session expired. Please sign in again.'
            : err.message === 'Network Error'
              ? 'Server offline. Check your connection.'
              : message
      }
      setFetchError(message)
    } finally {
      setIsLoading(false)
    }
  }, [])

  // Fetch on mount and whenever currentFolderId or activeNav changes
  useEffect(() => {
    // Only fetch if searchQuery is empty here, because the debouncer handles searchQuery changes
    if (!searchQuery.trim()) {
      void fetchDirectory(currentFolderId, activeNav, '')
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentFolderId, activeNav])

  // Debounced search fetch
  useEffect(() => {
    const delayDebounceFn = setTimeout(() => {
      if (searchQuery.trim() !== '') {
        void fetchDirectory(currentFolderId, activeNav, searchQuery)
      } else {
        // If cleared, fetch normal directory
        void fetchDirectory(currentFolderId, activeNav, '')
      }
    }, 500)

    return () => clearTimeout(delayDebounceFn)
  }, [searchQuery, currentFolderId, activeNav, fetchDirectory])

  // Cleanup abort on unmount
  useEffect(() => {
    return () => { abortRef.current?.abort() }
  }, [])

  // ---- Navigation handlers ----

  /** Navigate into a folder (double-click / Enter key). */
  const navigateToFolder = useCallback((item: FileItem) => {
    const targetFolderId = item.shortcut_target_id ?? item.target_id ?? item.id
    if (isInTrash) {
      setSearchParams({ nav: 'trash', folder: targetFolderId })
    } else if (activeNav === 'shared') {
      setSearchParams({ nav: 'shared', folder: targetFolderId })
    } else {
      setSearchParams({ folder: targetFolderId })
    }
    setBreadcrumbs((prev) => [...prev, { id: targetFolderId, name: item.name }])
    setSearchQuery('')
  }, [isInTrash, activeNav, setSearchParams])

  /** Navigate to a breadcrumb node (click). */
  const navigateToBreadcrumb = useCallback((index: number) => {
    const nextBreadcrumbs = effectiveBreadcrumbs.slice(0, index + 1)
    setBreadcrumbs(nextBreadcrumbs)
    const target = nextBreadcrumbs[nextBreadcrumbs.length - 1]
    if (target && target.id) {
      if (isInTrash) {
        setSearchParams({ nav: 'trash', folder: target.id })
      } else if (activeNav === 'shared') {
        setSearchParams({ nav: 'shared', folder: target.id })
      } else {
        setSearchParams({ folder: target.id })
      }
    } else {
      if (isInTrash && target?.name === 'Trash') {
        setSearchParams({ nav: 'trash' })
      } else if (activeNav === 'shared') {
        setSearchParams({ nav: 'shared' })
      } else {
        setSearchParams({})
      }
    }
    setSearchQuery('')
  }, [effectiveBreadcrumbs, setSearchParams, isInTrash, activeNav])

  // ---- Search filter ----
  // Semantic search is handled by the backend, so we just use items directly.
  const filteredItems = items

  // ---- New folder callback ----
  const handleFolderCreated = useCallback((folder: FileItem) => {
    // Inject the newly created folder into the items array (prepend, sort)
    setItems((prev) => {
      const next = [folder, ...prev]
      return next.sort((a, b) => {
        if (a.is_directory !== b.is_directory) return a.is_directory ? -1 : 1
        return a.name.localeCompare(b.name)
      })
    })
    // eslint-disable-next-line no-console
    console.info('[dashboard] folder created:', folder.name)
  }, [])

  /** Toggle Grid/List layout */
  const handleViewModeToggle = useCallback(() => {
    setViewMode((prev) => {
      const next = prev === 'list' ? 'grid' : 'list'
      localStorage.setItem('blobcloud_view_mode', next)
      return next
    })
  }, [])

  /* ----------------------- Phase 7.5: real-time sync ----------------------- */
  const { token } = useAuth()
  const { push: pushToast } = useToast()

  /**
   * Build the WS URL from the configured API base, upgrading http(s) → ws(s)
   * and appending the JWT as a query param (the Go server authenticates the
   * handshake from ?token=, since browsers can't set headers on WS).
   */
  const wsUrl = useMemo(() => {
    if (!token) return null
    const apiBase = (import.meta.env.VITE_API_BASE as string | undefined) ?? '/api'
    // Absolute backend host (e.g. http://localhost:8090/api) → ws://...
    // Relative path (/api) in Vite dev → talk to the dev server origin.
    let base: string
    if (/^https?:\/\//i.test(apiBase)) {
      base = apiBase
    } else if (typeof window !== 'undefined') {
      base = window.location.origin + apiBase
    } else {
      return null
    }
    const wsBase = base.replace(/^http/i, 'ws')
    return `${wsBase}/ws?token=${encodeURIComponent(token)}`
  }, [token])

  /** Merge a thumbnail URL into the matching item, causing an instant icon→image swap. */
  const applyThumbnail = useCallback((fileId: string, thumbnailUrl: string) => {
    const token = getAccessToken() || ''
    const fullUrl = thumbnailUrl.includes('token=') 
      ? thumbnailUrl 
      : `${apiClient.defaults.baseURL}${thumbnailUrl.replace('/api/files', '/files')}?token=${token}`
      
    setItems((prev) =>
      prev.map((it) => (it.id === fileId ? { ...it, thumbnail_url: fullUrl } : it)),
    )
  }, [])

  /** Look up a filename by id from current items (for toast copy). */
  const filenameForId = useCallback(
    (fileId: string): string | null => {
      const found = items.find((it) => it.id === fileId)
      return found?.name ?? null
    },
    [items],
  )

  // Batch debouncer for upload completion toasts to prevent toast flooding during folder uploads.
  const batchUploadCountRef = useRef(0)
  const batchUploadTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const lastUploadedNameRef = useRef<string | null>(null)

  const triggerBatchUploadToast = useCallback((name: string | null) => {
    batchUploadCountRef.current += 1
    if (name) lastUploadedNameRef.current = name

    if (batchUploadTimerRef.current) {
      clearTimeout(batchUploadTimerRef.current)
    }

    batchUploadTimerRef.current = setTimeout(() => {
      const count = batchUploadCountRef.current
      const lastName = lastUploadedNameRef.current

      if (count === 1) {
        pushToast({
          variant: 'success',
          message: lastName ? `Upload complete: ${lastName}` : 'Upload complete.',
        })
      } else if (count > 1) {
        pushToast({
          variant: 'success',
          message: `Upload complete: ${count} files uploaded`,
        })
      }

      batchUploadCountRef.current = 0
      lastUploadedNameRef.current = null
      batchUploadTimerRef.current = null
    }, 500)
  }, [pushToast])

  /**
   * Central dispatcher for incoming WS messages. Kept stable via refs so the
   * socket never resubscribes when items change.
   */
  const handleWsMessage = useCallback(
    (msg: WSMessage) => {
      switch (msg.type) {
        case 'UPLOAD_COMPLETED': {
          const payload = msg.payload as UploadCompletedPayload
          // Silent refresh so the new file appears in the active listing.
          void fetchDirectory(currentFolderId)
          const name = filenameForId(payload.file_id)
          triggerBatchUploadToast(name)
          break
        }
        case 'THUMBNAIL_READY': {
          const payload = msg.payload as ThumbnailReadyPayload
          if (payload.file_id && payload.thumbnail_url) {
            applyThumbnail(payload.file_id, payload.thumbnail_url)
          }
          break
        }
        case 'FILE_SHARED': {
          const payload = msg.payload as FileSharedPayload
          pushToast({
            variant: 'info',
            message: `${payload.shared_by || 'Someone'} shared a file with you: ${payload.filename || 'Untitled'}`,
            action: {
              label: 'View',
              onClick: () => {
                // "Shared with me" tab isn't built yet; log + leave a hook.
                // eslint-disable-next-line no-console
                console.info('[ws] navigate to shared file:', payload.file_id)
              },
            },
          })
          break
        }
        case 'VIRUS_DETECTED': {
          const payload = msg.payload as any // we'll use 'any' or 'VirusDetectedPayload' if we import it, wait, we can just import it. Let's cast it directly or inline.
          // Better yet, I'll just use any to avoid import issues.
          pushToast({
            variant: 'error',
            message: `Malware blocked: file ${payload.filename || 'Untitled'} was flagged as infected by ClamAV and placed in quarantine.`,
          })
          // Also refresh the directory so the item gets a QUARANTINED status in the UI
          void fetchDirectory(currentFolderId)
          break
        }
        default:
          // Unknown event types are ignored — forward-compatible.
          break
      }
    },
    // fetchDirectory is stable (useCallback, []); currentFolderId + items are
    // read via closures intentionally.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [currentFolderId, filenameForId, applyThumbnail, pushToast, triggerBatchUploadToast],
  )

  const { status: wsStatus } = useWebSocket({
    url: wsUrl,
    enabled: token !== null,
    onMessage: handleWsMessage,
  })

  /* ----------------------- Phase 7.4: file actions ----------------------- */

  /**
   * Open the floating context menu at the cursor, remembering which item it
   * was opened against. preventDefault stops the browser's native menu.
   */
  const handleItemContextMenu = useCallback((item: FileItem, e: MouseEvent) => {
    e.preventDefault()
    e.stopPropagation()
    setFileItem(item)
    setMenuPosition({ x: e.clientX, y: e.clientY })
  }, [])

  /** Dismiss the floating context menu. */
  const closeContextMenu = useCallback(() => {
    setFileItem(null)
    setMenuPosition(null)
  }, [])

  /** Trigger download of a file via its /download endpoint (browser-navigated). */
  const handleDownload = useCallback((item: FileItem) => {
    if (item.is_encrypted) {
      setDownloadDecryptTarget(item)
      return
    }
    const base = apiClient.defaults.baseURL ?? '/api'
    const token = getAccessToken() ?? ''
    const fileId = item.target_id ?? item.id
    const url = `${base}/files/${fileId}/download?token=${encodeURIComponent(token)}`
    window.location.href = url
  }, [])

  const handleOpenFile = useCallback((item: FileItem) => {
    if (item.is_encrypted) {
      setPreviewTarget(item.target_id ? { ...item, id: item.target_id, target_id: undefined } : item)
      return
    }
    const base = apiClient.defaults.baseURL ?? '/api'
    const token = getAccessToken() ?? ''
    const fileId = item.target_id ?? item.id
    const url = `${base}/files/${fileId}/download?inline=true&token=${encodeURIComponent(token)}`
    window.open(url, '_blank')
  }, [])

  const handleRestore = useCallback(
    async (item: FileItem) => {
      try {
        await apiClient.post(`/files/${item.id}/restore`)
        setItems((prev) => prev.filter((i) => i.id !== item.id))
        pushToast({ message: `Restored "${item.name}"`, variant: 'success' })
      } catch {
        pushToast({ message: `Failed to restore "${item.name}"` })
      }
    },
    [pushToast],
  )

  // The action bundle handed to the context menu. Each just opens a modal.
  const menuActions: ContextMenuActions = useMemo(
    () => ({
      onPreview: (item) => handleOpenFile(item),
      onShare: (item) => setShareTarget(item),
      onRename: (item) => setRenameTarget(item),
      onMove: (item) => setMoveTarget(item),
      onDownload: (item) => handleDownload(item),
      onDelete: (item) => setDeleteTarget(item),
      onRestore: (item) => void handleRestore(item),
      onPermanentDelete: (item) => setPermanentDeleteTarget(item),
      onCreateShortcut: (item) => setShortcutTarget(item),
      onSharePublic: (item) => setPublicShareTarget(item),
      onVersionHistory: (item) => setVersionHistoryTarget(item),
      onGetInfo: (item) => setInfoTarget(item),
    }),
    [handleDownload, handleRestore],
  )

  /** Patch an item's name in local state after a successful rename. */
  const handleRenamed = useCallback((itemId: string, newName: string) => {
    setItems((prev) =>
      prev
        .map((it) => (it.id === itemId ? { ...it, name: newName, updated_at: new Date().toISOString() } : it))
        .sort((a, b) => {
          if (a.is_directory !== b.is_directory) return a.is_directory ? -1 : 1
          return a.name.localeCompare(b.name)
        }),
    )
  }, [])

  /**
   * After a move, drop the item from the active listing if it left the current
   * folder. (If it was moved into the current folder — unlikely but possible —
   * we leave it alone; a refresh would reconcile if needed.)
   */
  const handleMoved = useCallback(
    (itemId: string, newParentId: string | null) => {
      if (newParentId !== currentFolderId) {
        setItems((prev) => prev.filter((it) => it.id !== itemId))
      }
    },
    [currentFolderId],
  )

  /** Optimistically remove a deleted item from the listing. */
  const handleDeleted = useCallback((itemId: string) => {
    setItems((prev) => prev.filter((it) => !selectedIds.has(it.id) && it.id !== itemId))
  }, [selectedIds])

  // Clear selection on folder navigation or tab change
  useEffect(() => {
    setSelectedIds(new Set())
    setLastSelectedId(null)
  }, [currentFolderId, activeNav])

  const clearSelection = useCallback(() => {
    setSelectedIds(new Set())
    setLastSelectedId(null)
  }, [])

  const handleSingleSelect = useCallback((id: string) => {
    setSelectedIds(new Set([id]))
    setLastSelectedId(id)
  }, [])

  const handleToggleSelect = useCallback((id: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev)
      if (next.has(id)) {
        next.delete(id)
      } else {
        next.add(id)
      }
      return next
    })
    setLastSelectedId(id)
  }, [])

  const handleSelectRange = useCallback(
    (targetId: string) => {
      if (!lastSelectedId) {
        handleToggleSelect(targetId)
        return
      }
      const ids = filteredItems.map((it) => it.id)
      const startIndex = ids.indexOf(lastSelectedId)
      const endIndex = ids.indexOf(targetId)

      if (startIndex === -1 || endIndex === -1) {
        handleToggleSelect(targetId)
        return
      }

      const [min, max] = startIndex < endIndex ? [startIndex, endIndex] : [endIndex, startIndex]
      const rangeIds = ids.slice(min, max + 1)

      setSelectedIds((prev) => {
        const next = new Set(prev)
        for (const id of rangeIds) next.add(id)
        return next
      })
    },
    [lastSelectedId, filteredItems, handleToggleSelect],
  )

  const handleSelectAll = useCallback(() => {
    const allIds = filteredItems.map((it) => it.id)
    const allSelected = allIds.length > 0 && allIds.every((id) => selectedIds.has(id))
    if (allSelected) {
      setSelectedIds(new Set())
    } else {
      setSelectedIds(new Set(allIds))
    }
  }, [filteredItems, selectedIds])

  // Ctrl+A / Cmd+A and Esc key listener
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement)?.tagName
      if (['INPUT', 'TEXTAREA', 'SELECT'].includes(tag)) return

      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'a') {
        e.preventDefault()
        handleSelectAll()
      } else if (e.key === 'Escape') {
        clearSelection()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [handleSelectAll, clearSelection])

  // ---- Bulk Action Executors ----
  const handleBulkSoftDelete = useCallback(async () => {
    const ids = Array.from(selectedIds)
    if (ids.length === 0) return
    try {
      await apiClient.post('/files/bulk/delete', { ids })
      setItems((prev) => prev.filter((it) => !selectedIds.has(it.id)))
      pushToast({
        variant: 'success',
        message: `Successfully moved ${ids.length} item${ids.length > 1 ? 's' : ''} to Trash.`,
      })
      clearSelection()
    } catch {
      pushToast({ message: 'Failed to delete selected items.' })
    }
  }, [selectedIds, pushToast, clearSelection])

  const handleBulkRestore = useCallback(async () => {
    const ids = Array.from(selectedIds)
    if (ids.length === 0) return
    try {
      await apiClient.post('/files/bulk/restore', { ids })
      setItems((prev) => prev.filter((it) => !selectedIds.has(it.id)))
      pushToast({
        variant: 'success',
        message: `Successfully restored ${ids.length} item${ids.length > 1 ? 's' : ''}.`,
      })
      clearSelection()
    } catch {
      pushToast({ message: 'Failed to restore selected items.' })
    }
  }, [selectedIds, pushToast, clearSelection])

  const handleBulkHardDelete = useCallback(async () => {
    const ids = Array.from(selectedIds)
    if (ids.length === 0) return
    try {
      await apiClient.delete('/files/bulk/permanent', { data: { ids } })
      setItems((prev) => prev.filter((it) => !selectedIds.has(it.id)))
      pushToast({
        variant: 'success',
        message: `Permanently deleted ${ids.length} item${ids.length > 1 ? 's' : ''}.`,
      })
      clearSelection()
      setBulkDeletePermanentOpen(false)
    } catch {
      pushToast({ message: 'Failed to permanently delete selected items.' })
    }
  }, [selectedIds, pushToast, clearSelection])

  const handleBulkDownload = useCallback(async () => {
    const selectedItems = filteredItems.filter((it) => selectedIds.has(it.id))
    if (selectedItems.length === 0) return

    if (selectedItems.length === 1) {
      handleDownload(selectedItems[0])
      return
    }

    const base = apiClient.defaults.baseURL ?? '/api'
    const token = getAccessToken() ?? ''
    const idsParam = selectedItems.map((it) => it.id).join(',')
    const url = `${base}/files/download?ids=${encodeURIComponent(idsParam)}&token=${encodeURIComponent(token)}`
    window.location.href = url

    clearSelection()
  }, [filteredItems, selectedIds, handleDownload, clearSelection])

  const handleBulkMoved = useCallback(
    (itemIds: string[], newParentId: string | null) => {
      if (newParentId !== currentFolderId) {
        const idSet = new Set(itemIds)
        setItems((prev) => prev.filter((it) => !idSet.has(it.id)))
      }
      pushToast({
        variant: 'success',
        message: `Successfully moved ${itemIds.length} item${itemIds.length > 1 ? 's' : ''}.`,
      })
      clearSelection()
      setBulkMoveOpen(false)
    },
    [currentFolderId, pushToast, clearSelection],
  )

  const { uploadFile, uploadFolder, isE2EEnabled, setIsE2EEnabled } = useUpload()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const encryptedFileInputRef = useRef<HTMLInputElement>(null)
  const [isDragging, setIsDragging] = useState(false)



  /** Open the native file picker for encrypted upload. */
  const handleUploadFile = useCallback(() => {
    if (isE2EEnabled) {
      encryptedFileInputRef.current?.click()
    } else {
      fileInputRef.current?.click()
    }
  }, [isE2EEnabled])

  /** Handle one or more files selected from the picker. */
  const handleFileChange = useCallback(
    (e: ChangeEvent<HTMLInputElement>) => {
      const files = e.target.files
      if (files) {
        for (const file of Array.from(files)) {
          uploadFile(file, currentFolderId)
        }
      }
      // Reset so selecting the same file again still fires onChange.
      e.target.value = ''
    },
    [uploadFile, currentFolderId],
  )

  const handleEncryptedFileChange = useCallback(
    (e: ChangeEvent<HTMLInputElement>) => {
      const files = e.target.files
      if (files && files.length > 0) {
        setPendingEncryptedUpload({ files: Array.from(files), isFolder: false })
      }
      e.target.value = ''
    },
    [],
  )

  /** Drag-and-drop handlers on the file explorer panel. */
  const handleDragOver = useCallback(
    (e: DragEvent<HTMLDivElement>) => {
      e.preventDefault()
      if (isInTrash) return
      if (e.dataTransfer.types.includes('Files')) setIsDragging(true)
    },
    [isInTrash],
  )

  const handleDragLeave = useCallback((e: DragEvent<HTMLDivElement>) => {
    // Only clear when leaving the container itself, not a child element.
    if (e.currentTarget === e.target) setIsDragging(false)
  }, [])

  const handleDrop = useCallback(
    (e: DragEvent<HTMLDivElement>) => {
      e.preventDefault()
      setIsDragging(false)
      if (isInTrash) {
        pushToast({
          variant: 'warning',
          message: 'Cannot upload files to the Trash Bin.',
        })
        return
      }
      const files = e.dataTransfer.files
      if (files) {
        for (const file of Array.from(files)) {
          uploadFile(file, currentFolderId)
        }
      }
    },
    [uploadFile, currentFolderId, isInTrash, pushToast],
  )

  // Refresh the listing when any upload completes.
  useEffect(() => {
    const handler = () => void fetchDirectory(currentFolderId, activeNav)
    window.addEventListener(UPLOAD_COMPLETE_EVENT, handler)
    return () => window.removeEventListener(UPLOAD_COMPLETE_EVENT, handler)
  }, [UPLOAD_COMPLETE_EVENT, currentFolderId, activeNav, fetchDirectory])



  return (
    <div className="flex h-screen overflow-hidden bg-arch-950 text-zinc-100 font-sans select-none relative">
      {/* Mobile Sidebar Backdrop */}
      {mobileSidebarOpen && (
        <div
          className="fixed inset-0 z-40 bg-black/50 md:hidden"
          onClick={() => setMobileSidebarOpen(false)}
        />
      )}

      {/* Sidebar (Desktop only) */}
      <div className="hidden md:flex h-full">
        <Sidebar
          collapsed={sidebarCollapsed}
          mobileOpen={mobileSidebarOpen}
          onCloseMobile={() => setMobileSidebarOpen(false)}
          onToggleCollapse={() => setSidebarCollapsed((c) => !c)}
          onNewFolder={() => setFolderModalOpen(true)}
          onUploadFile={handleUploadFile}
          onUploadFolder={(files: File[]) => {
            if (isE2EEnabled) {
              setPendingEncryptedUpload({ files, isFolder: true })
            } else {
              void uploadFolder(files, currentFolderId)
            }
          }}
          onOpenSettings={() => setSettingsModalOpen(true)}
          isE2EEnabled={isE2EEnabled}
          onToggleE2E={() => setIsE2EEnabled(!isE2EEnabled)}
          activeNav={isInTrash ? 'trash' : activeNav}
          disableNew={isInTrash}
          onSelectNav={(navId) => {
            if (navId === 'shared') {
              setSearchParams({ nav: 'shared' })
            } else if (navId === 'trash') {
              setSearchParams({ nav: 'trash' })
            } else {
              setSearchParams({})
            }
            setMobileSidebarOpen(false)
          }}
          onSignOut={logout}
          storageUsed={STORAGE_USED}
          storageLimit={STORAGE_LIMIT}
          syncStatus={wsStatus}
        />
      </div>

      {/* Hidden native file input (opened by the sidebar button) */}
      <input
        ref={fileInputRef}
        type="file"
        multiple
        className="hidden"
        onChange={handleFileChange}
        aria-hidden="true"
        tabIndex={-1}
      />
      
      {/* Hidden native file input for encrypted uploads */}
      <input
        ref={encryptedFileInputRef}
        type="file"
        multiple
        className="hidden"
        onChange={handleEncryptedFileChange}
        aria-hidden="true"
        tabIndex={-1}
      />

      {/* Main content area (drag-and-drop target) */}
      <div
        className="relative flex flex-1 flex-col min-w-0 pb-16 md:pb-0"
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        onDrop={handleDrop}
      >
        {/* Top navbar */}
        <Navbar
          breadcrumbs={effectiveBreadcrumbs}
          onBreadcrumbNavigate={navigateToBreadcrumb}
          searchQuery={searchQuery}
          onSearchChange={setSearchQuery}
          viewMode={viewMode}
          onViewModeToggle={handleViewModeToggle}
          isDetailsOpen={isDetailsOpen}
          onToggleDetails={() => setIsDetailsOpen((prev) => !prev)}
          onToggleMobileSidebar={() => setMobileSidebarOpen((o) => !o)}
        />

        <div className="flex flex-1 w-full h-full overflow-hidden relative">
          <div className="flex-1 flex flex-col min-h-0 overflow-y-auto" onClick={clearSelection}>
            {/* Error banner */}
            {fetchError && (
          <div className="px-6 pt-4">
            <Alert variant="error">
              <div className="flex items-center justify-between">
                <span>{fetchError}</span>
                <button
                  onClick={() => void fetchDirectory(currentFolderId)}
                  className="ml-4 rounded-md border border-zinc-700 bg-zinc-900 px-2.5 py-1 text-xs font-medium text-zinc-300 transition-colors hover:bg-zinc-800 hover:text-zinc-50"
                >
                  Retry
                </button>
              </div>
            </Alert>
          </div>
        )}

        {/* File list / grid / skeleton */}
        {isLoading || !isCurrentStateLoaded ? (
          <DirectorySkeleton />
        ) : viewMode === 'list' ? (
          <ListView
            items={filteredItems}
            selectedIds={selectedIds}
            isTrash={isInTrash}
            isShared={activeNav === 'shared'}
            onToggleSelect={handleToggleSelect}
            onSingleSelect={handleSingleSelect}
            onSelectRange={handleSelectRange}
            onSelectAll={handleSelectAll}
            onOpenFolder={(item) => navigateToFolder(item)}
            onOpenFile={handleOpenFile}
            onContextMenu={handleItemContextMenu}
          />
        ) : (
          <GridView
            items={filteredItems}
            selectedIds={selectedIds}
            onToggleSelect={handleToggleSelect}
            onSingleSelect={handleSingleSelect}
            onSelectRange={handleSelectRange}
            onOpenFolder={(item) => navigateToFolder(item)}
            onOpenFile={handleOpenFile}
              onContextMenu={handleItemContextMenu}
            />
          )}
          </div>
          <DetailPanel
            item={selectedIds.size === 1 ? filteredItems.find(it => selectedIds.has(it.id)) || null : null}
            isOpen={isDetailsOpen}
            onClose={() => setIsDetailsOpen(false)}
          />
        </div>

        {/* Drag overlay */}
        {isDragging && !isInTrash && activeNav === 'drive' && (
          <div className="pointer-events-none absolute inset-0 z-40 flex items-center justify-center bg-zinc-950/80 backdrop-blur-sm animate-fade-in">
            <div className="flex flex-col items-center gap-3 rounded-2xl border-2 border-dashed border-violet-500/60 px-12 py-10">
              <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" className="text-violet-400" aria-hidden="true">
                <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                <polyline points="17,8 12,3 7,8" />
                <line x1="12" y1="3" x2="12" y2="15" />
              </svg>
              <p className="text-sm font-medium text-zinc-50">Drop files to upload</p>
              <p className="text-xs text-zinc-500">They will be added to the current folder</p>
            </div>
          </div>
        )}
      </div>

      {/* Floating Bulk Action Toolbar */}
      <BulkActionBar
        selectedCount={selectedIds.size}
        isTrash={isInTrash}
        disableWriteActions={hasSelectedViewerItem}
        onMove={() => setBulkMoveOpen(true)}
        onDelete={handleBulkSoftDelete}
        onRestore={handleBulkRestore}
        onDeletePermanent={handleBulkHardDelete}
        onDownload={handleBulkDownload}
        onDeselect={clearSelection}
      />

      {/* New folder modal */}
      <NewFolderModal
        open={folderModalOpen}
        onClose={() => setFolderModalOpen(false)}
        parentId={currentFolderId}
        onCreated={handleFolderCreated}
      />

      {/* Floating right-click context menu (Phase 7.4 + Trash) */}
      <ContextMenu
        item={menuTarget}
        position={menuPosition}
        onClose={closeContextMenu}
        actions={menuActions}
        isTrash={isInTrash}
        role={menuTarget ? (menuTarget.user_id === user?.user_id ? 'OWNER' : (menuTarget.role || 'VIEWER')) : 'VIEWER'}
      />

      {/* Action modals (Phase 7.4 + Trash) */}
      <ShareModal
        open={shareTarget !== null}
        onClose={() => setShareTarget(null)}
        file={shareTarget}
      />
      <RenameModal
        open={renameTarget !== null}
        onClose={() => setRenameTarget(null)}
        file={renameTarget}
        onRenamed={handleRenamed}
      />
      <MoveModal
        open={moveTarget !== null || bulkMoveOpen}
        onClose={() => {
          setMoveTarget(null)
          setBulkMoveOpen(false)
        }}
        file={moveTarget}
        files={bulkMoveOpen ? filteredItems.filter((it) => selectedIds.has(it.id)) : null}
        onMoved={(itemIds, newParentId) => {
          if (Array.isArray(itemIds)) {
            handleBulkMoved(itemIds, newParentId)
          } else {
            handleMoved(itemIds, newParentId)
          }
        }}
      />
      <DeleteModal
        open={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        file={deleteTarget}
        onDeleted={handleDeleted}
        isPermanent={false}
      />
      <DeleteModal
        open={permanentDeleteTarget !== null || bulkDeletePermanentOpen}
        onClose={() => {
          setPermanentDeleteTarget(null)
          setBulkDeletePermanentOpen(false)
        }}
        file={
          permanentDeleteTarget ||
          (bulkDeletePermanentOpen
            ? {
                id: 'bulk',
                name: `${selectedIds.size} items`,
                is_directory: false,
              }
            : null)
        }
        onDeleted={(itemId) => {
          if (itemId === 'bulk' || bulkDeletePermanentOpen) {
            void handleBulkHardDelete()
          } else {
            handleDeleted(itemId)
          }
        }}
        isPermanent={true}
      />
      <SettingsModal
        open={settingsModalOpen}
        onClose={() => setSettingsModalOpen(false)}
      />
      <FilePreviewModal
        open={previewTarget !== null}
        onClose={() => setPreviewTarget(null)}
        file={previewTarget}
        onDownload={handleDownload}
      />
      <ShortcutModal
        open={shortcutTarget !== null}
        onClose={() => setShortcutTarget(null)}
        file={shortcutTarget}
        onCreated={() => {
          pushToast({ message: 'Shortcut created successfully.', variant: 'success' })
          void fetchDirectory(currentFolderId)
        }}
      />
      <PublicShareModal
        open={publicShareTarget !== null}
        onClose={() => setPublicShareTarget(null)}
        file={publicShareTarget}
      />
      <PassphraseModal
        open={downloadDecryptTarget !== null}
        onClose={() => {
          if (!isDecrypting) setDownloadDecryptTarget(null)
        }}
        title="Decrypt Download"
        description="This file is end-to-end encrypted. Enter the passphrase to decrypt it locally."
        actionLabel={isDecrypting ? 'Decrypting...' : 'Download & Decrypt'}
        onSubmit={async (passphrase) => {
          if (!downloadDecryptTarget) return
          setIsDecrypting(true)
          try {
            await downloadEncryptedFile(
              downloadDecryptTarget.target_id ?? downloadDecryptTarget.id,
              downloadDecryptTarget.name,
              passphrase
            )
            pushToast({ message: 'Decrypted and downloaded successfully.', variant: 'success' })
            setDownloadDecryptTarget(null)
          } catch (err) {
            pushToast({ message: (err as Error).message || 'Decryption failed.', variant: 'error' })
          } finally {
            setIsDecrypting(false)
          }
        }}
      />
      <PassphraseModal
        open={pendingEncryptedUpload !== null}
        onClose={() => setPendingEncryptedUpload(null)}
        title="Encrypt Upload"
        description={`Enter a passphrase to securely encrypt ${pendingEncryptedUpload?.isFolder ? 'this folder' : 'these files'} before uploading.`}
        actionLabel="Encrypt & Upload"
        onSubmit={(passphrase) => {
          if (!pendingEncryptedUpload) return
          if (pendingEncryptedUpload.isFolder) {
            void uploadFolder(pendingEncryptedUpload.files, currentFolderId, passphrase)
          } else {
            for (const file of pendingEncryptedUpload.files) {
              uploadFile(file, currentFolderId, undefined, passphrase)
            }
          }
          setPendingEncryptedUpload(null)
          pushToast({ message: 'Upload started with encryption.', variant: 'success' })
        }}
      />

      {/* Floating upload queue overlay */}
      <UploadQueue />

      {/* Phase 12: File Versioning Modal */}
      <VersionHistoryModal
        open={!!versionHistoryTarget}
        onClose={() => setVersionHistoryTarget(null)}
        file={versionHistoryTarget}
        onRestoreComplete={() => void fetchDirectory()}
      />

      {infoTarget && (
        <GetInfoModal
          item={infoTarget}
          onClose={() => setInfoTarget(null)}
        />
      )}

      <MobileNav
        activeNav={isInTrash ? 'trash' : activeNav}
        onSelectNav={(navId) => {
          if (navId === 'shared') {
            setSearchParams({ nav: 'shared' })
          } else if (navId === 'trash') {
            setSearchParams({ nav: 'trash' })
          } else {
            setSearchParams({})
          }
        }}
        onOpenSettings={() => setSettingsModalOpen(true)}
        onUploadFile={handleUploadFile}
        disableNew={isInTrash}
      />
    </div>
  )
}

export default Dashboard
