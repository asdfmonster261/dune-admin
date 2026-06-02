import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// During `npm run dev`, the Vite dev server proxies /api → the local Go
// binary so the React app can talk to it without CORS overhead. In the
// production build (embedded in the Go binary), same-origin handles this.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': { target: 'http://localhost:8080', changeOrigin: true },
      '/api/v1/logs/stream': { target: 'ws://localhost:8080', ws: true, changeOrigin: true },
    },
  },
})
