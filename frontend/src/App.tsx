import { useEffect, type ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider, type AnyRouter } from '@tanstack/react-router'
import { AuthProvider, useAuth } from 'react-oidc-context'
import { WebStorageStateStore } from 'oidc-client-ts'
import { setBearerTokenProvider, type AuthConfig } from './api'

// TokenBridge plumbs the live OIDC id_token into the api module's
// pluggable auth header. Re-registers on each context update so silent
// token refresh is transparent to the rest of the SPA.
function TokenBridge() {
  const auth = useAuth()
  useEffect(() => {
    setBearerTokenProvider(() => auth.user?.id_token ?? null)
  }, [auth])
  return null
}

interface AppProps {
  router: AnyRouter
  queryClient: QueryClient
  authConfig: AuthConfig
}

export function App({ router, queryClient, authConfig }: AppProps) {
  const tree: ReactNode = <RouterProvider router={router} />
  if (!authConfig.oidc) {
    return <QueryClientProvider client={queryClient}>{tree}</QueryClientProvider>
  }
  const { issuer, client_id, audience } = authConfig.oidc
  return (
    <QueryClientProvider client={queryClient}>
      <AuthProvider
        authority={issuer}
        client_id={client_id}
        redirect_uri={`${window.location.origin}/callback`}
        scope="openid profile email"
        // Pass audience as an extra param only when it differs from
        // client_id; some IdPs require this for the access_token's `aud`
        // claim to match what cadence verifies.
        extraQueryParams={audience !== client_id ? { audience } : undefined}
        // Persist tokens across reloads so refreshes don't bounce back to the IdP.
        userStore={new WebStorageStateStore({ store: window.localStorage })}
        // Strip the auth response from the URL after /callback so a
        // refresh doesn't re-trigger the code exchange.
        onSigninCallback={() => {
          window.history.replaceState(null, '', '/')
        }}
      >
        <TokenBridge />
        {tree}
      </AuthProvider>
    </QueryClientProvider>
  )
}
