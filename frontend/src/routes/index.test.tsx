import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { Dashboard } from './index'
import * as api from '../api'
import type { Check, ListChecksResponse } from '../api'

class FakeEventSource {
  url: string
  constructor(url: string) {
    this.url = url
  }
  addEventListener(): void {}
  removeEventListener(): void {}
  close(): void {}
}

function mkCheck(over: Partial<Check>): Check {
  return {
    slug: 'x',
    tags: '',
    status: 'up',
    has_open_run: false,
    grace: 60,
    n_pings: 0,
    ...over,
  }
}

function wrap(children: ReactNode) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>
}

describe('Dashboard', () => {
  beforeEach(() => {
    vi.stubGlobal('EventSource', FakeEventSource)
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('shows loading then renders rows sorted by status then slug', async () => {
    const checks: Check[] = [
      mkCheck({ slug: 'a-up', name: 'A up', status: 'up' }),
      mkCheck({ slug: 'b-down', name: 'B down', status: 'down' }),
      mkCheck({ slug: 'c-grace', name: 'C grace', status: 'grace' }),
      mkCheck({ slug: 'a-down', name: 'A down', status: 'down' }),
    ]
    vi.spyOn(api, 'listChecks').mockResolvedValue({
      checks,
    } satisfies ListChecksResponse)

    render(wrap(<Dashboard />))
    expect(screen.getByText(/loading/i)).toBeInTheDocument()

    await waitFor(() => expect(screen.getByText('4 checks')).toBeInTheDocument())

    // Pull the slug subtitles (rendered in the second column) in document
    // order to check sort: down < grace < up, then slug tiebreaker.
    const subtitles = screen
      .getAllByText(/^[abc]-(up|down|grace)$/)
      .map((el) => el.textContent)
    expect(subtitles).toEqual(['a-down', 'b-down', 'c-grace', 'a-up'])
  })

  it('renders the empty state when no checks are returned', async () => {
    vi.spyOn(api, 'listChecks').mockResolvedValue({ checks: [] })
    render(wrap(<Dashboard />))
    await waitFor(() =>
      expect(
        screen.getByText(/No checks declared\. Add them in your config file\./i),
      ).toBeInTheDocument(),
    )
    expect(screen.getByText('0 checks')).toBeInTheDocument()
  })

  it('shows an error banner with the message when the query fails', async () => {
    vi.spyOn(api, 'listChecks').mockRejectedValue(new Error('boom'))
    render(wrap(<Dashboard />))
    await waitFor(() => expect(screen.getByText(/Couldn't load checks/)).toBeInTheDocument())
    expect(screen.getByText(/boom/)).toBeInTheDocument()
  })

  it('sign-out clears the API key and reloads', async () => {
    api.setApiKey('to-clear')
    vi.spyOn(api, 'listChecks').mockResolvedValue({ checks: [] })
    const reload = vi.fn()
    // window.location is non-configurable in some envs; replace just .reload.
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { ...window.location, reload },
    })

    render(wrap(<Dashboard />))
    await waitFor(() => expect(screen.getByText('0 checks')).toBeInTheDocument())

    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /sign out/i }))

    expect(api.getApiKey()).toBeNull()
    expect(reload).toHaveBeenCalled()
  })

  it('reset-api-key in the error banner clears the key and reloads', async () => {
    api.setApiKey('to-clear')
    vi.spyOn(api, 'listChecks').mockRejectedValue(new Error('boom'))
    const reload = vi.fn()
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { ...window.location, reload },
    })

    render(wrap(<Dashboard />))
    await waitFor(() => expect(screen.getByText(/Couldn't load checks/)).toBeInTheDocument())

    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /reset api key/i }))

    expect(api.getApiKey()).toBeNull()
    expect(reload).toHaveBeenCalled()
  })
})
