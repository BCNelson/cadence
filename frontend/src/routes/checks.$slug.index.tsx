import { createFileRoute, Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { getCheck, getPingsForCheck, type Ping } from '../api'
import { useTransitionStream } from '../sse'
import { StatusBadge } from '../components/StatusBadge'

export const Route = createFileRoute('/checks/$slug/')({
  component: RouteComponent,
})

function RouteComponent() {
  const { slug } = Route.useParams()
  return <CheckDetail slug={slug} />
}

export function CheckDetail({ slug }: { slug: string }) {
  useTransitionStream()

  const checkQ = useQuery({
    queryKey: ['checks', slug],
    queryFn: () => getCheck(slug),
    refetchInterval: 30_000,
  })
  const pingsQ = useQuery({
    queryKey: ['checks', slug, 'pings'],
    queryFn: () => getPingsForCheck(slug),
    refetchInterval: 30_000,
  })

  if (checkQ.isLoading) return <Centered>Loading…</Centered>
  if (checkQ.error) {
    return <Centered>Couldn't load check: {(checkQ.error as Error).message}</Centered>
  }
  const check = checkQ.data
  if (!check) return null
  const pings = pingsQ.data?.pings ?? []
  const durations = pairRunDurations(pings)

  return (
    <div className="mx-auto max-w-6xl px-4 py-8">
      <header className="mb-6">
        <Link to="/" className="text-xs text-slate-500 hover:text-slate-900">
          ← All checks
        </Link>
        <div className="mt-2 flex items-center gap-3">
          <h1 className="text-2xl font-bold text-slate-900">{check.name ?? check.slug}</h1>
          <StatusBadge status={check.status} />
          {check.has_open_run && (
            <span className="inline-flex items-center rounded-full bg-sky-100 px-2 py-0.5 text-xs font-medium text-sky-800">
              RUNNING
            </span>
          )}
        </div>
        <div className="mt-1 text-xs text-slate-500">{check.slug}</div>
      </header>

      <dl className="mb-6 grid grid-cols-2 gap-x-6 gap-y-3 rounded-lg border border-slate-200 bg-white p-4 text-sm md:grid-cols-4">
        <Field label="Schedule">
          {check.schedule ? (
            <code className="rounded bg-slate-100 px-1 py-0.5 text-xs">{check.schedule}</code>
          ) : check.timeout ? (
            <span>every {Math.round(check.timeout / 60)}m</span>
          ) : (
            <span className="text-slate-400">—</span>
          )}
        </Field>
        <Field label="Grace">{check.grace}s</Field>
        <Field label="Last ping">{formatAbsolute(check.last_ping)}</Field>
        <Field label="Next ping">{formatAbsolute(check.next_ping)}</Field>
        <Field label="Pings retained">{check.n_pings}</Field>
        <Field label="Timezone">{check.timezone ?? '—'}</Field>
        <Field label="Tags">
          {check.tags
            .split(' ')
            .filter(Boolean)
            .map((tag) => (
              <Link
                key={tag}
                to="/tags/$tag"
                params={{ tag }}
                className="mr-1 inline-block rounded bg-slate-100 px-1.5 py-0.5 text-xs hover:bg-slate-200"
              >
                {tag}
              </Link>
            )) || <span className="text-slate-400">—</span>}
        </Field>
        {check.ping_url && (
          <Field label="Ping URL" wide>
            <code className="break-all rounded bg-slate-100 px-1 py-0.5 text-xs">
              {check.ping_url}
            </code>
          </Field>
        )}
      </dl>

      <div className="mb-2 flex items-baseline justify-between">
        <h2 className="text-sm font-semibold uppercase tracking-wide text-slate-500">
          Ping history
        </h2>
        <span className="text-xs text-slate-400">{pings.length} retained</span>
      </div>
      <div className="overflow-hidden rounded-lg border border-slate-200 bg-white">
        <table className="min-w-full text-sm">
          <thead className="bg-slate-50 text-left text-xs uppercase tracking-wide text-slate-500">
            <tr>
              <th className="py-2 pl-4 pr-2 font-medium">Type</th>
              <th className="py-2 px-2 font-medium">When</th>
              <th className="py-2 px-2 font-medium">Duration</th>
              <th className="py-2 px-2 font-medium">Remote</th>
              <th className="py-2 px-2 font-medium">User-Agent</th>
              <th className="py-2 px-4 text-right font-medium">Body</th>
            </tr>
          </thead>
          <tbody>
            {pings.map((p) => (
              <tr
                key={p.id}
                className="border-b border-slate-200 last:border-0 hover:bg-slate-50"
              >
                <td className="py-2 pl-4 pr-2">
                  <Link
                    to="/checks/$slug/pings/$pingId"
                    params={{ slug, pingId: p.id }}
                    className="hover:underline"
                  >
                    <PingTypeBadge ping={p} />
                  </Link>
                </td>
                <td className="py-2 px-2 tabular-nums text-slate-600">
                  <Link
                    to="/checks/$slug/pings/$pingId"
                    params={{ slug, pingId: p.id }}
                    className="hover:underline"
                  >
                    {formatAbsolute(p.date)}
                  </Link>
                </td>
                <td className="py-2 px-2 tabular-nums text-slate-600">
                  {formatDuration(durations[p.id])}
                </td>
                <td className="py-2 px-2 text-xs text-slate-500">
                  {p.remote_addr || '—'}
                </td>
                <td className="py-2 px-2 text-xs text-slate-500 truncate max-w-xs">
                  {p.ua || '—'}
                </td>
                <td className="py-2 px-4 text-right text-xs tabular-nums text-slate-500">
                  {p.body_size ? `${p.body_size}B` : '—'}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {pings.length === 0 && !pingsQ.isLoading && (
          <div className="py-8 text-center text-sm text-slate-500">
            No pings recorded yet.
          </div>
        )}
        {pingsQ.error && (
          <div className="py-4 text-center text-sm text-rose-600">
            Couldn't load pings: {(pingsQ.error as Error).message}
          </div>
        )}
      </div>
    </div>
  )
}

function Field({
  label,
  children,
  wide,
}: {
  label: string
  children: React.ReactNode
  wide?: boolean
}) {
  return (
    <div className={wide ? 'col-span-2 md:col-span-4' : undefined}>
      <dt className="text-xs uppercase tracking-wide text-slate-500">{label}</dt>
      <dd className="mt-0.5 text-slate-900">{children}</dd>
    </div>
  )
}

function PingTypeBadge({ ping }: { ping: Ping }) {
  const label =
    ping.type === 'exitstatus' && ping.exitstatus != null
      ? `exit ${ping.exitstatus}`
      : ping.type
  const tone = pingTone(ping)
  return (
    <span
      className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${tone}`}
    >
      {label}
    </span>
  )
}

function pingTone(p: Ping): string {
  if (p.type === 'fail') return 'bg-rose-100 text-rose-800'
  if (p.type === 'exitstatus' && (p.exitstatus ?? 0) !== 0)
    return 'bg-rose-100 text-rose-800'
  if (p.type === 'success' || (p.type === 'exitstatus' && p.exitstatus === 0))
    return 'bg-emerald-100 text-emerald-800'
  if (p.type === 'start') return 'bg-sky-100 text-sky-800'
  if (p.type === 'log') return 'bg-slate-200 text-slate-700'
  return 'bg-slate-100 text-slate-700'
}

// pairRunDurations walks the newest-first ping list and pairs each
// closing ping (success/fail/exit) with the nearest preceding start
// ping. Returned map is keyed by closing ping ID; entries without a
// matching start are absent (rendered as "—" in the table).
function pairRunDurations(pings: Ping[]): Record<string, number> {
  const out: Record<string, number> = {}
  for (let i = 0; i < pings.length; i++) {
    const end = pings[i]
    if (!isClosing(end)) continue
    for (let j = i + 1; j < pings.length; j++) {
      const candidate = pings[j]
      if (candidate.type === 'start') {
        const ms = new Date(end.date).getTime() - new Date(candidate.date).getTime()
        if (Number.isFinite(ms) && ms >= 0) out[end.id] = ms
        break
      }
      if (isClosing(candidate)) break
    }
  }
  return out
}

function isClosing(p: Ping): boolean {
  return p.type === 'success' || p.type === 'fail' || p.type === 'exitstatus'
}

function formatDuration(ms: number | undefined): string {
  if (ms == null) return '—'
  if (ms < 1000) return `${ms}ms`
  const s = ms / 1000
  if (s < 60) return `${s.toFixed(1)}s`
  const m = Math.floor(s / 60)
  const rs = Math.round(s - m * 60)
  return `${m}m ${rs}s`
}

function formatAbsolute(iso: string | null | undefined): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <main className="flex min-h-screen items-center justify-center text-slate-500">
      {children}
    </main>
  )
}
