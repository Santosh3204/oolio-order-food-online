import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Dev server proxies /api to the Go API, so the browser sees one origin (no CORS needed).
// Override the target with API_URL=http://host:port npm run dev
const target = process.env.API_URL || 'http://localhost:8080'

export default defineConfig({
  plugins: [react()],
  server: { port: 5173, proxy: { '/api': { target, changeOrigin: true } } },
})
