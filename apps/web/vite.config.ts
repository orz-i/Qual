import { fileURLToPath } from 'node:url'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { defineConfig } from 'vite'

const apiProxyTarget = process.env.QUAL_API_PROXY_TARGET || 'http://127.0.0.1:18080'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    // Route browser API requests to the independent Go service in development.
    proxy: {
      '/api': apiProxyTarget,
      '/healthz': apiProxyTarget,
      '/readyz': apiProxyTarget,
    },
  },
})
