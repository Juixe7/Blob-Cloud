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
  logout: () => void
}

const AuthContext = createContext<AuthContextValue | undefined>(undefined)

/** Normalize an Axios error into a single readable string. */
function extractError(err: unknown, fallback: string): string {
  if (axios.isAxiosError(err)) {
    const data = err.response?.data as { error?: string; message?: string } | undefined
    return data?.error || data?.message || err.message || fallback
  }
  if (err instanceof Error) return err.message
  return fallback
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<JwtUser | null>(null)
  const [token, setToken] = useState<string | null>(() => getAccessToken())
  const [refreshToken, setRefreshToken] = useState<string | null>(() => getRefreshToken())
  const [isLoading, setIsLoading] = useState(true)
  const didBootstrap = useRef(false)

  const isAuthenticated = token !== null && user !== null

  /** Persist tokens + hydrate user from the access token's claims. */
  const applyTokens = useCallback((access: string, refresh: string) => {
    setTokens(access, refresh)
    setToken(access)
    setRefreshToken(refresh)
    setUser(decodeUser(access))
  }, [])

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
        // eslint-disable-next-line no-console
        console.info('[auth] login success', { user_id: res.data.token ? 'redacted' : null })
        return { ok: true, error: null }
      } catch (err) {
        return { ok: false, error: extractError(err, 'Invalid email or password.') }
      }
    },
    [applyTokens],
  )

  const register = useCallback(
    async (email: string, password: string): Promise<AuthResult> => {
      try {
        const res = await apiClient.post<AuthResponse>('/auth/register', { email, password })
        applyTokens(res.data.token, res.data.refresh_token)
        // eslint-disable-next-line no-console
        console.info('[auth] register success')
        return { ok: true, error: null }
      } catch (err) {
        return { ok: false, error: extractError(err, 'Registration failed.') }
      }
    },
    [applyTokens],
  )

  const logout = useCallback(() => {
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
      logout,
    }),
    [user, token, refreshToken, isAuthenticated, isLoading, login, register, logout],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

// eslint-disable-next-line react-refresh/only-export-components
export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within an <AuthProvider>')
  return ctx
}
