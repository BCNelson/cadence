import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { listChecks, clearApiKey, type Check, type CheckStatus } from '../api'
import { useTransitionStream } from '../sse'
import { CheckRow } from '../components/CheckRow'
import { ApiKeyGate } from '../components/ApiKeyGate'

export const Route = createFileRoute('/')({
  component: () => (
    <ApiKeyGate>
      <Dashboard />
    </ApiKeyGate>
  ),
})

// statusOrder controls the row ordering: alerting first, healthy last.
// Operators care about anomalies; show those at the top so they're seen
// without scrolling.
const statusOrder: Record<CheckStatus, number> = {
  down: 0,
  grace: 1,
  new: 2,
  up: 3,
  paused: 4,
}

function compareChecks(a: Check, b: Check): number {
  const so = statusOrder[a.status] - statusOrder[b.status]
  if (so !== 0) return so
  return a.slug.localeCompare(b.slug)
}

function Dashboard() {
  useTransitionStream()
  const { data, isLoading, error } = useQuery({
    queryKey: ['checks'],
    queryFn: listChecks,
    // SSE will invalidate the cache on transitions; a 30s background
    // poll catches anything we miss (reconnect-after-network-blip etc).
    refetchInterval: 30_000,
  })

  if (isLoading) {
    return <Loading />
  }
  if (error) {
    return <ErrorBanner message={(error as Error).message} />
  }
  const checks = [...(data?.checks ?? [])].sort(compareChecks)

  return (
    <div className="mx-auto max-w-6xl px-4 py-8">
      <header className="mb-6 flex items-baseline justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-900">cadence</h1>
          <p className="text-sm text-slate-600">{checks.length} checks</p>
        </div>
        <button
          type="button"
          onClick={() => {
            clearApiKey()
            window.location.reload()
          }}
          className="text-xs text-slate-500 hover:text-slate-900"
        >
          Sign out
        </button>
      </header>

      <div className="overflow-hidden rounded-lg border border-slate-200 bg-white">
        <table className="min-w-full text-sm">
          <thead className="bg-slate-50 text-left text-xs uppercase tracking-wide text-slate-500">
            <tr>
              <th className="py-2 pl-4 pr-2 font-medium">Status</th>
              <th className="py-2 px-2 font-medium">Check</th>
              <th className="py-2 px-2 font-medium">Schedule</th>
              <th className="py-2 px-2 font-medium">Last ping</th>
              <th className="py-2 px-2 font-medium">Next ping</th>
              <th className="py-2 px-2 font-medium">Tags</th>
              <th className="py-2 px-4 text-right font-medium">Pings</th>
            </tr>
          </thead>
          <tbody>
            {checks.map((c) => (
              <CheckRow key={c.slug} check={c} />
            ))}
          </tbody>
        </table>
        {checks.length === 0 && (
          <div className="py-8 text-center text-sm text-slate-500">
            No checks declared. Add them in your config file.
          </div>
        )}
      </div>
    </div>
  )
}

function Loading() {
  return (
    <main className="flex min-h-screen items-center justify-center text-slate-500">
      Loading…
    </main>
  )
}

function ErrorBanner({ message }: { message: string }) {
  return (
    <main className="mx-auto max-w-3xl px-4 py-8">
      <div className="rounded border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-800">
        <strong>Couldn't load checks:</strong> {message}
        <div className="mt-2">
          <button
            type="button"
            onClick={() => {
              clearApiKey()
              window.location.reload()
            }}
            className="text-xs underline"
          >
            Reset API key
          </button>
        </div>
      </div>
    </main>
  )
}
