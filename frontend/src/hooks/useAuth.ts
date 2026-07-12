// Shortcut hook re-export. The real implementation lives in AuthContext so
// the provider + consumer stay co-located; this file satisfies the planned
// folder layout (src/hooks/useAuth.*) and gives components a stable import
// path that doesn't reach into context internals.
export { useAuth } from '../context/AuthContext'
