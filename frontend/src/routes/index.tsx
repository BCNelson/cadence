import { createFileRoute, Link, useNavigate, useSearch } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import {
  listChecks,
  clearApiKey,
  getAuthMode,
  rollupStatus,
  uniqueTags,
  type Check,
  type CheckStatus,
} from '../api'
import { useTransitionStream } from '../sse'
import { CheckRow } from '../components/CheckRow'
import { AuthGate } from '../components/AuthGate'
import { useAuth } from 'react-oidc-context'

// Search-params schema: ?tag=foo&tag=bar selects an AND-filter.
type DashboardSearch = { tag?: string[] }

export const Route = createFileRoute('/')({
  validateSearch: (search: Record<string, unknown>): DashboardSearch => {
    const raw = search.tag
    if (Array.isArray(raw)) return { tag: raw.map(String) }
    if (typeof raw === 'string' && raw !== '') return { tag: [raw] }
    return {}
  },
  component: () => (
    <AuthGate>
      <Dashboard />
    </AuthGate>
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

export function Dashboard() {
  useTransitionStream()
  // strict:false lets the Dashboard component render outside of the index
  // route match (e.g. in unit tests) — it returns {} in that case.
  const search = useSearch({ strict: false }) as DashboardSearch
  const navigate = useNavigate({ from: '/' })
  const selectedTags = search.tag ?? []

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
  const allChecks = data?.checks ?? []
  const tags = uniqueTags(allChecks)
  const filtered = selectedTags.length === 0
    ? allChecks
    : allChecks.filter((c) => {
        const have = new Set(c.tags.split(' ').filter(Boolean))
        return selectedTags.every((t) => have.has(t))
      })
  const checks = [...filtered].sort(compareChecks)

  const toggleTag = (tag: string) => {
    const next = selectedTags.includes(tag)
      ? selectedTags.filter((t) => t !== tag)
      : [...selectedTags, tag]
    void navigate({
      to: '/',
      search: next.length === 0 ? {} : { tag: next },
    })
  }

  return (
    <div className="mx-auto max-w-6xl px-4 py-8">
      <header className="mb-6 flex items-baseline justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-900">cadence</h1>
          <p className="text-sm text-slate-600">
            {selectedTags.length === 0
              ? `${checks.length} checks`
              : `${checks.length} of ${allChecks.length} checks`}
          </p>
        </div>
        <div className="flex items-center gap-3">
          <Link to="/tags" className="text-xs text-slate-500 hover:text-slate-900">
            Tags →
          </Link>
          <SignOutButton />
        </div>
      </header>

      {tags.length > 0 && (
        <TagFilterBar
          tags={tags}
          checks={allChecks}
          selected={selectedTags}
          onToggle={toggleTag}
        />
      )}

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
            {allChecks.length === 0
              ? 'No checks declared. Add them in your config file.'
              : 'No checks match the selected tags.'}
          </div>
        )}
      </div>
    </div>
  )
}

// TagFilterBar renders a clickable chip per tag with the rollup status
// next to the name. Clicking toggles AND-membership in ?tag=.
function TagFilterBar({
  tags,
  checks,
  selected,
  onToggle,
}: {
  tags: string[]
  checks: Check[]
  selected: string[]
  onToggle: (tag: string) => void
}) {
  return (
    <div className="mb-4 flex flex-wrap items-center gap-2">
      <span className="text-xs font-medium uppercase tracking-wide text-slate-500">
        Filter by tag:
      </span>
      {tags.map((tag) => {
        const members = checks.filter((c) =>
          c.tags.split(' ').filter(Boolean).includes(tag),
        )
        const status = rollupStatus(members)
        const active = selected.includes(tag)
        return (
          <button
            key={tag}
            type="button"
            onClick={() => onToggle(tag)}
            className={`inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs transition-colors ${
              active
                ? 'border-slate-900 bg-slate-900 text-white'
                : 'border-slate-200 bg-white text-slate-700 hover:border-slate-400'
            }`}
          >
            {status && <StatusDot status={status} />}
            <span>{tag}</span>
          </button>
        )
      })}
      {selected.length > 0 && (
        <button
          type="button"
          onClick={() => selected.forEach(onToggle)}
          className="text-xs text-slate-500 underline hover:text-slate-900"
        >
          clear
        </button>
      )}
    </div>
  )
}

function StatusDot({ status }: { status: CheckStatus }) {
  // Tiny colored dot inside the tag chip — compact enough that a row of
  // chips stays readable.
  const colors: Record<CheckStatus, string> = {
    up: 'bg-emerald-500',
    grace: 'bg-amber-500',
    down: 'bg-rose-500',
    new: 'bg-slate-400',
    paused: 'bg-slate-300',
  }
  return <span className={`inline-block h-2 w-2 rounded-full ${colors[status]}`} />
}

// SignOutButton dispatches the right sign-out for the active auth mode.
// The OIDC branch only renders when AuthProvider is mounted (i.e. the SPA
// discovered OIDC at boot), so useAuth is safe to call there.
function SignOutButton() {
  if (getAuthMode() === 'oidc') return <OidcSignOutButton />
  return (
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
  )
}

function OidcSignOutButton() {
  const auth = useAuth()
  return (
    <button
      type="button"
      onClick={() => void auth.removeUser()}
      className="text-xs text-slate-500 hover:text-slate-900"
    >
      Sign out
    </button>
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
        {getAuthMode() === 'apikey' && (
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
        )}
      </div>
    </main>
  )
}
