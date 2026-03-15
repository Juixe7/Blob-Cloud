/**
 * Shimmer skeleton that mimics the list view layout while directory contents
 * are being fetched. Prevents layout jank on navigation.
 */
export function DirectorySkeleton() {
  return (
    <div className="flex-1 overflow-auto" role="status" aria-label="Loading directory contents">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-zinc-800 text-left text-xs font-medium uppercase tracking-wider text-zinc-500">
            <th className="px-4 py-3 w-[60%]">Name</th>
            <th className="px-4 py-3 w-[25%]">Date Modified</th>
            <th className="px-4 py-3 w-[15%]">Size</th>
          </tr>
        </thead>
        <tbody>
          {Array.from({ length: 8 }).map((_, i) => (
            <tr key={i} className="border-b border-zinc-800/50">
              <td className="px-4 py-3">
                <div className="flex items-center gap-3">
                  <SkeletonBox className="h-[18px] w-[18px] rounded" />
                  <SkeletonBox className="h-3.5 w-40 rounded" />
                </div>
              </td>
              <td className="px-4 py-3">
                <SkeletonBox className="h-3.5 w-28 rounded" />
              </td>
              <td className="px-4 py-3">
                <SkeletonBox className="h-3.5 w-16 rounded" />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <span className="sr-only">Loading…</span>
    </div>
  )
}

/** A single shimmer rectangle. */
function SkeletonBox({ className = '' }: { className?: string }) {
  return (
    <div
      className={`animate-pulse rounded bg-zinc-900 ${className}`}
    />
  )
}

export default DirectorySkeleton
