import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { assertDevelopmentApiPort, assertDevelopmentPort } from './scripts/dev-stack-options.mjs'

export default defineConfig({
  plugins: [
    {
      name: 'kiwi-code-development-port-safety',
      configResolved(config) {
        if (config.command === 'serve') {
          assertDevelopmentPort(config.server.port, 'Vite development server')
          assertDevelopmentApiPort(config.env.VITE_KIWI_CODE_API_PORT)
        }
      },
    },
    react(),
    tailwindcss(),
  ],
  // Kept in step with the `paths` entry in tsconfig.app.json and the alias in
  // vitest.config.ts -- all three resolve `@/` independently.
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    host: '0.0.0.0',
    port: 5173,
    strictPort: true,
  },
  build: {
    outDir: '../internal/server/static/app',
    emptyOutDir: true,
  },
})
