import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// The daemon serves the built bundle from its own binary, so the build output
// goes straight into the Go embed directory. In dev, Vite serves the UI and
// proxies the API to the running daemon.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: '../daemon/internal/webui/dist',
    emptyOutDir: true,
    // The dashboard is a single view; a lone bundle beats a waterfall of chunks.
    chunkSizeWarningLimit: 900,
  },
  server: {
    port: 5199,
    strictPort: true,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:7777',
        changeOrigin: false,
      },
    },
  },
})
