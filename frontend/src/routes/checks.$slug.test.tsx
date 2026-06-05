import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { CheckDetail } from './checks.$slug'
import { withRouterReady } from '../test-utils'
import * as api from '../api'
import type { Check, Ping } from '../api'

class FakeEventSource {
  url: string
  constructor(url: string) {
    this.url = url
  }
  addEventListener(): void {}
  removeEventListener(): void {}
  close(): void {}
}

function mkCheck(over: Partial<Check> = {}): Check {
  return {
    slug: 'web-cron',
    name: 'Web cron',
    tags: 'prod web',
    status: 'up',
    has_open_run: false,
    grace: 60,
    n_pings: 0,
    ...over,
  }
}

function mkPing(over: Partial<Ping> = {}): Ping {
  return {
    id: '1700000000000000000',
    type: 'success',
    date: '2026-06-03T11:59:30Z',
    ...over,
  }
}

async function wrap(children: ReactNode) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return (
    <QueryClientProvider client={qc}>{await withRouterReady(children)}</QueryClientProvider>
  )
}

describe('CheckDetail', () => {
  beforeEach(() => {
    vi.stubGlobal('EventSource', FakeEventSource)
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('renders check metadata and a ping history table', async () => {
    vi.spyOn(api, 'getCheck').mockResolvedValue(
      mkCheck({
        last_ping: '2026-06-03T11:59:30Z',
        next_ping: '2026-06-03T12:30:00Z',
        n_pings: 3,
        schedule: '*/5 * * * *',
      }),
    )
    vi.spyOn(api, 'getPingsForCheck').mockResolvedValue({
      pings: [
        mkPing({ id: '300', type: 'success', date: '2026-06-03T11:59:30Z' }),
        mkPing({ id: '200', type: 'fail', date: '2026-06-03T11:50:00Z' }),
        mkPing({ id: '100', type: 'start', date: '2026-06-03T11:45:00Z' }),
      ],
    })

    render(await wrap(<CheckDetail slug="web-cron" />))

    await waitFor(() =>
      expect(screen.getByText('Web cron')).toBeInTheDocument(),
    )
    expect(screen.getByText('*/5 * * * *')).toBeInTheDocument()
    // Ping rows: success / fail / start badges all render.
    expect(screen.getByText('success')).toBeInTheDocument()
    expect(screen.getByText('fail')).toBeInTheDocument()
    expect(screen.getByText('start')).toBeInTheDocument()
  })

  it('computes duration by pairing each closing ping with the preceding start', async () => {
    vi.spyOn(api, 'getCheck').mockResolvedValue(mkCheck())
    // Newest-first order: success at +30s, then its start.
    vi.spyOn(api, 'getPingsForCheck').mockResolvedValue({
      pings: [
        mkPing({ id: '2', type: 'success', date: '2026-06-03T12:00:30Z' }),
        mkPing({ id: '1', type: 'start', date: '2026-06-03T12:00:00Z' }),
      ],
    })

    render(await wrap(<CheckDetail slug="web-cron" />))
    await waitFor(() => expect(screen.getByText('Web cron')).toBeInTheDocument())
    // 30s wall-clock gap between start and success → "30.0s".
    expect(screen.getByText('30.0s')).toBeInTheDocument()
  })

  it('shows an empty-state message when no pings have been recorded', async () => {
    vi.spyOn(api, 'getCheck').mockResolvedValue(mkCheck())
    vi.spyOn(api, 'getPingsForCheck').mockResolvedValue({ pings: [] })

    render(await wrap(<CheckDetail slug="web-cron" />))
    await waitFor(() =>
      expect(screen.getByText(/No pings recorded yet/i)).toBeInTheDocument(),
    )
  })

  it('surfaces a load error when the check fetch fails', async () => {
    vi.spyOn(api, 'getCheck').mockRejectedValue(new Error('nope'))
    vi.spyOn(api, 'getPingsForCheck').mockResolvedValue({ pings: [] })
    render(await wrap(<CheckDetail slug="web-cron" />))
    await waitFor(() =>
      expect(screen.getByText(/Couldn't load check/i)).toBeInTheDocument(),
    )
    expect(screen.getByText(/nope/)).toBeInTheDocument()
  })
})
