import { useState, useEffect } from 'react'
import type { RefObject } from 'react'

export function useResizeObserver(ref: RefObject<HTMLElement | null>) {
  const [dimensions, setDimensions] = useState({ width: 0, height: 0 })

  useEffect(() => {
    const element = ref.current
    if (!element) return

    const observer = new ResizeObserver((entries) => {
      if (entries.length === 0) return
      const entry = entries[0]
      setDimensions({
        width: entry.contentRect.width,
        height: entry.contentRect.height,
      })
    })

    observer.observe(element)
    
    // Initial dimensions
    setDimensions({
      width: element.getBoundingClientRect().width,
      height: element.getBoundingClientRect().height,
    })

    return () => {
      observer.disconnect()
    }
  }, [ref])

  return dimensions
}
