// Live updates via Server-Sent Events. The bus publishes a `transition`
// event for every state change; on receipt we invalidate the checks
// query so TanStack Query refetches and re-renders.
import { useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { eventsURL } from './api'

export function useTransitionStream(): void {
  const qc = useQueryClient()
  useEffect(() => {
    // EventSource can't set headers, so the credential rides as a query
    // parameter (api_key= in API-key mode, access_token= in OIDC mode).
    const url = eventsURL()
    if (!url) return
    const es = new EventSource(url)
    const onTransition = (): void => {
      // Don't try to merge the patch into the cache by hand — the read
      // API is authoritative (status, last_ping, next_ping all change),
      // so refetching is simpler and the response is small.
      void qc.invalidateQueries({ queryKey: ['checks'] })
    }
    es.addEventListener('transition', onTransition)
    return () => {
      es.removeEventListener('transition', onTransition)
      es.close()
    }
  }, [qc])
}
