import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { clearApiKey, getApiKey, listChecks, setApiKey } from './api'

describe('api key storage', () => {
  it('round-trips through localStorage', () => {
    expect(getApiKey()).toBeNull()
    setApiKey('secret')
    expect(getApiKey()).toBe('secret')
    expect(localStorage.getItem('cadence.apiKey')).toBe('secret')
    clearApiKey()
    expect(getApiKey()).toBeNull()
  })
})

describe('listChecks', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('throws if no API key is set', async () => {
    await expect(listChecks()).rejects.toThrow('No API key configured')
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('sends X-Api-Key and returns parsed JSON', async () => {
    setApiKey('k1')
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ checks: [] }), { status: 200 }),
    )

    const result = await listChecks()

    expect(result).toEqual({ checks: [] })
    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/v3/checks/')
    expect((init.headers as Record<string, string>)['X-Api-Key']).toBe('k1')
  })

  it('throws on non-2xx including the status and body', async () => {
    setApiKey('k1')
    fetchMock.mockResolvedValueOnce(
      new Response('bad key', { status: 401, statusText: 'Unauthorized' }),
    )

    await expect(listChecks()).rejects.toThrow(/401 Unauthorized: bad key/)
  })
})
