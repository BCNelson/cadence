import { createFileRoute, Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { getTag } from '../api'
import { TransitionStream } from '../components/TransitionStream'
import { AuthGate } from '../components/AuthGate'
import { CheckRow } from '../components/CheckRow'
import { StatusBadge } from '../components/StatusBadge'

export const Route = createFileRoute('/tags/$tag')({
  component: () => (
    <AuthGate>
      <TagDetail />
    </AuthGate>
  ),
})

function TagDetail() {
  const { tag } = Route.useParams()
  const { data, isLoading, error } = useQuery({
    queryKey: ['tags', tag],
    queryFn: () => getTag(tag),
    refetchInterval: 30_000,
  })

  if (isLoading) return <Centered>Loading…</Centered>
  if (error) {
    return <Centered>Couldn't load tag: {(error as Error).message}</Centered>
  }
  if (!data) return null
  const checks = [...data.checks].sort((a, b) => a.slug.localeCompare(b.slug))

  return (
    <div className="mx-auto max-w-6xl px-4 py-8">
      <TransitionStream />
      <header className="mb-6 flex items-baseline justify-between">
        <div>
          <div className="mb-1 flex items-center gap-2">
            <h1 className="text-2xl font-bold text-slate-900">{data.name}</h1>
            <StatusBadge status={data.status} />
          </div>
          <p className="text-sm text-slate-600">{checks.length} checks</p>
        </div>
        <Link to="/tags" className="text-xs text-slate-500 hover:text-slate-900">
          ← All tags
        </Link>
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
      </div>
    </div>
  )
}

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <main className="flex min-h-screen items-center justify-center text-slate-500">
      {children}
    </main>
  )
}
