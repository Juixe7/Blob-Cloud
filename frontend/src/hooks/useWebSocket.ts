import { useCallback, useEffect, useRef, useState } from 'react'
import type {
  WSMessage,
  WebSocketStatus,
} from '../types/sync'
import { useToast } from '../components/Toast'
import { clearTokens, dispatchUnauth, UNAUTH_EVENT } from '../lib/token'

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
/** Maximum consecutive handshake/connection failures before the circuit breaker trips. */
const MAX_CONSECUTIVE_FAILURES = 5

/** Application-level WebSocket Close Codes (RFC 6455 4000-4999 range) */
const WS_CLOSE_UNAUTHORIZED = 4401

/** Options accepted by the hook. */
export interface UseWebSocketOptions {
  /** Clean ws:// or wss:// URL to connect to (without query tokens). */
  url: string | null
  /** Active session JWT used for in-band authentication handshake. */
  token?: string | null
  /** Called for every valid message envelope received. */
  onMessage?: (msg: WSMessage) => void
  /** If false, the hook stays dormant (no connection attempt). */
  enabled?: boolean
}

export interface UseWebSocketResult {
  status: WebSocketStatus
  isCircuitBroken: boolean
  retry: () => void
}

/**
 * Phase 7.5 + Production Hardening — resilient WebSocket client hook.
 *
 * Responsibilities:
 *  - Open a socket to the Go backend's /api/ws endpoint.
 *  - Perform in-band first-message authentication handshake ({"type": "AUTH", "token": "..."})
 *    to prevent sensitive token leakage into URL query logs.
 *  - Halt retries immediately on deterministic RFC 4401 application close codes.
 *  - Circuit Breaker: Halt retries after 5 consecutive infrastructure failures.
 *  - Event Bus Integration: Cancel pending timers and abort connection on UNAUTH_EVENT.
 *  - Reconnect with exponential backoff (1s → 2s → … → 30s) after network drops.
 *  - Run a client-side heartbeat to detect half-open connections.
 *  - Cleanly abort sockets in both CONNECTING and OPEN states on unmount/redirect.
 */
export function useWebSocket({
  url,
  token,
  onMessage,
  enabled = true,
}: UseWebSocketOptions): UseWebSocketResult {
  const [status, setStatus] = useState<WebSocketStatus>('DISCONNECTED')
  const [isCircuitBroken, setIsCircuitBroken] = useState(false)
  const [retryTrigger, setRetryTrigger] = useState(0)
  const { push: pushToast } = useToast()

  // --- refs: owned by the effect, stable across renders ---
  const socketRef = useRef<WebSocket | null>(null)
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const heartbeatTimerRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const heartbeatTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const backoffRef = useRef(INITIAL_BACKOFF_MS)
  const consecutiveFailuresRef = useRef(0)
  const closedByUsRef = useRef(false)
  const onMessageRef = useRef(onMessage)

  // Keep the latest onMessage without resubscribing the socket.
  useEffect(() => {
    onMessageRef.current = onMessage
  }, [onMessage])

  // Clear reconnect timer helper
  const clearReconnectTimer = useCallback(() => {
    if (reconnectTimerRef.current) {
      clearTimeout(reconnectTimerRef.current)
      reconnectTimerRef.current = null
    }
  }, [])

  // Manual retry trigger to reset the circuit breaker
  const retry = useCallback(() => {
    consecutiveFailuresRef.current = 0
    setIsCircuitBroken(false)
    backoffRef.current = INITIAL_BACKOFF_MS
    clearReconnectTimer()
    setRetryTrigger((prev) => prev + 1)
  }, [clearReconnectTimer])

  // Reset circuit breaker when token changes (e.g. fresh login)
  useEffect(() => {
    if (token) {
      consecutiveFailuresRef.current = 0
      setIsCircuitBroken(false)
      backoffRef.current = INITIAL_BACKOFF_MS
    }
  }, [token])

  // Circuit breaker auto-recovery on network reconnect or tab focus
  useEffect(() => {
    const handleOnline = () => {
      if (consecutiveFailuresRef.current >= MAX_CONSECUTIVE_FAILURES) {
        retry()
      }
    }
    const handleVisibility = () => {
      if (document.visibilityState === 'visible' && consecutiveFailuresRef.current >= MAX_CONSECUTIVE_FAILURES) {
        retry()
      }
    }

    window.addEventListener('online', handleOnline)
    document.addEventListener('visibilitychange', handleVisibility)
    return () => {
      window.removeEventListener('online', handleOnline)
      document.removeEventListener('visibilitychange', handleVisibility)
    }
  }, [retry])

  // Immediate teardown on global session invalidation (blobcloud:unauth)
  useEffect(() => {
    const onUnauth = () => {
      closedByUsRef.current = true
      clearReconnectTimer()
      if (heartbeatTimerRef.current) {
        clearInterval(heartbeatTimerRef.current)
        heartbeatTimerRef.current = null
      }
      if (heartbeatTimeoutRef.current) {
        clearTimeout(heartbeatTimeoutRef.current)
        heartbeatTimeoutRef.current = null
      }
      if (socketRef.current && socketRef.current.readyState !== WebSocket.CLOSED) {
        socketRef.current.close(1000, 'unauthenticated')
      }
      socketRef.current = null
      setStatus('DISCONNECTED')
    }

    window.addEventListener(UNAUTH_EVENT, onUnauth)
    return () => window.removeEventListener(UNAUTH_EVENT, onUnauth)
  }, [clearReconnectTimer])

  useEffect(() => {
    // Unconditionally clear any pending timer before inspecting guards
    clearReconnectTimer()

    if (!enabled || !url || (token === null && !url.includes('token='))) {
      setStatus('DISCONNECTED')
      return
    }

    // Local copies for the closure
    const endpoint: string = url
    const authToken: string | undefined = token || undefined
    let socket: WebSocket | null = null
    let disposed = false

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

      try {
        socket = new WebSocket(endpoint)
      } catch {
        scheduleReconnect()
        return
      }
      socketRef.current = socket

      socket.onopen = () => {
        if (disposed) return

        // 1. Perform in-band auth handshake if token is available
        if (authToken) {
          try {
            socket?.send(JSON.stringify({ type: 'AUTH', token: authToken }))
          } catch {
            scheduleReconnect()
          }
          return
        }

        // 2. Fallback for legacy URL-token connections
        consecutiveFailuresRef.current = 0
        backoffRef.current = INITIAL_BACKOFF_MS
        setStatus('CONNECTED')
        startHeartbeat()
      }

      socket.onmessage = (event) => {
        armHeartbeatWatchdog()

        try {
          const msg = JSON.parse(event.data) as Record<string, unknown>
          if (msg && typeof msg.type === 'string') {
            // In-band Auth Handshake acknowledgement
            if (msg.type === 'AUTH_OK') {
              consecutiveFailuresRef.current = 0
              backoffRef.current = INITIAL_BACKOFF_MS
              setStatus('CONNECTED')
              startHeartbeat()
              return
            }

            if (msg.type === 'AUTH_ERROR') {
              console.warn('[ws] authentication rejected by server:', msg.error)
              return
            }

            if (msg.type === 'FORCE_LOGOUT') {
              clearTokens()
              dispatchUnauth()
              pushToast({ message: 'Session revoked by administrator', variant: 'error' })
              window.location.href = '/login'
              return
            }

            onMessageRef.current?.(msg as unknown as WSMessage)
          }
        } catch {
          /* swallow non-JSON frames */
        }
      }

      socket.onerror = () => {
        // onclose drives reconnect logic
      }

      socket.onclose = (event) => {
        clearHeartbeat()
        socket = null
        socketRef.current = null

        if (disposed || closedByUsRef.current) {
          setStatus('DISCONNECTED')
          return
        }

        // 1. Explicit Auth Failure (RFC 4401): halt retries immediately
        if (event.code === WS_CLOSE_UNAUTHORIZED) {
          console.warn('[ws] unauthorized (code 4401) — halting reconnects')
          clearReconnectTimer()
          clearTokens()
          dispatchUnauth()
          setStatus('DISCONNECTED')
          return
        }

        // 2. Infrastructure Failure: Check Circuit Breaker threshold
        consecutiveFailuresRef.current += 1
        if (consecutiveFailuresRef.current >= MAX_CONSECUTIVE_FAILURES) {
          console.warn(`[ws] circuit breaker tripped after ${consecutiveFailuresRef.current} failed attempts`)
          clearReconnectTimer()
          setIsCircuitBroken(true)
          setStatus('DISCONNECTED')
          return
        }

        // 3. Normal retry with exponential backoff
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
        // Cleanly close in either OPEN or CONNECTING states
        if (socket.readyState !== WebSocket.CLOSED) {
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
  }, [url, token, enabled, retryTrigger, clearReconnectTimer])

  return { status, isCircuitBroken, retry }
}

