import { createFileRoute, Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { getPing, type PingDetail } from '../api'

// Auth is handled by the /checks/$slug layout (checks.$slug.tsx), so
// the leaf doesn't re-wrap in AuthGate.
export const Route = createFileRoute('/checks/$slug/pings/$pingId')({
  component: RouteComponent,
})

function RouteComponent() {
  const { slug, pingId } = Route.useParams()
  return <PingDetailPage slug={slug} pingId={pingId} />
}

export function PingDetailPage({ slug, pingId }: { slug: string; pingId: string }) {
  const { data, isLoading, error } = useQuery({
    queryKey: ['checks', slug, 'pings', pingId],
    queryFn: () => getPing(slug, pingId),
  })

  if (isLoading) return <Centered>Loading…</Centered>
  if (error) {
    return <Centered>Couldn't load ping: {(error as Error).message}</Centered>
  }
  if (!data) return null

  return (
    <div className="mx-auto max-w-4xl px-4 py-8">
      <header className="mb-6">
        <Link
          to="/checks/$slug"
          params={{ slug }}
          className="text-xs text-slate-500 hover:text-slate-900"
        >
          ← Back to {slug}
        </Link>
        <div className="mt-2 flex items-center gap-3">
          <h1 className="text-2xl font-bold text-slate-900">
            Ping <code className="text-base">{pingId}</code>
          </h1>
          <TypeBadge ping={data} />
        </div>
      </header>

      <dl className="mb-6 grid grid-cols-2 gap-x-6 gap-y-3 rounded-lg border border-slate-200 bg-white p-4 text-sm md:grid-cols-2">
        <Field label="Type">{data.type}</Field>
        <Field label="Exact time">{formatExact(data.date, pingId)}</Field>
        {data.exitstatus != null && (
          <Field label="Exit status">{data.exitstatus}</Field>
        )}
        <Field label="Body size">{data.body_size ? `${data.body_size} bytes` : '—'}</Field>
        <Field label="Body truncated">{data.truncated ? 'yes' : 'no'}</Field>
        <Field label="Remote address">{data.remote_addr || '—'}</Field>
        <Field label="User-Agent" wide>
          <code className="break-all text-xs">{data.ua || '—'}</code>
        </Field>
      </dl>

      <div className="mb-2 flex items-baseline justify-between">
        <h2 className="text-sm font-semibold uppercase tracking-wide text-slate-500">
          Captured body / log
        </h2>
        {data.truncated && (
          <span className="text-xs text-amber-700">truncated</span>
        )}
      </div>
      <div className="overflow-hidden rounded-lg border border-slate-200 bg-slate-900">
        {data.has_body || data.body ? (
          <pre className="max-h-[60vh] overflow-auto whitespace-pre-wrap break-words p-4 text-xs text-slate-100">
            {data.body ?? '(body was captured but is no longer retained)'}
          </pre>
        ) : (
          <div className="p-4 text-center text-xs text-slate-400">
            No body captured for this ping.
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
    <div className={wide ? 'col-span-2' : undefined}>
      <dt className="text-xs uppercase tracking-wide text-slate-500">{label}</dt>
      <dd className="mt-0.5 text-slate-900">{children}</dd>
    </div>
  )
}

function TypeBadge({ ping }: { ping: PingDetail }) {
  const label =
    ping.type === 'exitstatus' && ping.exitstatus != null
      ? `exit ${ping.exitstatus}`
      : ping.type
  const tone =
    ping.type === 'fail' ||
    (ping.type === 'exitstatus' && (ping.exitstatus ?? 0) !== 0)
      ? 'bg-rose-100 text-rose-800'
      : ping.type === 'success' ||
          (ping.type === 'exitstatus' && ping.exitstatus === 0)
        ? 'bg-emerald-100 text-emerald-800'
        : ping.type === 'start'
          ? 'bg-sky-100 text-sky-800'
          : 'bg-slate-100 text-slate-700'
  return (
    <span
      className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${tone}`}
    >
      {label}
    </span>
  )
}

// formatExact renders the date with nanosecond precision recovered from
// the path parameter. The /pings/ list response truncates to seconds, so
// the URL identifier is the only place full precision survives.
function formatExact(iso: string, pingId: string): string {
  const nanos = Number(pingId)
  if (!Number.isFinite(nanos)) return iso
  const ms = Math.floor(nanos / 1_000_000)
  const sub = String(nanos % 1_000_000_000).padStart(9, '0')
  const d = new Date(ms)
  if (Number.isNaN(d.getTime())) return iso
  const human = d.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
  return `${human} .${sub} (${d.toISOString()})`
}

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <main className="flex min-h-screen items-center justify-center text-slate-500">
      {children}
    </main>
  )
}
