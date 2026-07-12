// Token storage + JWT decoding + the refresh-dedup primitive shared with the
// Axios interceptor. Keeping these as standalone functions (rather than React
// state) means the interceptor can read/write tokens outside the React tree.

const ACCESS_KEY = 'blobcloud:token'
const REFRESH_KEY = 'blobcloud:refresh'

/** Event dispatched on window when the session is invalidated by a failed
 *  refresh. AuthContext listens for it and clears its state. */
export const UNAUTH_EVENT = 'blobcloud:unauth'

/** Decoded JWT payload shape. We only care about identity + expiry. */
export interface JwtUser {
  user_id: string
  /** Expiry, in seconds since epoch. */
  exp?: number
  iat?: number
}

export function getAccessToken(): string | null {
  try {
    return localStorage.getItem(ACCESS_KEY)
  } catch {
    return null
  }
}

export function getRefreshToken(): string | null {
  try {
    return localStorage.getItem(REFRESH_KEY)
  } catch {
    return null
  }
}

/** Persist both tokens. Pass null to clear an individual token. */
export function setTokens(access: string | null, refresh?: string | null): void {
  try {
    if (access) localStorage.setItem(ACCESS_KEY, access)
    else localStorage.removeItem(ACCESS_KEY)
    if (refresh !== undefined) {
      if (refresh) localStorage.setItem(REFRESH_KEY, refresh)
      else localStorage.removeItem(REFRESH_KEY)
    }
  } catch {
    /* storage may be unavailable (private mode) — fail quietly */
  }
}

export function clearTokens(): void {
  try {
    localStorage.removeItem(ACCESS_KEY)
    localStorage.removeItem(REFRESH_KEY)
  } catch {
    /* noop */
  }
}

/**
 * Decode a JWT's payload without verifying the signature (verification happens
 * server-side). Returns null on malformed/expired tokens. We never trust this
 * payload for authorization — only for UX decisions (e.g. "is it worth trying
 * a refresh before mount").
 */
export function decodeUser(token: string | null): JwtUser | null {
  if (!token) return null
  const parts = token.split('.')
  if (parts.length !== 3) return null

  try {
    // JWT uses base64url: convert to base64, then UTF-8 decode the JSON.
    const b64 = parts[1].replace(/-/g, '+').replace(/_/g, '/')
    const json = decodeURIComponent(
      atob(b64)
        .split('')
        .map((c) => '%' + ('00' + c.charCodeAt(0).toString(16)).slice(-2))
        .join(''),
    )
    const payload = JSON.parse(json) as JwtUser
    if (payload.exp && Date.now() >= payload.exp * 1000) return null
    return payload
  } catch {
    return null
  }
}

/** True if a token exists and has not passed its `exp`. */
export function isTokenValid(token: string | null): boolean {
  return decodeUser(token) !== null
}

/* ------------------------------------------------------------------ *
 * Refresh de-duplication
 *
 * When multiple API calls fail with 401 at once (common on page load or after
 * a tab regains focus), we must only fire ONE /auth/refresh request. Other
 * callers queue on the in-flight promise and resolve together.
 * ------------------------------------------------------------------ */

let refreshPromise: Promise<string> | null = null

/**
 * Run `fn` exactly once concurrently. Concurrent callers receive the same
 * promise. `fn` should resolve with the new access token or reject.
 */
export function withRefreshLock(fn: () => Promise<string>): Promise<string> {
  if (refreshPromise) return refreshPromise
  refreshPromise = fn().finally(() => {
    refreshPromise = null
  })
  return refreshPromise
}

/** Signal total session invalidation so listeners (AuthContext) can react. */
export function dispatchUnauth(): void {
  window.dispatchEvent(new CustomEvent(UNAUTH_EVENT))
}
