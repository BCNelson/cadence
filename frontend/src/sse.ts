// Live updates via Server-Sent Events. The bus publishes a `transition`
// event for every state change; on receipt we invalidate the checks and
// tags queries so TanStack Query refetches and re-renders.
//
// The hook keys off the live credential so an EventSource is opened the
// moment the credential becomes available (avoiding a first-mount race
// against the OIDC TokenBridge) and replaced when the credential
// changes (covering silent OIDC token renewal — otherwise the connection
// reconnects with a stale token and the IdP rejects it).
import { useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { getAuthMode } from './api'

export function eventsURL(credential: string): string {
  const param = getAuthMode() === 'oidc' ? 'access_token' : 'api_key'
  return `/events?${param}=${encodeURIComponent(credential)}`
}

export function useTransitionStream(credential: string | null): void {
  const qc = useQueryClient()
  useEffect(() => {
    if (!credential) return
    const es = new EventSource(eventsURL(credential))
    const onTransition = (): void => {
      // Don't try to merge the patch into the cache by hand — the read
      // API is authoritative (status, last_ping, next_ping all change),
      // so refetching is simpler and the response is small. Tag rollups
      // are derived from the same underlying state, so invalidate both
      // query families on every transition.
      void qc.invalidateQueries({ queryKey: ['checks'] })
      void qc.invalidateQueries({ queryKey: ['tags'] })
    }
    es.addEventListener('transition', onTransition)
    return () => {
      es.removeEventListener('transition', onTransition)
      es.close()
    }
  }, [credential, qc])
}
