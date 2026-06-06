import { createFileRoute, Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { listTags } from '../api'
import { TransitionStream } from '../components/TransitionStream'
import { AuthGate } from '../components/AuthGate'
import { StatusBadge } from '../components/StatusBadge'

export const Route = createFileRoute('/tags')({
  component: () => (
    <AuthGate>
      <TagsIndex />
    </AuthGate>
  ),
})

function TagsIndex() {
  const { data, isLoading, error } = useQuery({
    queryKey: ['tags'],
    queryFn: listTags,
    refetchInterval: 30_000,
  })

  if (isLoading) return <Centered>Loading…</Centered>
  if (error) {
    return <Centered>Couldn't load tags: {(error as Error).message}</Centered>
  }
  const tags = data?.tags ?? []

  return (
    <div className="mx-auto max-w-6xl px-4 py-8">
      <TransitionStream />
      <header className="mb-6 flex items-baseline justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-900">Tags</h1>
          <p className="text-sm text-slate-600">{tags.length} tags</p>
        </div>
        <Link to="/" className="text-xs text-slate-500 hover:text-slate-900">
          ← All checks
        </Link>
      </header>

      <div className="overflow-hidden rounded-lg border border-slate-200 bg-white">
        <table className="min-w-full text-sm">
          <thead className="bg-slate-50 text-left text-xs uppercase tracking-wide text-slate-500">
            <tr>
              <th className="py-2 pl-4 pr-2 font-medium">Status</th>
              <th className="py-2 px-2 font-medium">Tag</th>
              <th className="py-2 px-4 text-right font-medium">Checks</th>
            </tr>
          </thead>
          <tbody>
            {tags.map((t) => (
              <tr key={t.name} className="border-b border-slate-200 last:border-0 hover:bg-slate-50">
                <td className="py-3 pl-4 pr-2">
                  <StatusBadge status={t.status} />
                </td>
                <td className="py-3 px-2 font-medium">
                  <Link
                    to="/tags/$tag"
                    params={{ tag: t.name }}
                    className="text-slate-900 hover:underline"
                  >
                    {t.name}
                  </Link>
                </td>
                <td className="py-3 px-4 text-right text-xs tabular-nums text-slate-500">
                  {t.n_checks}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {tags.length === 0 && (
          <div className="py-8 text-center text-sm text-slate-500">
            No tags. Add <code>tags:</code> to a check in your config file.
          </div>
        )}
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
