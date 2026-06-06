import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useTransitionStream } from './sse'
import { setAuthMode } from './api'

// Minimal EventSource fake — captures listeners so the test can dispatch
// events synchronously, and records close() so cleanup can be asserted.
class FakeEventSource {
  static instances: FakeEventSource[] = []
  url: string
  listeners: Record<string, ((ev: MessageEvent) => void)[]> = {}
  closed = false

  constructor(url: string) {
    this.url = url
    FakeEventSource.instances.push(this)
  }
  addEventListener(name: string, fn: (ev: MessageEvent) => void): void {
    ;(this.listeners[name] ??= []).push(fn)
  }
  removeEventListener(name: string, fn: (ev: MessageEvent) => void): void {
    this.listeners[name] = (this.listeners[name] ?? []).filter((l) => l !== fn)
  }
  close(): void {
    this.closed = true
  }
  dispatch(name: string): void {
    for (const fn of this.listeners[name] ?? []) {
      fn(new MessageEvent(name))
    }
  }
}

function wrapper(qc: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  )
}

describe('useTransitionStream', () => {
  beforeEach(() => {
    FakeEventSource.instances = []
    vi.stubGlobal('EventSource', FakeEventSource)
    setAuthMode('apikey')
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    setAuthMode('apikey')
  })

  it('opens an EventSource to /events with the api_key query parameter', () => {
    const qc = new QueryClient()
    renderHook(() => useTransitionStream('test-key'), { wrapper: wrapper(qc) })

    expect(FakeEventSource.instances).toHaveLength(1)
    expect(FakeEventSource.instances[0].url).toBe('/events?api_key=test-key')
  })

  it('uses access_token in OIDC mode', () => {
    setAuthMode('oidc')
    const qc = new QueryClient()
    renderHook(() => useTransitionStream('jwt.token.here'), { wrapper: wrapper(qc) })

    expect(FakeEventSource.instances[0].url).toBe('/events?access_token=jwt.token.here')
  })

  it('url-encodes the credential', () => {
    const qc = new QueryClient()
    renderHook(() => useTransitionStream('a b/c?d'), { wrapper: wrapper(qc) })

    expect(FakeEventSource.instances[0].url).toBe('/events?api_key=a%20b%2Fc%3Fd')
  })

  it('does not open a stream when the credential is null', () => {
    const qc = new QueryClient()
    renderHook(() => useTransitionStream(null), { wrapper: wrapper(qc) })

    expect(FakeEventSource.instances).toHaveLength(0)
  })

  it('opens an EventSource as soon as the credential changes from null to a value', () => {
    // Covers the OIDC first-mount race: the route effect can fire before
    // TokenBridge has installed the bearer provider; the hook must wake
    // up when the credential becomes available, not silently skip.
    const qc = new QueryClient()
    const { rerender } = renderHook(
      ({ token }: { token: string | null }) => useTransitionStream(token),
      { wrapper: wrapper(qc), initialProps: { token: null } },
    )
    expect(FakeEventSource.instances).toHaveLength(0)

    rerender({ token: 'arrived' })
    expect(FakeEventSource.instances).toHaveLength(1)
    expect(FakeEventSource.instances[0].url).toBe('/events?api_key=arrived')
  })

  it('replaces the EventSource when the credential rotates', () => {
    // Covers OIDC silent token renewal: the old URL embeds a stale token
    // that the server will 401 on reconnect; we must tear it down and
    // open a fresh stream with the new token.
    const qc = new QueryClient()
    const { rerender } = renderHook(
      ({ token }: { token: string | null }) => useTransitionStream(token),
      { wrapper: wrapper(qc), initialProps: { token: 'first' } },
    )
    expect(FakeEventSource.instances).toHaveLength(1)
    const first = FakeEventSource.instances[0]

    rerender({ token: 'second' })
    expect(FakeEventSource.instances).toHaveLength(2)
    expect(first.closed).toBe(true)
    expect(FakeEventSource.instances[1].url).toBe('/events?api_key=second')
  })

  it('invalidates the checks query when a transition event fires', () => {
    const qc = new QueryClient()
    const spy = vi.spyOn(qc, 'invalidateQueries')
    renderHook(() => useTransitionStream('k'), { wrapper: wrapper(qc) })

    FakeEventSource.instances[0].dispatch('transition')

    expect(spy).toHaveBeenCalledWith({ queryKey: ['checks'] })
  })

  it('removes the listener and closes the stream on unmount', () => {
    const qc = new QueryClient()
    const { unmount } = renderHook(() => useTransitionStream('k'), {
      wrapper: wrapper(qc),
    })
    const es = FakeEventSource.instances[0]

    unmount()

    expect(es.closed).toBe(true)
    expect(es.listeners['transition']).toEqual([])
  })
})
