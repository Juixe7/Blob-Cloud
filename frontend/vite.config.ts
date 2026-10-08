import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import fs from 'node:fs'

// Vite config: dev server on :5173 with /api and /health proxied to the Go
// backend on :8090, so the browser talks same-origin while developing.
export default defineConfig({
  root: fs.realpathSync(process.cwd()),
  plugins: [react()],
  server: {
    port: 5173,
    headers: {
      'Cross-Origin-Opener-Policy': 'same-origin-allow-popups',
    },
    fs: {
      allow: [
        '..',
        'C:/Users/Asus/Desktop/ProjectsFInal/z',
        'C:/Users/Asus/Desktop/z',
      ],
    },
    proxy: {
      '/api': {
        target: 'http://localhost:8090',
        changeOrigin: true,
        // Phase 7.5: proxy WebSocket upgrade requests so the browser can talk
        // to the Go backend's /api/ws endpoint same-origin in development.
        ws: true,
      },
      '/health': {
        target: 'http://localhost:8090',
        changeOrigin: true,
      },
    },
  },
})
