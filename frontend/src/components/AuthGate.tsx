import { type ReactNode } from 'react'
import { getAuthMode } from '../api'
import { ApiKeyGate } from './ApiKeyGate'
import { OidcGate } from './OidcGate'

// AuthGate is the single entry point used by routes. It delegates to the
// gate appropriate for the auth mode the SPA discovered at boot.
export function AuthGate({ children }: { children: ReactNode }) {
  if (getAuthMode() === 'oidc') {
    return <OidcGate>{children}</OidcGate>
  }
  return <ApiKeyGate>{children}</ApiKeyGate>
}
