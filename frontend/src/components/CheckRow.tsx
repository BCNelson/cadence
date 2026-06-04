import type { Check } from '../api'
import { StatusBadge } from './StatusBadge'

// formatRelative renders an ISO timestamp as "5m ago" / "in 12s" etc.
// Falls back to the raw string if parsing fails. Returns "—" for null.
function formatRelative(iso: string | null | undefined): string {
  if (!iso) return '—'
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return iso
  const delta = (t - Date.now()) / 1000
  return formatDelta(delta)
}

function formatDelta(delta: number): string {
  const abs = Math.abs(delta)
  let value: number
  let unit: string
  if (abs < 60) {
    value = Math.round(abs)
    unit = 's'
  } else if (abs < 3600) {
    value = Math.round(abs / 60)
    unit = 'm'
  } else if (abs < 86400) {
    value = Math.round(abs / 3600)
    unit = 'h'
  } else {
    value = Math.round(abs / 86400)
    unit = 'd'
  }
  return delta < 0 ? `${value}${unit} ago` : `in ${value}${unit}`
}

export function CheckRow({ check }: { check: Check }) {
  return (
    <tr className="border-b border-slate-200 last:border-0 hover:bg-slate-50">
      <td className="py-3 pl-4 pr-2">
        <StatusBadge status={check.status} />
        {check.started && (
          <span className="ml-2 inline-flex items-center rounded-full bg-sky-100 px-1.5 py-0.5 text-[10px] font-medium text-sky-800">
            RUNNING
          </span>
        )}
      </td>
      <td className="py-3 px-2">
        <div className="font-medium text-slate-900">{check.name ?? check.slug}</div>
        <div className="text-xs text-slate-500">{check.slug}</div>
      </td>
      <td className="py-3 px-2 text-sm text-slate-600">
        {check.schedule ? (
          <code className="rounded bg-slate-100 px-1 py-0.5 text-xs">{check.schedule}</code>
        ) : check.timeout ? (
          <span>every {Math.round(check.timeout / 60)}m</span>
        ) : (
          <span className="text-slate-400">—</span>
        )}
      </td>
      <td className="py-3 px-2 text-sm tabular-nums text-slate-600">
        {formatRelative(check.last_ping)}
      </td>
      <td className="py-3 px-2 text-sm tabular-nums text-slate-600">
        {formatRelative(check.next_ping)}
      </td>
      <td className="py-3 px-2 text-xs text-slate-500">
        {check.tags
          .split(' ')
          .filter(Boolean)
          .map((tag) => (
            <span key={tag} className="mr-1 inline-block rounded bg-slate-100 px-1.5 py-0.5">
              {tag}
            </span>
          ))}
      </td>
      <td className="py-3 px-4 text-right text-xs tabular-nums text-slate-400">
        {check.n_pings}
      </td>
    </tr>
  )
}
