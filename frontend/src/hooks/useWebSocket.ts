import { useEffect, useRef, useState } from 'react'
import type {
  WSMessage,
  WebSocketStatus,
} from '../types/sync'
import { useToast } from '../components/Toast'

/* ----------------------------- tuning constants ----------------------------- */

/** Initial backoff delay after a socket drop, in ms. Doubles on each failure. */
const INITIAL_BACKOFF_MS = 1_000
/** Ceiling for the backoff delay so an offline tab doesn't spin too fast. */
const MAX_BACKOFF_MS = 30_000
/** Client-side heartbeat interval. Pings keep intermediary proxies alive and
 *  let us detect a half-open socket faster than the OS TCP timeout. */
const HEARTBEAT_INTERVAL_MS = 25_000
/** If no message (or pong) arrives within this window, assume the link is dead
 *  and proactively reconnect. */
const HEARTBEAT_TIMEOUT_MS = 60_000

/** Options accepted by the hook. */
interface UseWebSocketOptions {
  /** Full ws:// or wss:// URL to connect to, including the token query param. */
  url: string | null
  /** Called for every valid message envelope received. */
  onMessage?: (msg: WSMessage) => void
  /** If false, the hook stays dormant (no connection attempt). */
  enabled?: boolean
}

/**
 * Phase 7.5 — resilient WebSocket client hook.
 *
 * Responsibilities:
 *  - Open a socket to the Go backend's /api/ws endpoint when a token is present.
 *  - Expose a reactive `status` (CONNECTING / CONNECTED / DISCONNECTED /
 *    RECONNECTING) for the sidebar indicator.
 *  - Reconnect with exponential backoff (1s → 2s → … → 30s) after drops,
 *    resetting the delay once a connection succeeds.
 *  - Run a client-side heartbeat to detect half-open connections and keep
 *    cloud load balancers from idle-closing the socket.
 *  - Tear down cleanly on unmount/logout: send a 1001 Going Away close frame
 *    so the Go server's Hub doesn't leak the client.
 *
 * The hook holds all mutable connection state in refs so the React render
 * cycle never fights the socket lifecycle; only `status` is surfaced as state.
 */
export function useWebSocket({ url, onMessage, enabled = true }: UseWebSocketOptions) {
  const [status, setStatus] = useState<WebSocketStatus>('DISCONNECTED')
  const { push: pushToast } = useToast()

  // --- refs: owned by the effect, stable across renders ---
  const socketRef = useRef<WebSocket | null>(null)
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const heartbeatTimerRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const heartbeatTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const backoffRef = useRef(INITIAL_BACKOFF_MS)
  const closedByUsRef = useRef(false)
  const onMessageRef = useRef(onMessage)

  // Keep the latest onMessage without resubscribing the socket.
  useEffect(() => {
    onMessageRef.current = onMessage
  }, [onMessage])

  useEffect(() => {
    if (!enabled || !url) {
      setStatus('DISCONNECTED')
      return
    }

    // Local copies so the cleanup closure captures the right values even if a
    // reconnect has already swapped the socket.
    const endpoint: string = url // narrowed from string | null by the guard above
    let socket: WebSocket | null = null
    let disposed = false

    /** Clear any pending reconnect timer. */
    const clearReconnectTimer = () => {
      if (reconnectTimerRef.current) {
        clearTimeout(reconnectTimerRef.current)
        reconnectTimerRef.current = null
      }
    }

    /** Tear down heartbeat timers. */
    const clearHeartbeat = () => {
      if (heartbeatTimerRef.current) {
        clearInterval(heartbeatTimerRef.current)
        heartbeatTimerRef.current = null
      }
      if (heartbeatTimeoutRef.current) {
        clearTimeout(heartbeatTimeoutRef.current)
        heartbeatTimeoutRef.current = null
      }
    }

    /** Schedule a reconnect with exponential backoff (capped at MAX_BACKOFF_MS). */
    const scheduleReconnect = () => {
      if (disposed || closedByUsRef.current) return
      setStatus('RECONNECTING')
      const delay = backoffRef.current
      backoffRef.current = Math.min(backoffRef.current * 2, MAX_BACKOFF_MS)
      clearReconnectTimer()
      reconnectTimerRef.current = setTimeout(() => {
        if (!disposed) connect()
      }, delay)
    }

    /** (Re)arm the heartbeat watchdog. Called on every incoming frame. */
    const armHeartbeatWatchdog = () => {
      if (heartbeatTimeoutRef.current) clearTimeout(heartbeatTimeoutRef.current)
      heartbeatTimeoutRef.current = setTimeout(() => {
        // No traffic within the window — the link is likely half-open.
        // Force-close; the onclose handler will trigger a reconnect.
        if (socket && socket.readyState === WebSocket.OPEN) {
          socket.close(4001, 'heartbeat timeout')
        }
      }, HEARTBEAT_TIMEOUT_MS)
    }

    /** Start the periodic ping interval. */
    const startHeartbeat = () => {
      clearHeartbeat()
      heartbeatTimerRef.current = setInterval(() => {
        if (socket && socket.readyState === WebSocket.OPEN) {
          // Browsers don't expose a raw WS ping; sending a small app-level
          // ping frame works with the Go read pump which discards text.
          // Wrapped in try/catch because send() throws if the socket just
          // transitioned to CLOSING between our check and the call.
          try {
            socket.send(JSON.stringify({ type: 'ping' }))
          } catch {
            /* will be caught by the watchdog / onclose */
          }
        }
        armHeartbeatWatchdog()
      }, HEARTBEAT_INTERVAL_MS)
      armHeartbeatWatchdog()
    }

    /** Open the socket and wire its event handlers. */
    function connect() {
      if (disposed) return
      closedByUsRef.current = false
      setStatus('CONNECTING')

      // Guard: browsers throw synchronously on a malformed URL.
      try {
        socket = new WebSocket(endpoint)
      } catch {
        scheduleReconnect()
        return
      }
      socketRef.current = socket

      socket.onopen = () => {
        if (disposed) return
        // A successful connection resets the backoff window.
        backoffRef.current = INITIAL_BACKOFF_MS
        setStatus('CONNECTED')
        startHeartbeat()
      }

      socket.onmessage = (event) => {
        armHeartbeatWatchdog()
        // Parse the envelope; ignore anything malformed (the Go backend sends
        // a {type, payload} shape, but we never want a bad frame to crash the UI).
        try {
          const msg = JSON.parse(event.data) as WSMessage
          if (msg && typeof msg.type === 'string') {
            if (msg.type === 'FORCE_LOGOUT') {
              localStorage.removeItem('access_token')
              localStorage.removeItem('refresh_token')
              pushToast({ message: 'Session revoked by administrator', variant: 'error' })
              window.location.href = '/login'
              return
            }
            onMessageRef.current?.(msg)
          }
        } catch {
          /* swallow non-JSON frames (e.g. the server's own pong echoes) */
        }
      }

      socket.onerror = () => {
        // The browser fires onerror before onclose for most failures; we let
        // onclose drive the reconnect so we don't double-schedule.
      }

      socket.onclose = (event) => {
        clearHeartbeat()
        socket = null
        socketRef.current = null
        if (disposed || closedByUsRef.current) {
          setStatus('DISCONNECTED')
          return
        }
        // An abnormal close (1006) or our heartbeat-driven 4001 triggers a retry.
        // Normal 1000 closures while still enabled also retry (server restart).
        scheduleReconnect()
        // eslint-disable-next-line no-console
        console.info('[ws] closed', { code: event.code, reason: event.reason })
      }
    }

    connect()

    // --- cleanup: runs on unmount, logout (url → null), or HMR ---
    return () => {
      disposed = true
      clearReconnectTimer()
      clearHeartbeat()
      closedByUsRef.current = true
      if (socket) {
        // Send a clean frame so the Go Hub unregisters us promptly.
        if (socket.readyState === WebSocket.OPEN) {
          socket.close(1000, 'client disconnect')
        }
        socket.onopen = null
        socket.onmessage = null
        socket.onerror = null
        socket.onclose = null
        socket = null
      }
      socketRef.current = null
      setStatus('DISCONNECTED')
    }
  }, [url, enabled])

  return { status }
}
