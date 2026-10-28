import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: {
    target: 'es2022',
    // The service worker is built separately (vite.sw.config.ts) into the
    // same folder, so it can be a single self-contained file at /sw.js.
    emptyOutDir: true,
  },
  server: {
    // During development the Go server answers the API.
    proxy: { '/api': 'http://localhost:8080' },
  },
})
