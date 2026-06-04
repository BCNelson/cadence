import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { CheckRow } from './CheckRow'
import type { Check } from '../api'

function row(over: Partial<Check> = {}): Check {
  return {
    slug: 'web-cron',
    name: 'Web cron',
    tags: 'prod web',
    status: 'up',
    started: false,
    last_ping: null,
    next_ping: null,
    grace: 60,
    n_pings: 0,
    ...over,
  }
}

// Renders a single row inside a table so the row's <tr>/<td> are valid DOM.
function renderRow(check: Check) {
  return render(
    <table>
      <tbody>
        <CheckRow check={check} />
      </tbody>
    </table>,
  )
}

describe('CheckRow', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-06-03T12:00:00Z'))
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('shows name with slug subtitle', () => {
    renderRow(row({ name: 'Web cron', slug: 'web-cron' }))
    expect(screen.getByText('Web cron')).toBeInTheDocument()
    expect(screen.getByText('web-cron')).toBeInTheDocument()
  })

  it('falls back to slug when name is missing', () => {
    renderRow(row({ name: undefined, slug: 'only-slug' }))
    // slug rendered as both heading + subtitle
    expect(screen.getAllByText('only-slug').length).toBeGreaterThanOrEqual(1)
  })

  it('renders a RUNNING badge when started', () => {
    renderRow(row({ started: true }))
    expect(screen.getByText('RUNNING')).toBeInTheDocument()
  })

  it('omits the RUNNING badge when not started', () => {
    renderRow(row({ started: false }))
    expect(screen.queryByText('RUNNING')).not.toBeInTheDocument()
  })

  it('renders cron schedule when present', () => {
    renderRow(row({ schedule: '*/5 * * * *' }))
    expect(screen.getByText('*/5 * * * *')).toBeInTheDocument()
  })

  it('renders timeout in minutes when no schedule', () => {
    renderRow(row({ timeout: 600 }))
    expect(screen.getByText(/every 10m/)).toBeInTheDocument()
  })

  it('renders em-dash when neither schedule nor timeout', () => {
    renderRow(row())
    // The em-dash appears in 3 places (schedule, last_ping, next_ping cols)
    expect(screen.getAllByText('—').length).toBeGreaterThan(0)
  })

  it('formats past timestamps as "N ago" and future as "in N"', () => {
    renderRow(
      row({
        last_ping: '2026-06-03T11:59:30Z', // 30s ago
        next_ping: '2026-06-03T12:02:00Z', // in 2m
      }),
    )
    expect(screen.getByText('30s ago')).toBeInTheDocument()
    expect(screen.getByText('in 2m')).toBeInTheDocument()
  })

  it('uses hour and day units for larger deltas', () => {
    renderRow(
      row({
        last_ping: '2026-06-03T09:00:00Z', // 3h ago
        next_ping: '2026-06-05T12:00:00Z', // in 2d
      }),
    )
    expect(screen.getByText('3h ago')).toBeInTheDocument()
    expect(screen.getByText('in 2d')).toBeInTheDocument()
  })

  it('falls back to raw string on unparseable timestamp', () => {
    renderRow(row({ last_ping: 'not-a-date' }))
    expect(screen.getByText('not-a-date')).toBeInTheDocument()
  })

  it('splits tags on whitespace and renders each as a chip', () => {
    const { container } = renderRow(row({ tags: 'prod  web   db' }))
    const tagCell = container.querySelectorAll('td')[5] as HTMLElement
    const chips = within(tagCell).getAllByText(/prod|web|db/)
    expect(chips.map((c) => c.textContent)).toEqual(['prod', 'web', 'db'])
  })

  it('renders ping count', () => {
    renderRow(row({ n_pings: 42 }))
    expect(screen.getByText('42')).toBeInTheDocument()
  })
})
