import { defineConfig } from 'vite'

// Builds src/sw/sw.ts into dist/sw.js as one classic script without imports:
// module service workers aren't supported everywhere yet.
export default defineConfig({
  define: {
    // Every build gets a new shell cache, so old assets are dropped.
    __BUILD_ID__: JSON.stringify(Date.now().toString(36)),
  },
  build: {
    target: 'es2022',
    emptyOutDir: false,
    copyPublicDir: false,
    lib: {
      entry: 'src/sw/sw.ts',
      formats: ['iife'],
      name: 'homedriveServiceWorker',
      fileName: () => 'sw.js',
    },
  },
})
