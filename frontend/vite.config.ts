import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Vite config: dev server on :5173 with /api and /health proxied to the Go
// backend on :8090, so the browser talks same-origin while developing.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:8090',
        changeOrigin: true,
      },
      '/health': {
        target: 'http://localhost:8090',
        changeOrigin: true,
      },
    },
  },
})
