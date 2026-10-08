import { useState, useRef, useEffect } from 'react'
import { createPortal } from 'react-dom'
import { cn, formatDate } from '../lib/format'

interface ContactCardPopoverProps {
  email: string
  role?: string
  sharedAt?: string
  isOwner?: boolean
  onFilterByEmail?: (email: string) => void
  showEmailText?: boolean
  avatarSize?: 'sm' | 'md' | 'lg'
  className?: string
}

function getAvatarColor(email: string) {
  const colors = [
    { bg: 'bg-blue-950/90', text: 'text-blue-300', border: 'border-blue-700/60' },
    { bg: 'bg-emerald-950/90', text: 'text-emerald-300', border: 'border-emerald-700/60' },
    { bg: 'bg-sky-950/90', text: 'text-sky-300', border: 'border-sky-700/60' },
    { bg: 'bg-slate-800', text: 'text-slate-200', border: 'border-slate-600' },
    { bg: 'bg-teal-950/90', text: 'text-teal-300', border: 'border-teal-700/60' },
    { bg: 'bg-cyan-950/90', text: 'text-cyan-300', border: 'border-cyan-700/60' },
    { bg: 'bg-zinc-800', text: 'text-zinc-200', border: 'border-zinc-700' },
  ]
  let hash = 0
  for (let i = 0; i < email.length; i++) {
    hash = (hash << 5) - hash + email.charCodeAt(i)
    hash |= 0
  }
  return colors[Math.abs(hash) % colors.length]
}

export function ContactCardPopover({
  email,
  role,
  sharedAt,
  isOwner = false,
  onFilterByEmail,
  showEmailText = false,
  avatarSize = 'sm',
  className,
}: ContactCardPopoverProps) {
  const [isOpen, setIsOpen] = useState(false)
  const [coords, setCoords] = useState<{ top: number; left: number } | null>(null)
  const [copied, setCopied] = useState(false)
  
  const triggerRef = useRef<HTMLButtonElement>(null)
  const popoverRef = useRef<HTMLDivElement>(null)
  const openTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const closeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const initial = (email?.charAt(0) || '?').toUpperCase()
  const color = getAvatarColor(email || '')

  const updatePosition = () => {
    if (!triggerRef.current) return
    const rect = triggerRef.current.getBoundingClientRect()
    const popoverWidth = 280
    const popoverHeight = 190
    const padding = 12

    // Check vertical space
    const spaceBelow = window.innerHeight - rect.bottom
    const showAbove = spaceBelow < popoverHeight + padding && rect.top > popoverHeight + padding

    const top = showAbove ? rect.top - popoverHeight - 8 : rect.bottom + 8

    // Center horizontally on the avatar, clamp inside viewport
    let left = rect.left + rect.width / 2 - popoverWidth / 2
    if (left + popoverWidth > window.innerWidth - padding) {
      left = window.innerWidth - popoverWidth - padding
    }
    if (left < padding) {
      left = padding
    }

    setCoords({ top, left })
  }

  const handleMouseEnter = () => {
    if (closeTimerRef.current) {
      clearTimeout(closeTimerRef.current)
      closeTimerRef.current = null
    }
    openTimerRef.current = setTimeout(() => {
      updatePosition()
      setIsOpen(true)
    }, 120)
  }

  const handleMouseLeave = () => {
    if (openTimerRef.current) {
      clearTimeout(openTimerRef.current)
      openTimerRef.current = null
    }
    closeTimerRef.current = setTimeout(() => {
      setIsOpen(false)
    }, 220)
  }

  const handlePopoverMouseEnter = () => {
    if (closeTimerRef.current) {
      clearTimeout(closeTimerRef.current)
      closeTimerRef.current = null
    }
  }

  const handlePopoverMouseLeave = () => {
    closeTimerRef.current = setTimeout(() => {
      setIsOpen(false)
    }, 220)
  }

  const handleClick = (e: React.MouseEvent) => {
    e.stopPropagation()
    if (openTimerRef.current) clearTimeout(openTimerRef.current)
    if (closeTimerRef.current) clearTimeout(closeTimerRef.current)

    if (isOpen) {
      setIsOpen(false)
    } else {
      updatePosition()
      setIsOpen(true)
    }
  }

  useEffect(() => {
    if (!isOpen) return

    function handlePointerDown(e: PointerEvent) {
      const target = e.target as Node
      if (
        popoverRef.current &&
        !popoverRef.current.contains(target) &&
        triggerRef.current &&
        !triggerRef.current.contains(target)
      ) {
        setIsOpen(false)
      }
    }

    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        setIsOpen(false)
      }
    }

    function handleScrollOrResize() {
      setIsOpen(false)
    }

    document.addEventListener('pointerdown', handlePointerDown)
    document.addEventListener('keydown', handleKeyDown)
    window.addEventListener('scroll', handleScrollOrResize, true)
    window.addEventListener('resize', handleScrollOrResize)

    return () => {
      document.removeEventListener('pointerdown', handlePointerDown)
      document.removeEventListener('keydown', handleKeyDown)
      window.removeEventListener('scroll', handleScrollOrResize, true)
      window.removeEventListener('resize', handleScrollOrResize)
    }
  }, [isOpen])

  useEffect(() => {
    return () => {
      if (openTimerRef.current) clearTimeout(openTimerRef.current)
      if (closeTimerRef.current) clearTimeout(closeTimerRef.current)
    }
  }, [])

  const handleCopy = async (e: React.MouseEvent) => {
    e.stopPropagation()
    try {
      await navigator.clipboard.writeText(email)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      // fallback
    }
  }

  const sizeClasses = {
    sm: 'w-6 h-6 text-[10px]',
    md: 'w-7 h-7 text-xs',
    lg: 'w-9 h-9 text-sm',
  }

  return (
    <div className={cn('relative inline-flex items-center', className)}>
      {/* Trigger Button with Hover & Click handlers */}
      <button
        ref={triggerRef}
        type="button"
        onClick={handleClick}
        onMouseEnter={handleMouseEnter}
        onMouseLeave={handleMouseLeave}
        className="group/avatar inline-flex items-center gap-2 focus:outline-none focus-visible:ring-2 focus-visible:ring-zinc-500 rounded-full cursor-pointer select-none"
        aria-label={`Shared by ${email}. View contact details`}
        aria-expanded={isOpen}
      >
        <span
          className={cn(
            'inline-flex items-center justify-center font-bold rounded-full border transition-all duration-150 group-hover/avatar:scale-110 group-hover/avatar:ring-2 group-hover/avatar:ring-zinc-600/60 shadow-xs',
            sizeClasses[avatarSize],
            color.bg,
            color.text,
            color.border
          )}
        >
          {initial}
        </span>
        {showEmailText && (
          <span className="text-xs text-zinc-300 truncate max-w-[150px] group-hover/avatar:text-white transition-colors">
            {email}
          </span>
        )}
      </button>

      {/* Floating Popover rendered outside scroll/virtualization clipping bounds */}
      {isOpen && coords && createPortal(
        <div
          ref={popoverRef}
          onMouseEnter={handlePopoverMouseEnter}
          onMouseLeave={handlePopoverMouseLeave}
          onClick={(e) => e.stopPropagation()}
          style={{
            position: 'fixed',
            top: `${coords.top}px`,
            left: `${coords.left}px`,
            width: '280px',
            zIndex: 99999,
          }}
          className="rounded-xl bg-zinc-900/98 backdrop-blur-md border border-zinc-700/90 p-4 shadow-2xl animate-in fade-in zoom-in-95 duration-150 select-text"
        >
          {/* Header Info */}
          <div className="flex items-start gap-3">
            <div
              className={cn(
                'w-10 h-10 shrink-0 inline-flex items-center justify-center font-bold text-base rounded-full border shadow-inner',
                color.bg,
                color.text,
                color.border
              )}
            >
              {initial}
            </div>
            <div className="flex-1 min-w-0">
              <div className="flex items-center gap-1.5 flex-wrap">
                <span className="font-semibold text-xs text-zinc-100 truncate" title={email}>
                  {email}
                </span>
              </div>
              <div className="mt-1 flex items-center gap-1.5 flex-wrap">
                {isOwner ? (
                  <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-semibold bg-zinc-800 text-zinc-200 border border-zinc-700">
                    Owner
                  </span>
                ) : role ? (
                  <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-semibold bg-zinc-800 text-zinc-300 border border-zinc-700">
                    {role.charAt(0).toUpperCase() + role.slice(1).toLowerCase()}
                  </span>
                ) : null}
                {sharedAt && (
                  <span className="text-[10px] text-zinc-400 font-mono">
                    Shared {formatDate(sharedAt)}
                  </span>
                )}
              </div>
            </div>
          </div>

          <div className="my-3 h-px bg-zinc-800" />

          {/* Action Buttons */}
          <div className="flex flex-col gap-1">
            <a
              href={`mailto:${email}`}
              className="flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg text-xs font-medium text-zinc-300 hover:text-white hover:bg-zinc-800/80 transition-colors"
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="text-zinc-400">
                <rect x="2" y="4" width="20" height="16" rx="2" />
                <path d="m22 7-8.97 5.7a1.94 1.94 0 0 1-2.06 0L2 7" />
              </svg>
              Send email
            </a>

            <button
              type="button"
              onClick={handleCopy}
              className="flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg text-xs font-medium text-zinc-300 hover:text-white hover:bg-zinc-800/80 transition-colors text-left cursor-pointer"
            >
              {copied ? (
                <>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" className="text-emerald-400">
                    <polyline points="20 6 9 17 4 12" />
                  </svg>
                  <span className="text-emerald-400 font-semibold">Copied to clipboard</span>
                </>
              ) : (
                <>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="text-zinc-400">
                    <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
                    <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
                  </svg>
                  Copy email address
                </>
              )}
            </button>

            {onFilterByEmail && (
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation()
                  onFilterByEmail(email)
                  setIsOpen(false)
                }}
                className="flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg text-xs font-medium text-sky-400 hover:text-sky-300 hover:bg-sky-500/10 transition-colors text-left cursor-pointer"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="text-sky-400">
                  <polygon points="22 3 2 3 10 12.46 10 19 14 21 14 12.46 22 3" />
                </svg>
                Filter files by this person
              </button>
            )}
          </div>
        </div>,
        document.body
      )}
    </div>
  )
}
