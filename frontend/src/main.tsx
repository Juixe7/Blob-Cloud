import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { GoogleOAuthProvider } from '@react-oauth/google'
import { AuthProvider } from './context/AuthContext'
import { UploadProvider } from './context/UploadContext'
import { ToastProvider } from './components/Toast'
import App from './App'
import './index.css'

const googleClientId = import.meta.env.VITE_GOOGLE_CLIENT_ID || 'dummy-client-id.apps.googleusercontent.com'

// Initialize theme: force dark mode
document.documentElement.classList.remove('light')
document.documentElement.classList.add('dark')

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <GoogleOAuthProvider clientId={googleClientId}>
      <AuthProvider>
        <UploadProvider>
          <ToastProvider>
            <App />
          </ToastProvider>
        </UploadProvider>
      </AuthProvider>
    </GoogleOAuthProvider>
  </StrictMode>,
)
