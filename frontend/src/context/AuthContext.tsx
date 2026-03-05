import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import axios from 'axios'
import { apiClient } from '../lib/api'
import {
  clearTokens,
  decodeUser,
  dispatchUnauth,
  getAccessToken,
  getRefreshToken,
  isTokenValid,
  setTokens,
  UNAUTH_EVENT,
  type JwtUser,
} from '../lib/token'

/** Shape returned by /auth/login and /auth/register. */
interface AuthResponse {
  token: string
  refresh_token: string
}

export interface AuthResult {
  ok: boolean
  /** Human-readable error string for the form, or null on success. */
  error: string | null
  verificationRequired?: boolean
  userId?: string
  email?: string
}

export interface AuthContextValue {
  user: JwtUser | null
  token: string | null
  refreshToken: string | null
  isAuthenticated: boolean
  /** True during initial bootstrap — guards against route-guard flicker. */
  isLoading: boolean
  login: (email: string, password: string) => Promise<AuthResult>
  register: (email: string, password: string) => Promise<AuthResult>
  loginWithTokens: (token: string, refreshToken: string) => void
  logout: () => void
}

const AuthContext = createContext<AuthContextValue | undefined>(undefined)

/** Normalize an Axios error into a single readable string. */
export function extractError(err: unknown, fallback: string): string {
  if (axios.isAxiosError(err)) {
    const data = err.response?.data as { error?: string; message?: string } | undefined
    return data?.error || data?.message || err.message || fallback
  }
  if (err instanceof Error) return err.message
  return fallback
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string | null>(() => getAccessToken())
  const [refreshToken, setRefreshToken] = useState<string | null>(() => getRefreshToken())
  const [user, setUser] = useState<JwtUser | null>(() => {
    const access = getAccessToken()
    return isTokenValid(access) ? decodeUser(access) : null
  })
  const [isLoading, setIsLoading] = useState(() => {
    const access = getAccessToken()
    const refresh = getRefreshToken()
    if (isTokenValid(access)) {
      return false
    }
    if (access && refresh) {
      return true
    }
    return false
  })
  const didBootstrap = useRef(false)

  const isAuthenticated = token !== null && user !== null

  /** Persist tokens + hydrate user from the access token's claims. */
  const applyTokens = useCallback((access: string, refresh: string) => {
    setTokens(access, refresh)
    setToken(access)
    setRefreshToken(refresh)
    setUser(decodeUser(access))
  }, [])

  const loginWithTokens = useCallback(
    (access: string, refresh: string) => {
      applyTokens(access, refresh)
    },
    [applyTokens],
  )

  const wipeSession = useCallback(() => {
    clearTokens()
    setToken(null)
    setRefreshToken(null)
    setUser(null)
  }, [])

  /**
   * Validate the stored session on mount. If the access token is still valid we
   * hydrate immediately; otherwise we attempt a refresh so a returning user
   * with an expired (but refreshable) session doesn't bounce to /login.
   */
  const bootstrap = useCallback(async () => {
    const access = getAccessToken()
    const refresh = getRefreshToken()

    if (isTokenValid(access)) {
      setUser(decodeUser(access))
      setIsLoading(false)
      return
    }

    if (access && refresh) {
      try {
        const res = await axios.post<AuthResponse>('/api/auth/refresh', {
          refresh_token: refresh,
        })
        applyTokens(res.data.token, refresh)
      } catch {
        wipeSession()
      } finally {
        setIsLoading(false)
      }
      return
    }

    wipeSession()
    setIsLoading(false)
  }, [applyTokens, wipeSession])

  useEffect(() => {
    if (didBootstrap.current) return
    didBootstrap.current = true
    void bootstrap()
  }, [bootstrap])

  /** React to interceptor-driven session invalidation (failed refresh). */
  useEffect(() => {
    const handler = () => wipeSession()
    window.addEventListener(UNAUTH_EVENT, handler)
    return () => window.removeEventListener(UNAUTH_EVENT, handler)
  }, [wipeSession])

  const login = useCallback(
    async (email: string, password: string): Promise<AuthResult> => {
      try {
        const res = await apiClient.post<AuthResponse>('/auth/login', { email, password })
        applyTokens(res.data.token, res.data.refresh_token)
        return { ok: true, error: null }
      } catch (err) {
        if (axios.isAxiosError(err) && err.response) {
          const data = err.response.data as { error?: string; user_id?: string; email?: string }
          if (data?.error === 'verification_required' && data?.user_id) {
            return { ok: false, error: 'verification_required', verificationRequired: true, userId: data.user_id, email: data.email || email }
          }
        }
        return { ok: false, error: extractError(err, 'Invalid email or password.') }
      }
    },
    [applyTokens],
  )

  const register = useCallback(
    async (email: string, password: string): Promise<AuthResult> => {
      try {
        const res = await apiClient.post<{ status?: string; user_id?: string; email?: string; token?: string; refresh_token?: string }>(
          '/auth/register',
          { email, password },
        )
        if (res.data.status === 'verification_required' && res.data.user_id) {
          return { ok: false, error: null, verificationRequired: true, userId: res.data.user_id, email: res.data.email || email }
        }
        if (res.data.token && res.data.refresh_token) {
          applyTokens(res.data.token, res.data.refresh_token)
        }
        return { ok: true, error: null }
      } catch (err) {
        return { ok: false, error: extractError(err, 'Registration failed.') }
      }
    },
    [applyTokens],
  )

  const logout = useCallback(async () => {
    const refresh = getRefreshToken()
    if (refresh) {
      try {
        await apiClient.post('/auth/logout', { refresh_token: refresh })
      } catch {
        // Silently ignore errors during logout API request to ensure client session is always wiped
      }
    }
    wipeSession()
    dispatchUnauth() // ensure any open tabs/hooks also clear
  }, [wipeSession])


  const value = useMemo<AuthContextValue>(
    () => ({
      user,
      token,
      refreshToken,
      isAuthenticated,
      isLoading,
      login,
      register,
      loginWithTokens,
      logout,
    }),
    [user, token, refreshToken, isAuthenticated, isLoading, login, register, loginWithTokens, logout],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

// eslint-disable-next-line react-refresh/only-export-components
export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within an <AuthProvider>')
  return ctx
}
