import { defineConfig, type Plugin } from 'vite'
import { spawn, type ChildProcess } from 'child_process'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { TanStackRouterVite } from '@tanstack/router-plugin/vite'

// Starts the Go backend with `air` when `vite dev` runs so the frontend
// proxy has something to talk to. Requires `air` on PATH (devenv provides it).
function goBackend(): Plugin {
  let proc: ChildProcess | null = null
  return {
    name: 'go-backend',
    configureServer() {
      proc = spawn('air', [], { cwd: '..', stdio: 'inherit' })
      proc.on('exit', () => { proc = null })
    },
    buildEnd() {
      if (proc) { proc.kill(); proc = null }
    },
  }
}

export default defineConfig({
  plugins: [
    TanStackRouterVite({
      routesDirectory: './src/routes',
      generatedRouteTree: './src/routeTree.gen.ts',
    }),
    react(),
    tailwindcss(),
    goBackend(),
  ],
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
  build: {
    outDir: 'dist',
  },
})
