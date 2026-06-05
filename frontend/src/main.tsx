import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient } from '@tanstack/react-query'
import { createRouter } from '@tanstack/react-router'
import { routeTree } from './routeTree.gen'
import { fetchAuthConfig, setAuthMode } from './api'
import { App } from './App'
import './styles.css'

const router = createRouter({ routeTree })
const queryClient = new QueryClient()

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}

async function bootstrap() {
  const authConfig = await fetchAuthConfig()
  setAuthMode(authConfig.oidc ? 'oidc' : 'apikey')
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <App router={router} queryClient={queryClient} authConfig={authConfig} />
    </StrictMode>,
  )
}

void bootstrap()
