import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { PrivateRoute, GuestRoute } from './routes/PrivateRoute'
import { Login } from './pages/Login'
import { Register } from './pages/Register'
import { Verify } from './pages/Verify'
import { ForgotPassword } from './pages/ForgotPassword'
import { ResetPassword } from './pages/ResetPassword'
import { Dashboard } from './pages/Dashboard'
import { PublicShare } from './pages/PublicShare'

/**
 * Top-level route tree.
 *
 *   /login, /register, /verify, /forgot-password, /reset-password → guest-only (redirect to /dashboard if signed in)
 *   /dashboard                                                     → private (redirect to /login if unauthenticated)
 *   /share/:token                                                  → public (accessible to all)
 *   /                                                              → redirect to /dashboard
 */
export function App() {
  return (
    <BrowserRouter>
      <Routes>
        {/* Guest routes */}
        <Route element={<GuestRoute />}>
          <Route path="/login" element={<Login />} />
          <Route path="/register" element={<Register />} />
          <Route path="/verify" element={<Verify />} />
          <Route path="/forgot-password" element={<ForgotPassword />} />
          <Route path="/reset-password" element={<ResetPassword />} />
        </Route>

        {/* Protected routes */}
        <Route element={<PrivateRoute />}>
          <Route path="/dashboard" element={<Dashboard />} />
        </Route>

        {/* Public Routes */}
        <Route path="/share/:token" element={<PublicShare />} />

        {/* Catch-all redirect */}
        <Route path="*" element={<Navigate to="/dashboard" replace />} />
      </Routes>
    </BrowserRouter>
  )
}

export default App
