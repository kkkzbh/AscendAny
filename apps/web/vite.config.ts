import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { resolve, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const rdir = dirname(fileURLToPath(import.meta.url))
const backendOrigin = process.env.VITE_BACKEND_ORIGIN || 'http://127.0.0.1:8000'
const backendWsOrigin = backendOrigin.replace(/^http/i, 'ws')

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  void mode
  return {
    plugins: [react()],
    base: '/',
    resolve: {
      alias: {
        vscode: 'monaco-languageclient/lib/vscode-compatibility',
      },
      // Avoid duplicate React copies (can trigger "Invalid hook call")
      dedupe: ['react', 'react-dom', 'react/jsx-runtime', 'react/jsx-dev-runtime'],
    },
    server: {
      port: 5175,
      proxy: {
        '/api': {
          target: backendOrigin,
          changeOrigin: true,
        },
        '/ws': {
          target: backendWsOrigin,
          ws: true,
          changeOrigin: true,
        },
      },
    },
    build: {
      outDir: resolve(rdir, 'dist'),
      emptyOutDir: true,
    },
  }
})
