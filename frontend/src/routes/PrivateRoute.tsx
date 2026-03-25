import { Navigate, Outlet } from 'react-router-dom'
import { useAuth } from '../hooks/useAuth'
import { FullPageSpinner } from '../components/ui/Spinner'

/**
 * Route guard for authenticated views (dashboard, file explorer, etc.).
 * - While auth is bootstrapping (isLoading), shows a full-page spinner to
 *   prevent route-guard flicker.
 * - If not authenticated, redirects to /login and records the attempted path.
 */
export function PrivateRoute() {
  const { isAuthenticated, isLoading } = useAuth()

  if (isLoading) return <FullPageSpinner />
  if (!isAuthenticated) return <Navigate to="/login" replace />
  return <Outlet />
}

/**
 * Route guard for guest-only views (login, register).
 * Redirects already-authenticated users straight to the dashboard.
 */
export function GuestRoute() {
  const { isAuthenticated, isLoading } = useAuth()

  if (isLoading) return <FullPageSpinner />
  if (isAuthenticated) return <Navigate to="/dashboard" replace />
  return <Outlet />
}
