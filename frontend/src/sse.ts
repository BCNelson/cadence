// Live updates via Server-Sent Events. The bus publishes a `transition`
// event for every state change; on receipt we invalidate the checks
// query so TanStack Query refetches and re-renders.
import { useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'

export function useTransitionStream(): void {
  const qc = useQueryClient()
  useEffect(() => {
    const es = new EventSource('/events')
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
