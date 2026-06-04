import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useTransitionStream } from './sse'

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
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('opens an EventSource to /events', () => {
    const qc = new QueryClient()
    renderHook(() => useTransitionStream(), { wrapper: wrapper(qc) })

    expect(FakeEventSource.instances).toHaveLength(1)
    expect(FakeEventSource.instances[0].url).toBe('/events')
  })

  it('invalidates the checks query when a transition event fires', () => {
    const qc = new QueryClient()
    const spy = vi.spyOn(qc, 'invalidateQueries')
    renderHook(() => useTransitionStream(), { wrapper: wrapper(qc) })

    FakeEventSource.instances[0].dispatch('transition')

    expect(spy).toHaveBeenCalledWith({ queryKey: ['checks'] })
  })

  it('removes the listener and closes the stream on unmount', () => {
    const qc = new QueryClient()
    const { unmount } = renderHook(() => useTransitionStream(), {
      wrapper: wrapper(qc),
    })
    const es = FakeEventSource.instances[0]

    unmount()

    expect(es.closed).toBe(true)
    expect(es.listeners['transition']).toEqual([])
  })
})
