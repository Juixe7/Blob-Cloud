import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { AuthProvider } from './context/AuthContext'
import { UploadProvider } from './context/UploadContext'
import App from './App'
import './index.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <AuthProvider>
      <UploadProvider>
        <App />
      </UploadProvider>
    </AuthProvider>
  </StrictMode>,
)
