import axios, {
  type AxiosError,
  type AxiosRequestConfig,
  type InternalAxiosRequestConfig,
} from 'axios'
import {
  clearTokens,
  dispatchUnauth,
  getAccessToken,
  getRefreshToken,
  setTokens,
  withRefreshLock,
} from './token'

/**
 * Configured Axios instance for all /api calls.
 *
 * - Every outgoing request is stamped with `Authorization: Bearer <token>`.
 * - On a 401, the response interceptor transparently refreshes the session via
 *   /auth/refresh and retries the original request exactly once. Concurrent
 *   401s collapse into a single refresh (see token.ts#withRefreshLock).
 * - If the refresh itself fails (or there's no refresh token), we wipe local
 *   state and broadcast UNAUTH_EVENT so AuthContext can redirect to /login.
 */
export const apiClient = axios.create({
  baseURL: import.meta.env.VITE_API_BASE ?? '/api',
  headers: { 'Content-Type': 'application/json' },
})

/* Mark requests that are already a retry, so we don't loop forever. */
const RETRY_FLAG = '_blobcloudRetried'

/** Auth endpoints whose 401 must NOT trigger a refresh attempt. */
const NO_REFRESH_PATHS = ['/auth/login', '/auth/register', '/auth/refresh']

/* ------------------------- Request interceptor ------------------------- */
apiClient.interceptors.request.use((config: InternalAxiosRequestConfig) => {
  const token = getAccessToken()
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

/* ------------------------ Response interceptor ------------------------- */
apiClient.interceptors.response.use(
  (response) => response,
  async (error: AxiosError) => {
    const original = error.config as (InternalAxiosRequestConfig & { [k: string]: unknown }) | undefined
    const status = error.response?.status

    // Only a 401 on a "real" API call (and not already retried) is recoverable.
    if (status !== 401 || !original || original[RETRY_FLAG]) {
      return Promise.reject(error)
    }

    // Don't try to refresh when the failing call is itself an auth endpoint —
    // a 401 there means the credentials/tokens are simply wrong.
    const url = (original.url ?? '') + ''
    if (NO_REFRESH_PATHS.some((p) => url.includes(p))) {
      return Promise.reject(error)
    }

    const refreshToken = getRefreshToken()
    if (!refreshToken) {
      // Nothing to refresh with — end the session.
      clearTokens()
      dispatchUnauth()
      return Promise.reject(error)
    }

    original[RETRY_FLAG] = true

    try {
      // Collapse concurrent 401s into a single refresh round-trip.
      const newToken = await withRefreshLock(async () => {
        // Use a bare axios call so the response interceptor doesn't recurse.
        const res = await axios.post<{ token: string }>(
          `${apiClient.defaults.baseURL}/auth/refresh`,
          { refresh_token: refreshToken },
          { headers: { 'Content-Type': 'application/json' } },
        )
        const next = res.data.token
        setTokens(next)
        return next
      })

      original.headers.Authorization = `Bearer ${newToken}`
      return apiClient.request(original as AxiosRequestConfig)
    } catch (refreshError) {
      clearTokens()
      dispatchUnauth()
      return Promise.reject(refreshError)
    }
  },
)

export default apiClient
