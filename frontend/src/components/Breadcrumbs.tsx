import type { BreadcrumbNode } from '../types/file'

interface BreadcrumbsProps {
  nodes: BreadcrumbNode[]
  /** Called when a non-last breadcrumb is clicked. Index identifies which node. */
  onNavigate: (index: number) => void
}

/**
 * Clickable breadcrumb chain separated by chevrons.
 * The last node represents the current location and is non-clickable.
 */
export function Breadcrumbs({ nodes, onNavigate }: BreadcrumbsProps) {
  return (
    <nav aria-label="Directory breadcrumbs" className="flex items-center gap-1.5 overflow-x-auto text-xs whitespace-nowrap select-none font-mono">
      {nodes.map((node, i) => {
        const isLast = i === nodes.length - 1
        return (
          <span key={`${node.id}-${i}`} className="flex items-center gap-1.5">
            {i > 0 && (
              <span className="text-zinc-600 font-bold text-[11px] select-none">/</span>
            )}
            {isLast ? (
              <span className="font-semibold text-amber-400 bg-amber-500/10 border border-amber-500/20 px-2 py-0.5 rounded-xs">{node.name}</span>
            ) : (
              <button
                onClick={() => onNavigate(i)}
                className="rounded px-1.5 py-0.5 text-zinc-400 font-medium transition-colors hover:bg-arch-850 hover:text-zinc-100"
                aria-label={`Navigate to ${node.name}`}
              >
                {node.name}
              </button>
            )}
          </span>
        )
      })}
    </nav>
  )
}

export default Breadcrumbs
