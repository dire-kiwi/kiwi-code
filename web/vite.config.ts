import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { assertDevelopmentApiTarget, assertDevelopmentPort } from './scripts/dev-stack-options.mjs'

export default defineConfig({
  plugins: [
    {
      name: 'kiwi-code-development-port-safety',
      configResolved(config) {
        if (config.command === 'serve') {
          assertDevelopmentPort(config.server.port, 'Vite development server')
          assertDevelopmentApiTarget(
            config.env.VITE_KIWI_CODE_API_PORT,
            config.env.VITE_KIWI_CODE_API_URL,
          )
        }
      },
    },
    react(),
    tailwindcss(),
  ],
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
