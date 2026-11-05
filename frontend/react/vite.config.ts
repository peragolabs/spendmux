import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
      '/auth': 'http://localhost:8080',
      '/webhooks': 'http://localhost:8080',
      '/graphql': 'http://localhost:8080',
    },
  },
  build: {
    outDir: '../../priv/www',
    emptyOutDir: true,
    rollupOptions: {
      output: {
        // Static assets are served immutable in production, so every build must
        // generate new URLs rather than reusing a stale browser-cached bundle.
        entryFileNames: 'assets/index-[hash].js',
        assetFileNames: (info) =>
          info.name && info.name.endsWith('.css') ? 'assets/index-[hash].css' : 'assets/[name][extname]',
        chunkFileNames: 'assets/[name]-[hash].js',
      },
    },
  },
})
