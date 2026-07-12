import { useCallback, useEffect, useMemo, useRef, useState, type ChangeEvent, type DragEvent } from 'react'
import axios from 'axios'
import { apiClient } from '../lib/api'
import { useAuth } from '../hooks/useAuth'
import { useUpload, UPLOAD_COMPLETE_EVENT } from '../context/UploadContext'
import type { FileItem, BreadcrumbNode } from '../types/file'
import { Sidebar } from '../components/Sidebar'
import { Navbar } from '../components/Navbar'
import { ListView } from '../components/ListView'
import { GridView } from '../components/GridView'
import { DirectorySkeleton } from '../components/DirectorySkeleton'
import { NewFolderModal } from '../components/NewFolderModal'
import { UploadQueue } from '../components/UploadQueue'
import { Alert } from '../components/ui/Alert'

/** Mocked storage limit for the gauge (15 GB in bytes). */
const STORAGE_LIMIT = 15 * 1_073_741_824
/** Mocked storage used (2.4 GB). */
const STORAGE_USED = 2.4 * 1_073_741_824

/**
 * File Explorer Dashboard.
 *
 * Manages the directory navigation state machine, API fetch lifecycle,
 * local search filtering, grid/list view toggle, and the new-folder modal.
 */
export function Dashboard() {
  const { logout } = useAuth()

  // ---- Navigation state ----
  const [currentFolderId, setCurrentFolderId] = useState<string | null>(null)
  const [breadcrumbs, setBreadcrumbs] = useState<BreadcrumbNode[]>([
    { id: null, name: 'My Drive' },
  ])
  const [items, setItems] = useState<FileItem[]>([])
  const [viewMode, setViewMode] = useState<'grid' | 'list'>('list')
  const [searchQuery, setSearchQuery] = useState('')
  const [isLoading, setIsLoading] = useState(true)
  const [fetchError, setFetchError] = useState<string | null>(null)

  // ---- Sidebar state ----
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)

  // ---- New folder modal state ----
  const [folderModalOpen, setFolderModalOpen] = useState(false)

  // ---- Abort controller ref for fetch cleanup ----
  const abortRef = useRef<AbortController | null>(null)

  // ---- Fetch directory contents ----
  const fetchDirectory = useCallback(async (folderId: string | null) => {
    // Abort any in-flight request
    abortRef.current?.abort()
    const controller = new AbortController()
    abortRef.current = controller

    setIsLoading(true)
    setFetchError(null)

    try {
      const url = folderId
        ? `/files?parent_id=${folderId}`
        : '/files'

      const res = await apiClient.get<FileItem[]>(url, {
        signal: controller.signal,
      })

      // Sort: directories first, then alphabetical by name
      const sorted = res.data.sort((a, b) => {
        if (a.is_directory !== b.is_directory) return a.is_directory ? -1 : 1
        return a.name.localeCompare(b.name)
      })

      setItems(sorted)
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

  // Fetch on mount and whenever currentFolderId changes
  useEffect(() => {
    void fetchDirectory(currentFolderId)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentFolderId])

  // Cleanup abort on unmount
  useEffect(() => {
    return () => { abortRef.current?.abort() }
  }, [])

  // ---- Navigation handlers ----

  /** Navigate into a folder (double-click / Enter key). */
  const navigateToFolder = useCallback((item: FileItem) => {
    setCurrentFolderId(item.id)
    setBreadcrumbs((prev) => [...prev, { id: item.id, name: item.name }])
    setSearchQuery('')
  }, [])

  /** Navigate to a breadcrumb node (click). */
  const navigateToBreadcrumb = useCallback((index: number) => {
    setBreadcrumbs((prev) => prev.slice(0, index + 1))
    const target = breadcrumbs[index]
    setCurrentFolderId(target.id)
    setSearchQuery('')
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [breadcrumbs])

  // ---- Search filter ----
  const filteredItems = useMemo(() => {
    if (!searchQuery.trim()) return items
    const q = searchQuery.toLowerCase()
    return items.filter((item) => item.name.toLowerCase().includes(q))
  }, [items, searchQuery])

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

  // ---- View mode toggle ----
  const handleViewModeToggle = useCallback(() => {
    setViewMode((prev) => (prev === 'list' ? 'grid' : 'list'))
  }, [])

  // ---- Upload wiring (Phase 7.3) ----
  const { uploadFile } = useUpload()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [isDragging, setIsDragging] = useState(false)

  /** Open the native file picker. */
  const handleUploadFile = useCallback(() => {
    fileInputRef.current?.click()
  }, [])

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

  /** Drag-and-drop handlers on the file explorer panel. */
  const handleDragOver = useCallback((e: DragEvent<HTMLDivElement>) => {
    e.preventDefault()
    if (e.dataTransfer.types.includes('Files')) setIsDragging(true)
  }, [])

  const handleDragLeave = useCallback((e: DragEvent<HTMLDivElement>) => {
    // Only clear when leaving the container itself, not a child element.
    if (e.currentTarget === e.target) setIsDragging(false)
  }, [])

  const handleDrop = useCallback(
    (e: DragEvent<HTMLDivElement>) => {
      e.preventDefault()
      setIsDragging(false)
      const files = e.dataTransfer.files
      if (files) {
        for (const file of Array.from(files)) {
          uploadFile(file, currentFolderId)
        }
      }
    },
    [uploadFile, currentFolderId],
  )

  // Refresh the listing when any upload completes.
  useEffect(() => {
    const handler = () => void fetchDirectory(currentFolderId)
    window.addEventListener(UPLOAD_COMPLETE_EVENT, handler)
    return () => window.removeEventListener(UPLOAD_COMPLETE_EVENT, handler)
  }, [UPLOAD_COMPLETE_EVENT, currentFolderId, fetchDirectory])

  return (
    <div className="flex h-screen overflow-hidden bg-zinc-950">
      {/* Sidebar */}
      <Sidebar
        collapsed={sidebarCollapsed}
        onToggleCollapse={() => setSidebarCollapsed((c) => !c)}
        onNewFolder={() => setFolderModalOpen(true)}
        onUploadFile={handleUploadFile}
        onSignOut={logout}
        storageUsed={STORAGE_USED}
        storageLimit={STORAGE_LIMIT}
      />

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

      {/* Main content area (drag-and-drop target) */}
      <div
        className="relative flex flex-1 flex-col min-w-0"
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        onDrop={handleDrop}
      >
        {/* Top navbar */}
        <Navbar
          breadcrumbs={breadcrumbs}
          onBreadcrumbNavigate={navigateToBreadcrumb}
          searchQuery={searchQuery}
          onSearchChange={setSearchQuery}
          viewMode={viewMode}
          onViewModeToggle={handleViewModeToggle}
        />

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
        {isLoading ? (
          <DirectorySkeleton />
        ) : viewMode === 'list' ? (
          <ListView items={filteredItems} onOpenFolder={navigateToFolder} />
        ) : (
          <GridView items={filteredItems} onOpenFolder={navigateToFolder} />
        )}

        {/* Drag overlay */}
        {isDragging && (
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

      {/* New folder modal */}
      <NewFolderModal
        open={folderModalOpen}
        onClose={() => setFolderModalOpen(false)}
        parentId={currentFolderId}
        onCreated={handleFolderCreated}
      />

      {/* Floating upload queue overlay */}
      <UploadQueue />
    </div>
  )
}

export default Dashboard
