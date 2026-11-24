import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': path.resolve(path.dirname(fileURLToPath(import.meta.url)), 'src') },
  },
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
