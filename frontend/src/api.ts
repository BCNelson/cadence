// API client for cadence's v3 read endpoints. Wire-compatible with
// Healthchecks.io v3, plus cadence-specific extensions (the v3 spec is
// already documented at the package level in internal/api).

export type CheckStatus = 'new' | 'up' | 'grace' | 'down' | 'paused'

export interface Check {
  slug: string
  name?: string
  tags: string
  status: CheckStatus
  has_open_run: boolean
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

// AuthConfig is the public discovery payload served at /api/v3/auth/config.
// `oidc: null` means the server is API-key-only; an object turns on the
// OIDC path in the SPA.
export interface AuthConfig {
  oidc: {
    issuer: string
    client_id: string
    audience: string
  } | null
}

export async function fetchAuthConfig(): Promise<AuthConfig> {
  try {
    const res = await fetch('/api/v3/auth/config')
    if (!res.ok) return { oidc: null }
    return (await res.json()) as AuthConfig
  } catch {
    // Network error before the daemon is reachable shouldn't break the
    // login screen — fall back to API-key mode.
    return { oidc: null }
  }
}

// --- API-key storage (unchanged behavior in API-key mode) ---

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

// --- Pluggable auth ---
//
// The SPA picks an auth mode once at boot from /api/v3/auth/config and
// installs the matching header/token strategy via setAuthMode + (in OIDC
// mode) setBearerTokenProvider. Code that issues requests does not need
// to know which mode is active.

export type AuthMode = 'apikey' | 'oidc'

let authMode: AuthMode = 'apikey'
let bearerProvider: () => string | null = () => null

export function setAuthMode(mode: AuthMode): void {
  authMode = mode
}

export function getAuthMode(): AuthMode {
  return authMode
}

// setBearerTokenProvider lets the OIDC provider hand us a "give me the
// current token" closure. We avoid storing the token directly so refreshes
// are transparent.
export function setBearerTokenProvider(fn: () => string | null): void {
  bearerProvider = fn
}

function authHeader(): Record<string, string> {
  if (authMode === 'oidc') {
    const t = bearerProvider()
    if (!t) throw new Error('Not signed in')
    return { Authorization: `Bearer ${t}` }
  }
  const k = getApiKey()
  if (!k) throw new Error('No API key configured')
  return { 'X-Api-Key': k }
}

// eventsURL builds the URL for the SSE stream. EventSource cannot set
// headers, so the credential rides as a query parameter in either mode.
// Returns null when no credential is available (caller should skip).
export function eventsURL(): string | null {
  if (authMode === 'oidc') {
    const t = bearerProvider()
    return t ? `/events?access_token=${encodeURIComponent(t)}` : null
  }
  const k = getApiKey()
  return k ? `/events?api_key=${encodeURIComponent(k)}` : null
}

// fetchJson sends an authenticated request and unwraps the JSON body.
// Throws on non-2xx so TanStack Query's error state can surface it.
async function fetchJson<T>(path: string): Promise<T> {
  const res = await fetch(path, { headers: authHeader() })
  if (!res.ok) {
    const body = await res.text()
    throw new Error(`${res.status} ${res.statusText}: ${body}`)
  }
  return res.json() as Promise<T>
}

export function listChecks(): Promise<ListChecksResponse> {
  return fetchJson<ListChecksResponse>('/api/v3/checks/')
}
