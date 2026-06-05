import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { PingDetailPage } from './checks.$slug.pings.$pingId'
import { withRouterReady } from '../test-utils'
import * as api from '../api'
import type { PingDetail } from '../api'

function mkDetail(over: Partial<PingDetail> = {}): PingDetail {
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

describe('PingDetailPage', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders the body when one was captured', async () => {
    vi.spyOn(api, 'getPing').mockResolvedValue(
      mkDetail({
        type: 'fail',
        body: 'oh no, the deploy script crashed',
        body_size: 31,
        has_body: true,
        remote_addr: '10.0.0.5',
        ua: 'curl/8',
      }),
    )

    render(
      await wrap(<PingDetailPage slug="web-cron" pingId="1700000000000000000" />),
    )

    await waitFor(() =>
      expect(screen.getByText(/oh no, the deploy script crashed/)).toBeInTheDocument(),
    )
    expect(screen.getByText('10.0.0.5')).toBeInTheDocument()
    expect(screen.getByText('curl/8')).toBeInTheDocument()
    expect(screen.getByText('31 bytes')).toBeInTheDocument()
  })

  it('shows a placeholder when no body was captured', async () => {
    vi.spyOn(api, 'getPing').mockResolvedValue(mkDetail({ has_body: false }))

    render(
      await wrap(<PingDetailPage slug="web-cron" pingId="1700000000000000000" />),
    )

    await waitFor(() =>
      expect(screen.getByText(/No body captured/i)).toBeInTheDocument(),
    )
  })

  it('renders exit-status as part of the type badge', async () => {
    vi.spyOn(api, 'getPing').mockResolvedValue(
      mkDetail({ type: 'exitstatus', exitstatus: 7 }),
    )

    render(
      await wrap(<PingDetailPage slug="web-cron" pingId="1700000000000000000" />),
    )

    await waitFor(() => expect(screen.getByText('exit 7')).toBeInTheDocument())
  })

  it('surfaces an error message when the fetch fails', async () => {
    vi.spyOn(api, 'getPing').mockRejectedValue(new Error('gone'))
    render(
      await wrap(<PingDetailPage slug="web-cron" pingId="1700000000000000000" />),
    )
    await waitFor(() =>
      expect(screen.getByText(/Couldn't load ping/i)).toBeInTheDocument(),
    )
    expect(screen.getByText(/gone/)).toBeInTheDocument()
  })
})
