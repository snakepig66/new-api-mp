import { fileURLToPath } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

const srcDir = fileURLToPath(new URL('./src', import.meta.url))

// Development proxy: the Go backend owns /api, /v1, /mj and /pg.
const backend = process.env.MP_BACKEND ?? 'http://localhost:3000'

export default defineConfig({
  plugins: [
    tanstackRouter({
      target: 'react',
      autoCodeSplitting: true,
      routesDirectory: './src/app/routes',
      generatedRouteTree: './src/app/routeTree.gen.ts',
    }),
    react(),
    tailwindcss(),
  ],
  resolve: {
    alias: { '@': srcDir },
  },
  server: {
    port: 5173,
    proxy: Object.fromEntries(
      ['/api', '/v1', '/mj', '/pg'].map((prefix) => [
        prefix,
        { target: backend, changeOrigin: true },
      ]),
    ),
  },
  build: {
    outDir: 'dist',
    assetsDir: 'assets',
    target: 'es2022',
    sourcemap: false,
  },
})
