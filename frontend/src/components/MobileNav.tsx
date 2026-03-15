import { cn } from '../lib/format'

interface MobileNavProps {
  activeNav: string
  onSelectNav: (navId: string) => void
  onOpenSettings: () => void
  onUploadFile: () => void
  disableNew?: boolean
}

export function MobileNav({
  activeNav,
  onSelectNav,
  onOpenSettings,
  onUploadFile,
  disableNew,
}: MobileNavProps) {
  const items = [
    { id: 'drive', label: 'Drive', icon: <path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z" /> },
    { id: 'shared', label: 'Shared', icon: <><path d="M16 21v-2a4 4 0 00-4-4H6a4 4 0 00-4 4v2" /><circle cx="9" cy="7" r="4" /><path d="M22 21v-2a4 4 0 00-3-3.87" /><path d="M16 3.13a4 4 0 010 7.75" /></> },
    { id: 'trash', label: 'Trash', icon: <><path d="M3 6h18" /><path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2" /><line x1="10" y1="11" x2="10" y2="17" /><line x1="14" y1="11" x2="14" y2="17" /></> },
    { id: 'settings', label: 'Settings', icon: <><circle cx="12" cy="12" r="3" /><path d="M12 1v2m0 18v2M4.22 4.22l1.42 1.42m12.72 12.72l1.42 1.42M1 12h2m18 0h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42" /></> },
  ]

  return (
    <>
      {/* Floating Action Button for Upload */}
      {!disableNew && (
        <button
          onClick={onUploadFile}
          className="md:hidden fixed bottom-[72px] right-4 z-[60] flex h-14 w-14 items-center justify-center rounded-2xl bg-amber-500 text-arch-950 shadow-xl transition-transform hover:scale-105 focus:outline-none focus:ring-4 focus:ring-amber-500/30"
          aria-label="Upload"
        >
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" aria-hidden="true">
            <path d="M12 5v14M5 12h14" />
          </svg>
        </button>
      )}

      {/* Bottom Navigation Bar */}
      <nav className="md:hidden fixed bottom-0 left-0 right-0 z-[60] flex items-center justify-around border-t border-arch-border bg-arch-950/95 pb-safe pt-2 pb-2 backdrop-blur-md select-none">
        {items.map((item) => {
          const isActive = activeNav === item.id || (item.id === 'settings' && activeNav === 'settings')
          return (
            <button
              key={item.id}
              onClick={() => {
                if (item.id === 'settings') onOpenSettings()
                else onSelectNav(item.id)
              }}
              className="flex flex-col items-center justify-center w-16 h-12 gap-1 text-zinc-500 transition-colors hover:text-zinc-300"
            >
              <div className={cn(
                'flex items-center justify-center h-8 w-14 rounded-full transition-colors',
                isActive ? 'bg-amber-500/20 text-amber-400' : ''
              )}>
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  {item.icon}
                </svg>
              </div>
              <span className={cn('text-[10px] font-medium', isActive ? 'text-amber-400' : '')}>
                {item.label}
              </span>
            </button>
          )
        })}
      </nav>
    </>
  )
}
