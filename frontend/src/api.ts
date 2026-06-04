// API client for cadence's v3 read endpoints. Wire-compatible with
// Healthchecks.io v3, plus cadence-specific extensions (the v3 spec is
// already documented at the package level in internal/api).

export type CheckStatus = 'new' | 'up' | 'grace' | 'down' | 'paused'

export interface Check {
  slug: string
  name?: string
  tags: string
  status: CheckStatus
  started: boolean
  last_ping?: string | null
  next_ping?: string | null
  grace: number
  schedule?: string
  timezone?: string
  timeout?: number
  n_pings: number
  ping_url?: string
  channels?: string
  unique_key?: string
}

export interface ListChecksResponse {
  checks: Check[]
}

const API_KEY_STORAGE = 'cadence.apiKey'

export function getApiKey(): string | null {
  return localStorage.getItem(API_KEY_STORAGE)
}

export function setApiKey(key: string): void {
  localStorage.setItem(API_KEY_STORAGE, key)
}

export function clearApiKey(): void {
  localStorage.removeItem(API_KEY_STORAGE)
}

// fetchJson sends an authenticated request and unwraps the JSON body.
// Throws on non-2xx so TanStack Query's error state can surface it.
async function fetchJson<T>(path: string): Promise<T> {
  const key = getApiKey()
  if (!key) throw new Error('No API key configured')
  const res = await fetch(path, {
    headers: { 'X-Api-Key': key },
  })
  if (!res.ok) {
    const body = await res.text()
    throw new Error(`${res.status} ${res.statusText}: ${body}`)
  }
  return res.json() as Promise<T>
}

export function listChecks(): Promise<ListChecksResponse> {
  return fetchJson<ListChecksResponse>('/api/v3/checks/')
}
