// Live updates via Server-Sent Events. The bus publishes a `transition`
// event for every state change; on receipt we invalidate the checks
// query so TanStack Query refetches and re-renders.
import { useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { getApiKey } from './api'

export function useTransitionStream(): void {
  const qc = useQueryClient()
  useEffect(() => {
    const key = getApiKey()
    if (!key) return
    // EventSource can't set headers, so the api key rides as a query
    // parameter. The server accepts X-Api-Key OR ?api_key= for /events.
    const es = new EventSource(`/events?api_key=${encodeURIComponent(key)}`)
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
