import { useAuth } from 'react-oidc-context'
import { getApiKey, getAuthMode } from '../api'
import { useTransitionStream } from '../sse'

// TransitionStream is a render-free helper that picks the right credential
// source for the active auth mode and feeds it to useTransitionStream.
// Routes render <TransitionStream /> instead of calling the hook directly
// — splitting on mode at the component boundary keeps the conditional out
// of the hook itself (useAuth() throws when AuthProvider is absent, i.e.
// in API-key mode).
export function TransitionStream() {
  return getAuthMode() === 'oidc' ? <OidcTransitionStream /> : <ApiKeyTransitionStream />
}

function OidcTransitionStream(): null {
  const auth = useAuth()
  useTransitionStream(auth.user?.id_token ?? null)
  return null
}

function ApiKeyTransitionStream(): null {
  // The API key only changes via sign-out, which reloads the page, so a
  // one-shot read is enough — no need to make this reactive.
  useTransitionStream(getApiKey())
  return null
}
