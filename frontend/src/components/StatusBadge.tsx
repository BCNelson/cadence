import type { CheckStatus } from '../api'

// Status colors map to severity, not to brand. Operators glance at this
// many times an hour; high-contrast wins over aesthetic.
const styles: Record<CheckStatus, string> = {
  new: 'bg-slate-200 text-slate-700',
  up: 'bg-emerald-100 text-emerald-800',
  grace: 'bg-amber-100 text-amber-800',
  down: 'bg-rose-100 text-rose-800',
  paused: 'bg-slate-100 text-slate-500',
}

const labels: Record<CheckStatus, string> = {
  new: 'New',
  up: 'Up',
  grace: 'Late',
  down: 'Down',
  paused: 'Paused',
}

export function StatusBadge({ status }: { status: CheckStatus }) {
  return (
    <span
      className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${styles[status]}`}
    >
      {labels[status]}
    </span>
  )
}
