import { useCallback, useEffect, useMemo, useRef, useState, type ChangeEvent, type DragEvent, type MouseEvent } from 'react'
import { useSearchParams } from 'react-router-dom'
import axios from 'axios'
import { apiClient } from '../lib/api'
import { getAccessToken } from '../lib/token'
import { useAuth } from '../hooks/useAuth'
import { useWebSocket } from '../hooks/useWebSocket'
import { useUpload, UPLOAD_COMPLETE_EVENT } from '../context/UploadContext'
import { useToast } from '../components/Toast'
import type { FileItem, BreadcrumbNode, FileStatus, ShareInvitation } from '../types/file'
import type {
  WSMessage,
  ThumbnailReadyPayload,
  UploadCompletedPayload,
  FileSharedPayload,
  ShareInvitationPayload,
  DeltaSyncResponse,
  WebSocketStatus,
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
import { UploadConflictModal } from '../components/UploadConflictModal'
import { DetailPanel } from '../components/DetailPanel'
import { NotificationsView } from '../components/NotificationsView'
import { SandboxedPreviewModal } from '../components/SandboxedPreviewModal'
import { FilterChipsBar, type TypeFilter, type DateFilter, type SortField, type SortDirection } from '../components/FilterChipsBar'

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
  const { push: pushToast } = useToast()
  const [searchParams, setSearchParams] = useSearchParams()

  // ---- Navigation state (driven by URL search params for Chrome Back/Forward support) ----
  const rawNav = searchParams.get('nav')
  const activeNav: 'drive' | 'shared' | 'recent' | 'trash' | 'notifications' =
    rawNav === 'shared' ? 'shared' :
    rawNav === 'recent' ? 'recent' :
    rawNav === 'trash' ? 'trash' :
    rawNav === 'notifications' ? 'notifications' :
    'drive'
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

  // Phase 2: Dropbox-grade delta sync tracking
  const syncCursorRef = useRef<number>(0)
  const isSyncingRef = useRef<boolean>(false)
  const currentFolderIdRef = useRef<string | null>(currentFolderId)
  const activeNavRef = useRef<string>(activeNav)
  const searchQueryRef = useRef<string>(searchQuery)
  useEffect(() => {
    currentFolderIdRef.current = currentFolderId
  }, [currentFolderId])
  useEffect(() => {
    activeNavRef.current = activeNav
  }, [activeNav])
  useEffect(() => {
    searchQueryRef.current = searchQuery
  }, [searchQuery])

  // Automatically reset isTrashContext whenever activeNav is not trash
  useEffect(() => {
    if (activeNav !== 'trash') {
      setIsTrashContext(false)
    }
  }, [activeNav])

  const isInTrash = activeNav === 'trash' || (activeNav === 'drive' && isTrashContext)

  const currentKey = activeNav + ':' + (currentFolderId || '')
  const isCurrentStateLoaded = lastFetchedKey === currentKey

  const effectiveBreadcrumbs = useMemo<BreadcrumbNode[]>(() => {
    if (activeNav === 'notifications') {
      return [{ id: null, name: 'Notifications' }]
    }
    if (activeNav === 'shared') {
      return [{ id: null, name: 'Shared with me' }]
    }
    if (activeNav === 'recent') {
      return [{ id: null, name: 'Recent' }]
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
    if (activeNav === 'notifications') {
      setBreadcrumbs([{ id: null, name: 'Notifications' }])
    } else if (activeNav === 'shared') {
      setBreadcrumbs([{ id: null, name: 'Shared with me' }])
    } else if (activeNav === 'recent') {
      setBreadcrumbs([{ id: null, name: 'Recent' }])
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
  const [isDetailsOpen, setIsDetailsOpen] = useState(false)

  // File Versioning & Conflict State
  const [pendingConflicts, setPendingConflicts] = useState<
    Array<{ file: File; parentId: string | null; passphrase?: string }>
  >([])
  const versionUploadTargetRef = useRef<FileItem | null>(null)
  const versionFileInputRef = useRef<HTMLInputElement>(null)

  // Share Invitations & Safety Gate state (Option A)
  const [invitations, setInvitations] = useState<ShareInvitation[]>([])
  const [loadingInvitationId, setLoadingInvitationId] = useState<string | null>(null)
  const [previewInvitation, setPreviewInvitation] = useState<ShareInvitation | null>(null)

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
  // ---- Tab SWR Cache for 0ms instant tab switching ----
  const tabCacheRef = useRef<Map<string, FileItem[]>>(new Map())

  // ---- Fetch directory contents ----
  const fetchDirectory = useCallback(async (
    folderId: string | null = currentFolderIdRef.current,
    navMode: string = activeNavRef.current,
    query: string = searchQueryRef.current
  ) => {
    // Abort any in-flight request
    abortRef.current?.abort()
    const controller = new AbortController()
    abortRef.current = controller

    if (navMode === 'notifications') {
      setItems([])
      setIsTrashContext(false)
      setIsLoading(false)
      setLastFetchedKey('notifications:')
      return
    }

    const cacheKey = `${navMode}:${folderId || ''}:${query.trim()}`

    // Stale-While-Revalidate: render cached tab items immediately for zero-lag switching
    if (tabCacheRef.current.has(cacheKey) && !query.trim()) {
      setItems(tabCacheRef.current.get(cacheKey)!)
      setIsLoading(false)
    } else {
      setIsLoading(true)
    }

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
      const mapped = rawData.map(item => {
        const isSupportedMedia = !item.is_directory && (
          /\.(jpg|jpeg|png|webp|gif|mp4|webm|mov|mkv|pdf)$/i.test(item.name) ||
          item.mime_type?.startsWith('image/') ||
          item.mime_type?.startsWith('video/') ||
          item.mime_type === 'application/pdf'
        )
        if (isSupportedMedia && !item.thumbnail_url) {
          return {
             ...item,
             thumbnail_url: `${apiClient.defaults.baseURL}/files/${item.id}/thumbnail?token=${token}`
          }
        }
        return item
      })

      // In Recent tab, keep the backend's chronological activity order!
      let sorted = mapped
      if (navMode !== 'recent') {
        sorted = [...mapped].sort((a, b) => {
          if (a.is_directory !== b.is_directory) return a.is_directory ? -1 : 1
          return a.name.localeCompare(b.name)
        })
      }

      tabCacheRef.current.set(cacheKey, sorted)
      setItems(sorted)
      setLastFetchedKey(navMode + ':' + (folderId || ''))

      // Extract X-Delta-Cursor header if provided, eliminating any race window
      const deltaCursorHeader = res.headers['x-delta-cursor']
      if (deltaCursorHeader) {
        const parsed = parseInt(deltaCursorHeader, 10)
        if (!isNaN(parsed) && parsed >= 0) {
          syncCursorRef.current = parsed
        }
      } else if (syncCursorRef.current === 0) {
        // Fallback: sync latest cursor baseline on initial load only
        apiClient.get<{ cursor: number }>('/sync/cursor')
          .then(cRes => {
            if (cRes.data && typeof cRes.data.cursor === 'number') {
              syncCursorRef.current = cRes.data.cursor
            }
          })
          .catch(cErr => console.error('Failed to sync initial cursor', cErr))
      }

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
    if (!searchQuery.trim()) return

    const delayDebounceFn = setTimeout(() => {
      void fetchDirectory(currentFolderId, activeNav, searchQuery)
    }, 400)

    return () => clearTimeout(delayDebounceFn)
  }, [searchQuery, currentFolderId, activeNav, fetchDirectory])

  // Cleanup abort on unmount
  useEffect(() => {
    return () => { abortRef.current?.abort() }
  }, [])

  // ---- Share Invitations (Collaborative Safety Gate - Option A) ----
  const fetchInvitations = useCallback(async () => {
    try {
      const res = await apiClient.get<ShareInvitation[]>('/shares/invitations')
      setInvitations(res.data ?? [])
    } catch {
      // Ignored if permissions are unavailable or during temporary network disruption
    }
  }, [])

  useEffect(() => {
    void fetchInvitations()
  }, [fetchInvitations, activeNav])

  const handleAcceptInvitation = useCallback(async (inv: ShareInvitation) => {
    setLoadingInvitationId(inv.id)
    try {
      await apiClient.post(`/shares/invitations/${inv.id}/accept`)
      pushToast({
        variant: 'success',
        message: `Accepted "${inv.file_name}" and added to your Drive!`,
      })
      setInvitations((prev) => prev.filter((i) => i.id !== inv.id))
      if (activeNav === 'shared') {
        void fetchDirectory(null, 'shared')
      }
    } catch {
      pushToast({
        variant: 'error',
        message: 'Failed to accept share invitation.',
      })
    } finally {
      setLoadingInvitationId(null)
    }
  }, [activeNav, fetchDirectory, pushToast])

  const handleDeclineInvitation = useCallback(async (inv: ShareInvitation) => {
    setLoadingInvitationId(inv.id)
    try {
      await apiClient.post(`/shares/invitations/${inv.id}/decline`)
      pushToast({
        variant: 'info',
        message: `Declined share for "${inv.file_name}".`,
      })
      setInvitations((prev) => prev.filter((i) => i.id !== inv.id))
    } catch {
      pushToast({
        variant: 'error',
        message: 'Failed to decline share invitation.',
      })
    } finally {
      setLoadingInvitationId(null)
    }
  }, [pushToast])

  const handleBlockSender = useCallback(async (inv: ShareInvitation) => {
    setLoadingInvitationId(inv.id)
    try {
      await apiClient.post(`/shares/invitations/${inv.id}/block`)
      pushToast({
        variant: 'info',
        message: `Blocked ${inv.sender_email} and dismissed invitation.`,
      })
      setInvitations((prev) => prev.filter((i) => i.id !== inv.id))
    } catch {
      pushToast({
        variant: 'error',
        message: 'Failed to block sender.',
      })
    } finally {
      setLoadingInvitationId(null)
    }
  }, [pushToast])

  const handlePreviewInvitation = useCallback((inv: ShareInvitation) => {
    setPreviewInvitation(inv)
  }, [])

  const [isBatchProcessing, setIsBatchProcessing] = useState(false)

  const handleAcceptAll = useCallback(async () => {
    if (invitations.length === 0) return
    setIsBatchProcessing(true)
    let successCount = 0
    for (const inv of invitations) {
      try {
        await apiClient.post(`/shares/invitations/${inv.id}/accept`)
        successCount++
      } catch {
        // continue
      }
    }
    setInvitations([])
    pushToast({
      variant: 'success',
      message: `Accepted ${successCount} invitation${successCount === 1 ? '' : 's'} and added to Shared with me!`,
    })
    if (activeNav === 'shared') {
      void fetchDirectory(null, 'shared')
    }
    setIsBatchProcessing(false)
  }, [invitations, activeNav, fetchDirectory, pushToast])

  const handleDeclineAll = useCallback(async () => {
    if (invitations.length === 0) return
    setIsBatchProcessing(true)
    let successCount = 0
    for (const inv of invitations) {
      try {
        await apiClient.post(`/shares/invitations/${inv.id}/decline`)
        successCount++
      } catch {
        // continue
      }
    }
    setInvitations([])
    pushToast({
      variant: 'info',
      message: `Declined ${successCount} invitation${successCount === 1 ? '' : 's'}.`,
    })
    setIsBatchProcessing(false)
  }, [invitations, pushToast])

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

  // ---- Advanced Filter Chips & Sorting ----
  const [typeFilter, setTypeFilter] = useState<TypeFilter>('all')
  const [personFilter, setPersonFilter] = useState<string | null>(null)
  const [dateFilter, setDateFilter] = useState<DateFilter>('all')
  const [sortField, setSortField] = useState<SortField>('name')
  const [sortDirection, setSortDirection] = useState<SortDirection>('asc')

  useEffect(() => {
    setTypeFilter('all')
    setPersonFilter(null)
    setDateFilter('all')
  }, [activeNav])

  const handleResetFilters = useCallback(() => {
    setTypeFilter('all')
    setPersonFilter(null)
    setDateFilter('all')
  }, [])

  const handleSortChange = useCallback((field: SortField) => {
    if (sortField === field) {
      setSortDirection((prev) => (prev === 'asc' ? 'desc' : 'asc'))
    } else {
      setSortField(field)
      setSortDirection(field === 'name' ? 'asc' : 'desc')
    }
  }, [sortField])

  const handleToggleSortDirection = useCallback(() => {
    setSortDirection((prev) => (prev === 'asc' ? 'desc' : 'asc'))
  }, [])

  const availablePeople = useMemo(() => {
    const set = new Set<string>()
    items.forEach((it) => {
      if (it.shared_by_email) set.add(it.shared_by_email)
      if (it.owner_email) {
        if (!user?.email || it.owner_email !== user.email) {
          set.add(it.owner_email)
        }
      }
    })
    invitations.forEach((inv) => {
      if (inv.sender_email) set.add(inv.sender_email)
    })
    return Array.from(set).sort()
  }, [items, user?.email, invitations])

  const filteredItems = useMemo(() => {
    let result = [...items]

    // 1. Type Filter
    if (typeFilter !== 'all') {
      result = result.filter((it) => {
        if (typeFilter === 'folders') return it.is_directory
        if (it.is_directory) return false
        const ext = it.name.split('.').pop()?.toLowerCase() || ''
        const mime = it.mime_type?.toLowerCase() || ''
        if (typeFilter === 'pdfs') {
          return ext === 'pdf' || mime === 'application/pdf'
        }
        if (typeFilter === 'documents') {
          return ['doc', 'docx', 'txt', 'rtf', 'odt', 'md'].includes(ext) || mime.includes('word') || mime.includes('text')
        }
        if (typeFilter === 'spreadsheets') {
          return ['xls', 'xlsx', 'csv', 'ods'].includes(ext) || mime.includes('sheet') || mime.includes('csv')
        }
        if (typeFilter === 'images') {
          return ['jpg', 'jpeg', 'png', 'webp', 'gif', 'svg', 'bmp'].includes(ext) || mime.startsWith('image/')
        }
        if (typeFilter === 'videos') {
          return ['mp4', 'webm', 'mov', 'mkv', 'avi'].includes(ext) || mime.startsWith('video/')
        }
        if (typeFilter === 'audio') {
          return ['mp3', 'wav', 'ogg', 'm4a', 'flac'].includes(ext) || mime.startsWith('audio/')
        }
        return true
      })
    }

    // 2. People Filter
    if (personFilter !== null) {
      result = result.filter((it) => {
        return (
          it.shared_by_email?.toLowerCase() === personFilter.toLowerCase() ||
          it.owner_email?.toLowerCase() === personFilter.toLowerCase()
        )
      })
    }

    // 3. Date Filter
    if (dateFilter !== 'all') {
      const now = new Date().getTime()
      const dayMs = 24 * 60 * 60 * 1000
      result = result.filter((it) => {
        const itemDate = new Date(it.shared_at || it.updated_at || it.created_at).getTime()
        const diffMs = now - itemDate
        if (dateFilter === 'today') return diffMs <= dayMs
        if (dateFilter === '7days') return diffMs <= 7 * dayMs
        if (dateFilter === '30days') return diffMs <= 30 * dayMs
        if (dateFilter === 'this_year') return diffMs <= 365 * dayMs
        return true
      })
    }

    // 4. Sorting (Directories always grouped first, then items sorted by chosen field & direction)
    result.sort((a, b) => {
      if (a.is_directory !== b.is_directory) {
        return a.is_directory ? -1 : 1
      }

      let cmp = 0
      if (sortField === 'name') {
        cmp = a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' })
      } else if (sortField === 'date') {
        const dateA = new Date(a.shared_at || a.updated_at || a.created_at).getTime() || 0
        const dateB = new Date(b.shared_at || b.updated_at || b.created_at).getTime() || 0
        cmp = dateA - dateB
      } else if (sortField === 'size') {
        const sizeA = (a.aggregate_size ?? a.size_bytes) || 0
        const sizeB = (b.aggregate_size ?? b.size_bytes) || 0
        cmp = sizeA - sizeB
      }

      return sortDirection === 'asc' ? cmp : -cmp
    })

    return result
  }, [items, typeFilter, personFilter, dateFilter, sortField, sortDirection])

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

  /**
   * Build the clean WS URL from the configured API base, upgrading http(s) → ws(s).
   * Authentication is performed via the secure in-band first-message handshake,
   * keeping JWTs completely off URL query logs.
   */
  const wsUrl = useMemo(() => {
    if (!token) return null
    const apiBase = (import.meta.env.VITE_API_BASE as string | undefined) ?? '/api'
    let base: string
    if (/^https?:\/\//i.test(apiBase)) {
      base = apiBase
    } else if (typeof window !== 'undefined') {
      base = window.location.origin + apiBase
    } else {
      return null
    }
    const wsBase = base.replace(/^http/i, 'ws')
    return `${wsBase}/ws`
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
  const deltaRefreshTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
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
   * Phase 2: Dropbox-grade delta sync engine.
   * Incrementally polls changes since `syncCursorRef.current` and patches `items`
   * in-memory with O(1) state mutations instead of full directory reloads.
   */
  const applyDeltaSync = useCallback(async () => {
    if (isSyncingRef.current) return
    isSyncingRef.current = true

    try {
      let hasMore = true
      const updatedItemIds = new Set<string>()
      while (hasMore) {
        const since = syncCursorRef.current
        const res = await apiClient.get<DeltaSyncResponse>(`/sync/delta?since=${since}&limit=100`)
        const { entries, next_cursor, has_more, reset_cursor } = res.data

        if (reset_cursor) {
          syncCursorRef.current = next_cursor
          void fetchDirectory(currentFolderIdRef.current)
          break
        }

        if (entries && entries.length > 0) {
          setItems((prevItems) => {
            let updated = [...prevItems]
            const token = getAccessToken() || ''
            const curFolder = currentFolderIdRef.current ?? null
            const isDriveHierarchy = activeNavRef.current === 'drive' && searchQueryRef.current.trim() === ''

            for (const entry of entries) {
              const isSupportedMedia = !entry.is_directory && (
                /\.(jpg|jpeg|png|webp|gif|mp4|webm|mov|mkv|pdf)$/i.test(entry.name) ||
                entry.mime_type?.startsWith('image/') ||
                entry.mime_type?.startsWith('video/') ||
                entry.mime_type === 'application/pdf'
              )
              const fallbackThumb = isSupportedMedia
                ? `${apiClient.defaults.baseURL}/files/${entry.file_id}/thumbnail?token=${token}`
                : undefined
              const entryParent = entry.parent_id ?? null

              switch (entry.action) {
                case 'FILE_CREATED': {
                  if (isDriveHierarchy && entryParent === curFolder) {
                    const existingIdx = updated.findIndex((i) => i.id === entry.file_id)
                    const item: FileItem = {
                      id: entry.file_id,
                      user_id: entry.user_id,
                      name: entry.name,
                      status: (entry.status as FileStatus) || 'ACTIVE',
                      parent_id: entry.parent_id,
                      is_directory: entry.is_directory,
                      size_bytes: entry.size_bytes,
                      mime_type: entry.mime_type,
                      thumbnail_url: entry.thumbnail_url || fallbackThumb,
                      created_at: entry.created_at,
                      updated_at: entry.created_at,
                    }
                    if (existingIdx >= 0) {
                      updated[existingIdx] = { ...updated[existingIdx], ...item }
                    } else {
                      updated.push(item)
                    }
                  }
                  break
                }

                case 'FILE_UPDATED': {
                  const existingIdx = updated.findIndex((i) => i.id === entry.file_id)
                  if (existingIdx >= 0) {
                    updated[existingIdx] = {
                      ...updated[existingIdx],
                      name: entry.name,
                      size_bytes: entry.size_bytes,
                      mime_type: entry.mime_type || updated[existingIdx].mime_type,
                      status: (entry.status as FileStatus) || updated[existingIdx].status,
                      thumbnail_url: entry.thumbnail_url || updated[existingIdx].thumbnail_url || fallbackThumb,
                      updated_at: entry.created_at,
                    }
                    updatedItemIds.add(entry.file_id)
                  }
                  break
                }

                case 'FILE_MOVED': {
                  const existingIdx = updated.findIndex((i) => i.id === entry.file_id)
                  if (isDriveHierarchy && entryParent === curFolder) {
                    if (existingIdx >= 0) {
                      updated[existingIdx] = {
                        ...updated[existingIdx],
                        parent_id: entry.parent_id,
                        name: entry.name,
                      }
                    } else {
                      updated.push({
                        id: entry.file_id,
                        user_id: entry.user_id,
                        name: entry.name,
                        status: (entry.status as FileStatus) || 'ACTIVE',
                        parent_id: entry.parent_id,
                        is_directory: entry.is_directory,
                        size_bytes: entry.size_bytes,
                        mime_type: entry.mime_type,
                        thumbnail_url: entry.thumbnail_url || fallbackThumb,
                        created_at: entry.created_at,
                        updated_at: entry.created_at,
                      })
                    }
                  } else {
                    if (existingIdx >= 0) {
                      updated.splice(existingIdx, 1)
                    }
                  }
                  break
                }

                case 'FILE_RENAMED': {
                  const existingIdx = updated.findIndex((i) => i.id === entry.file_id)
                  if (existingIdx >= 0) {
                    updated[existingIdx] = {
                      ...updated[existingIdx],
                      name: entry.name,
                      updated_at: entry.created_at,
                    }
                  }
                  break
                }

                case 'FILE_TRASHED': {
                  if (activeNavRef.current !== 'trash') {
                    updated = updated.filter((i) => i.id !== entry.file_id)
                  } else {
                    const existingIdx = updated.findIndex((i) => i.id === entry.file_id)
                    if (existingIdx < 0) {
                      updated.push({
                        id: entry.file_id,
                        user_id: entry.user_id,
                        name: entry.name,
                        status: (entry.status as FileStatus) || 'ACTIVE',
                        parent_id: entry.parent_id,
                        is_directory: entry.is_directory,
                        size_bytes: entry.size_bytes,
                        mime_type: entry.mime_type,
                        thumbnail_url: entry.thumbnail_url || fallbackThumb,
                        created_at: entry.created_at,
                        updated_at: entry.created_at,
                        deleted_at: entry.created_at,
                      })
                    }
                  }
                  break
                }

                case 'FILE_DELETED': {
                  updated = updated.filter((i) => i.id !== entry.file_id)
                  break
                }

                case 'FILE_RESTORED': {
                  if (isDriveHierarchy && entry.parent_id === curFolder) {
                    const existingIdx = updated.findIndex((i) => i.id === entry.file_id)
                    if (existingIdx >= 0) {
                      updated[existingIdx] = {
                        ...updated[existingIdx],
                        deleted_at: null,
                      }
                    } else {
                      updated.push({
                        id: entry.file_id,
                        user_id: entry.user_id,
                        name: entry.name,
                        status: (entry.status as FileStatus) || 'ACTIVE',
                        parent_id: entry.parent_id,
                        is_directory: entry.is_directory,
                        size_bytes: entry.size_bytes,
                        mime_type: entry.mime_type,
                        thumbnail_url: entry.thumbnail_url || fallbackThumb,
                        created_at: entry.created_at,
                        updated_at: entry.created_at,
                      })
                    }
                  } else if (activeNavRef.current === 'trash') {
                    // Item restored out of Trash: remove from Trash view immediately
                    updated = updated.filter((i) => i.id !== entry.file_id)
                  }
                  break
                }
              }
            }

            return updated.sort((a, b) => {
              if (a.is_directory !== b.is_directory) return a.is_directory ? -1 : 1
              return a.name.localeCompare(b.name)
            })
          })
        }

        syncCursorRef.current = next_cursor
        hasMore = has_more
      }

      // Batch or debounce background metadata refresh (AI tags, AI summaries)
      if (updatedItemIds.size > 0) {
        if (updatedItemIds.size <= 3) {
          const ids = Array.from(updatedItemIds)
          Promise.allSettled(ids.map((id) => apiClient.get<FileItem>(`/files/${id}`)))
            .then((results) => {
              const fresh: FileItem[] = []
              for (const r of results) {
                if (r.status === 'fulfilled' && r.value.data) {
                  fresh.push(r.value.data)
                }
              }
              if (fresh.length > 0) {
                const map = new Map(fresh.map((item) => [item.id, item]))
                setItems((prev) => prev.map((it) => (map.has(it.id) ? { ...it, ...map.get(it.id)! } : it)))
              }
            })
            .catch(() => {})
        } else {
          // Large burst update: Debounce a single fetchDirectory to avoid request storms
          if (deltaRefreshTimerRef.current) {
            clearTimeout(deltaRefreshTimerRef.current)
          }
          deltaRefreshTimerRef.current = setTimeout(() => {
            void fetchDirectory(currentFolderIdRef.current)
          }, 500)
        }
      }
    } catch (err: any) {
      console.error('Failed to apply delta sync, falling back to full refresh', err)
      if (err.response?.status === 410 || err.response?.status === 400) {
        try {
          const curRes = await apiClient.get<{ cursor: number }>('/sync/cursor')
          syncCursorRef.current = curRes.data.cursor
        } catch {
          syncCursorRef.current = 0
        }
      }
      void fetchDirectory(currentFolderIdRef.current)
    } finally {
      isSyncingRef.current = false
    }
  }, [fetchDirectory])

  /**
   * Central dispatcher for incoming WS messages. Kept stable via refs so the
   * socket never resubscribes when items change.
   */
  const handleWsMessage = useCallback(
    (msg: WSMessage) => {
      switch (msg.type) {
        case 'SYNC_DELTA': {
          void applyDeltaSync()
          break
        }
        case 'AI_METADATA_READY': {
          const payload = msg.payload as { file_id?: string; tags?: string | null; summary?: string | null }
          if (payload?.file_id) {
            setItems((prev) =>
              prev.map((it) =>
                it.id === payload.file_id
                  ? {
                      ...it,
                      tags: payload.tags !== undefined ? payload.tags : it.tags,
                      summary: payload.summary !== undefined ? payload.summary : it.summary,
                    }
                  : it
              )
            )
          }
          break
        }
        case 'UPLOAD_COMPLETED': {
          const payload = msg.payload as UploadCompletedPayload
          // Incrementally sync newly created/uploaded file deltas
          void applyDeltaSync()
          const name = filenameForId(payload.file_id)
          triggerBatchUploadToast(name)

          // Adaptive smart polling for background worker results (AI summary & thumbnails)
          if (payload.file_id) {
            const fileId = payload.file_id
            const pollDelays = [3000, 4000, 5000, 6000] // adaptive checks at 3s, 7s, 12s, 18s
            let attempt = 0
            const pollForUpdates = () => {
              apiClient
                .get<FileItem>(`/files/${fileId}`)
                .then((res) => {
                  if (res.data) {
                    const updatedFile = res.data
                    setItems((prev) =>
                      prev.map((it) => (it.id === fileId ? { ...it, ...updatedFile } : it))
                    )
                    // If AI summary or AI tags are ready, terminate polling early
                    if (updatedFile.summary || updatedFile.tags) {
                      return
                    }
                  }
                  attempt++
                  if (attempt < pollDelays.length) {
                    setTimeout(pollForUpdates, pollDelays[attempt])
                  }
                })
                .catch(() => {
                  attempt++
                  if (attempt < pollDelays.length) {
                    setTimeout(pollForUpdates, pollDelays[attempt])
                  }
                })
            }
            setTimeout(pollForUpdates, pollDelays[0])
          }
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
                setSearchParams({ nav: 'shared' })
              },
            },
          })
          if (activeNav === 'shared') {
            void fetchDirectory(null, 'shared')
          }
          break
        }
        case 'SHARE_INVITATION': {
          const payload = msg.payload as ShareInvitationPayload
          pushToast({
            variant: 'info',
            message: `${payload.shared_by || 'Someone'} sent you a share invitation for: ${payload.filename || 'Untitled'}`,
            action: {
              label: 'Review',
              onClick: () => {
                setSearchParams({ nav: 'shared' })
              },
            },
          })
          void fetchInvitations()
          break
        }
        case 'VIRUS_DETECTED': {
          const payload = msg.payload as any
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
    [currentFolderId, filenameForId, applyThumbnail, pushToast, triggerBatchUploadToast, applyDeltaSync, fetchDirectory, activeNav, setSearchParams, fetchInvitations],
  )

  const { status: wsStatus, isCircuitBroken, retry: retryWs } = useWebSocket({
    url: wsUrl,
    token,
    enabled: token !== null,
    onMessage: handleWsMessage,
  })

  // Reconnect catchup: when socket reconnects, catch up on missed deltas
  const prevWsStatusRef = useRef<WebSocketStatus>(wsStatus)
  useEffect(() => {
    if (prevWsStatusRef.current !== 'CONNECTED' && wsStatus === 'CONNECTED') {
      if (syncCursorRef.current > 0) {
        void applyDeltaSync()
      }
    }
    prevWsStatusRef.current = wsStatus
  }, [wsStatus, applyDeltaSync])

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
      onOpenFolder: (item) => navigateToFolder(item),
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
      onUploadVersion: (item) => {
        versionUploadTargetRef.current = item
        versionFileInputRef.current?.click()
      },
      onGetInfo: (item) => {
        setSelectedIds(new Set([item.id]))
        setLastSelectedId(item.id)
        setIsDetailsOpen(true)
      },
    }),
    [handleDownload, handleRestore, navigateToFolder],
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
    if (tabCacheRef.current) {
      for (const [key, cachedList] of tabCacheRef.current.entries()) {
        tabCacheRef.current.set(key, cachedList.filter((it) => it.id !== itemId))
      }
    }
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
      if (activeNav === 'shared') {
        await Promise.all(ids.map((id) => apiClient.delete(`/shares/shared-with-me/${id}`)))
        pushToast({
          variant: 'success',
          message: `Successfully removed ${ids.length} shared item${ids.length > 1 ? 's' : ''} from your view.`,
        })
      } else {
        await apiClient.post('/files/bulk/delete', { ids })
        pushToast({
          variant: 'success',
          message: `Successfully moved ${ids.length} item${ids.length > 1 ? 's' : ''} to Trash.`,
        })
      }
      setItems((prev) => prev.filter((it) => !selectedIds.has(it.id)))
      if (tabCacheRef.current) {
        for (const [key, cachedList] of tabCacheRef.current.entries()) {
          tabCacheRef.current.set(key, cachedList.filter((it) => !selectedIds.has(it.id)))
        }
      }
      clearSelection()
    } catch {
      pushToast({ message: 'Failed to delete selected items.' })
    }
  }, [selectedIds, pushToast, clearSelection, activeNav])

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
  const processUploadFiles = useCallback(
    (files: File[], parentId: string | null, passphrase?: string) => {
      const conflicts: Array<{ file: File; parentId: string | null; passphrase?: string }> = []
      for (const file of files) {
        const lowerName = file.name.toLowerCase()
        const match = items.find((it) => it.name.toLowerCase() === lowerName)
        if (match) {
          if (match.is_directory) {
            pushToast({
              variant: 'error',
              message: `Cannot upload "${file.name}": A folder with this name already exists.`,
            })
          } else {
            conflicts.push({ file, parentId, passphrase })
          }
        } else {
          uploadFile(file, parentId, undefined, passphrase)
        }
      }
      if (conflicts.length > 0) {
        setPendingConflicts((prev) => [...prev, ...conflicts])
      }
    },
    [items, uploadFile, pushToast],
  )

  const handleResolveConflictUpdate = useCallback(() => {
    const current = pendingConflicts[0]
    if (current) {
      uploadFile(current.file, current.parentId, undefined, current.passphrase, 'version')
      pushToast({ message: `Updating version for ${current.file.name}...`, variant: 'info' })
    }
    setPendingConflicts((prev) => prev.slice(1))
  }, [pendingConflicts, uploadFile, pushToast])

  const handleResolveConflictKeepBoth = useCallback(() => {
    const current = pendingConflicts[0]
    if (current) {
      uploadFile(current.file, current.parentId, undefined, current.passphrase, 'copy')
      pushToast({ message: `Uploading copy for ${current.file.name}...`, variant: 'info' })
    }
    setPendingConflicts((prev) => prev.slice(1))
  }, [pendingConflicts, uploadFile, pushToast])

  const handleResolveConflictCancel = useCallback(() => {
    setPendingConflicts((prev) => prev.slice(1))
  }, [])

  const handleVersionFileChange = useCallback(
    (e: ChangeEvent<HTMLInputElement>) => {
      const selected = e.target.files?.[0]
      const target = versionUploadTargetRef.current
      if (selected && target) {
        const versioned = new File([selected], target.name, { type: selected.type })
        uploadFile(versioned, target.parent_id)
        pushToast({ message: `Uploading new version for ${target.name}...`, variant: 'info' })
      }
      e.target.value = ''
      versionUploadTargetRef.current = null
    },
    [uploadFile, pushToast],
  )

  /** Handle one or more files selected from the picker. */
  const handleFileChange = useCallback(
    (e: ChangeEvent<HTMLInputElement>) => {
      const files = e.target.files
      if (files) {
        processUploadFiles(Array.from(files), currentFolderId)
      }
      // Reset so selecting the same file again still fires onChange.
      e.target.value = ''
    },
    [processUploadFiles, currentFolderId],
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
        processUploadFiles(Array.from(files), currentFolderId)
      }
    },
    [processUploadFiles, currentFolderId, isInTrash, pushToast],
  )

  // Refresh the listing incrementally when any upload completes.
  useEffect(() => {
    const handler = () => void applyDeltaSync()
    window.addEventListener(UPLOAD_COMPLETE_EVENT, handler)
    return () => window.removeEventListener(UPLOAD_COMPLETE_EVENT, handler)
  }, [UPLOAD_COMPLETE_EVENT, applyDeltaSync])

  // Prompt conflict modal when a server-side 409 conflict is encountered
  useEffect(() => {
    const handleConflictEvent = (e: Event) => {
      const custom = e as CustomEvent<{ file: File; parentId: string | null; passphrase?: string }>
      if (custom.detail) {
        setPendingConflicts((prev) => [...prev, custom.detail])
      }
    }
    window.addEventListener('upload-conflict', handleConflictEvent)
    return () => window.removeEventListener('upload-conflict', handleConflictEvent)
  }, [])



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
          activeNav={activeNav}
          disableNew={isInTrash || activeNav === 'notifications' || activeNav === 'recent'}
          onSelectNav={(navId) => {
            setIsTrashContext(false)
            if (navId === 'notifications') {
              setSearchParams({ nav: 'notifications' })
            } else if (navId === 'shared') {
              setSearchParams({ nav: 'shared' })
            } else if (navId === 'recent') {
              setSearchParams({ nav: 'recent' })
            } else if (navId === 'trash') {
              setSearchParams({ nav: 'trash' })
            } else {
              setSearchParams({})
            }
            setMobileSidebarOpen(false)
          }}
          onSignOut={logout}
          syncStatus={wsStatus}
          isCircuitBroken={isCircuitBroken}
          onRetrySync={retryWs}
          pendingInvitationsCount={invitations.length}
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

      {/* Hidden native file input for direct context-menu version upload */}
      <input
        ref={versionFileInputRef}
        type="file"
        className="hidden"
        onChange={handleVersionFileChange}
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

        {/* Google Drive-style Filter Chips Bar */}
        {activeNav !== 'notifications' && (
          <FilterChipsBar
            typeFilter={typeFilter}
            onTypeFilterChange={setTypeFilter}
            personFilter={personFilter}
            onPersonFilterChange={setPersonFilter}
            dateFilter={dateFilter}
            onDateFilterChange={setDateFilter}
            availablePeople={availablePeople}
            totalItems={items.length}
            filteredItemsCount={filteredItems.length}
            onResetFilters={handleResetFilters}
            sortField={sortField}
            onSortFieldChange={handleSortChange}
            sortDirection={sortDirection}
            onToggleSortDirection={handleToggleSortDirection}
            isSharedView={activeNav === 'shared'}
          />
        )}

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

        {/* Dedicated Notifications View */}
        {activeNav === 'notifications' ? (
          <NotificationsView
            invitations={invitations}
            onAccept={handleAcceptInvitation}
            onDecline={handleDeclineInvitation}
            onBlockSender={handleBlockSender}
            onPreview={handlePreviewInvitation}
            onAcceptAll={handleAcceptAll}
            onDeclineAll={handleDeclineAll}
            loadingId={loadingInvitationId}
            isBatchLoading={isBatchProcessing}
          />
        ) : isLoading || !isCurrentStateLoaded ? (
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
            onFilterBySender={(email) => setPersonFilter(email)}
            sortField={sortField}
            sortDirection={sortDirection}
            onSortChange={handleSortChange}
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
            onFilterBySender={(email) => setPersonFilter(email)}
          />
        )}
          </div>
          <DetailPanel
            item={selectedIds.size === 1 ? filteredItems.find(it => selectedIds.has(it.id)) || null : null}
            isOpen={isDetailsOpen}
            onClose={() => setIsDetailsOpen(false)}
            onItemUpdated={(updated) => {
              setItems((prev) => prev.map((it) => (it.id === updated.id ? updated : it)))
            }}
            onManageAccess={(item) => setShareTarget(item)}
            isSharedView={activeNav === 'shared'}
            currentLocationPath={effectiveBreadcrumbs.map((b) => b.name).join(' / ')}
            currentUserEmail={user?.email}
          />
        </div>

        {/* Drag overlay */}
        {isDragging && !isInTrash && activeNav === 'drive' && (
          <div className="pointer-events-none absolute inset-0 z-40 flex items-center justify-center bg-zinc-950/80 backdrop-blur-sm animate-fade-in">
            <div className="flex flex-col items-center gap-3 rounded-2xl border-2 border-dashed border-amber-500/60 px-12 py-10">
              <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" className="text-amber-400" aria-hidden="true">
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
        file={shareTarget ? { id: shareTarget.target_id || shareTarget.id, name: shareTarget.name } : null}
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
        isShared={activeNav === 'shared'}
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
            processUploadFiles(pendingEncryptedUpload.files, currentFolderId, passphrase)
          }
          setPendingEncryptedUpload(null)
          pushToast({ message: 'Upload started with encryption.', variant: 'success' })
        }}
      />

      {/* Floating upload queue overlay */}
      <UploadQueue />

      {/* Upload Conflict Modal (Update Version vs Keep Both) */}
      <UploadConflictModal
        open={pendingConflicts.length > 0}
        onClose={handleResolveConflictCancel}
        fileName={pendingConflicts[0]?.file.name || ''}
        onUpdateVersion={handleResolveConflictUpdate}
        onKeepBoth={handleResolveConflictKeepBoth}
      />

      {/* Phase 12: File Versioning Modal */}
      <VersionHistoryModal
        open={!!versionHistoryTarget}
        onClose={() => setVersionHistoryTarget(null)}
        file={versionHistoryTarget}
        onRestoreComplete={() => void fetchDirectory()}
      />

      {/* Sandboxed Read-Only Preview Modal for Unaccepted Shares */}
      <SandboxedPreviewModal
        open={previewInvitation !== null}
        onClose={() => setPreviewInvitation(null)}
        invitation={previewInvitation}
        onAccept={handleAcceptInvitation}
        onDecline={handleDeclineInvitation}
      />

      <MobileNav
        activeNav={activeNav}
        onSelectNav={(navId) => {
          setIsTrashContext(false)
          if (navId === 'notifications') {
            setSearchParams({ nav: 'notifications' })
          } else if (navId === 'shared') {
            setSearchParams({ nav: 'shared' })
          } else if (navId === 'recent') {
            setSearchParams({ nav: 'recent' })
          } else if (navId === 'trash') {
            setSearchParams({ nav: 'trash' })
          } else {
            setSearchParams({})
          }
        }}
        onOpenSettings={() => setSettingsModalOpen(true)}
        onUploadFile={handleUploadFile}
        disableNew={isInTrash || activeNav === 'notifications' || activeNav === 'recent'}
        pendingInvitationsCount={invitations.length}
      />
    </div>
  )
}

export default Dashboard
