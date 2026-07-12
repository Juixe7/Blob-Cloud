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
    <nav aria-label="Directory breadcrumbs" className="flex items-center gap-1 overflow-x-auto text-sm whitespace-nowrap">
      {nodes.map((node, i) => {
        const isLast = i === nodes.length - 1
        return (
          <span key={`${node.id}-${i}`} className="flex items-center gap-1">
            {i > 0 && (
              <svg
                width="14"
                height="14"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
                className="text-zinc-600"
                aria-hidden="true"
              >
                <polyline points="9,6 15,12 9,18" />
              </svg>
            )}
            {isLast ? (
              <span className="font-medium text-zinc-50">{node.name}</span>
            ) : (
              <button
                onClick={() => onNavigate(i)}
                className="rounded px-1 py-0.5 text-zinc-400 transition-colors hover:bg-zinc-900 hover:text-zinc-200"
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
