import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { PrivateRoute, GuestRoute } from './routes/PrivateRoute'
import { Login } from './pages/Login'
import { Register } from './pages/Register'
import { Dashboard } from './pages/Dashboard'

/**
 * Top-level route tree.
 *
 *   /login, /register  → guest-only (redirect to /dashboard if signed in)
 *   /dashboard          → private (redirect to /login if unauthenticated)
 *   /                   → redirect to /dashboard
 */
export function App() {
  return (
    <BrowserRouter>
      <Routes>
        {/* Guest routes */}
        <Route element={<GuestRoute />}>
          <Route path="/login" element={<Login />} />
          <Route path="/register" element={<Register />} />
        </Route>

        {/* Protected routes */}
        <Route element={<PrivateRoute />}>
          <Route path="/dashboard" element={<Dashboard />} />
        </Route>

        {/* Catch-all redirect */}
        <Route path="*" element={<Navigate to="/dashboard" replace />} />
      </Routes>
    </BrowserRouter>
  )
}

export default App
