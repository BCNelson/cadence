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

// listChecksByTag narrows the check list to entries bearing every listed
// tag (AND semantics, matching HC.io and the server-side filter).
export function listChecksByTag(tags: string[]): Promise<ListChecksResponse> {
  if (tags.length === 0) return listChecks()
  const qs = tags.map((t) => `tag=${encodeURIComponent(t)}`).join('&')
  return fetchJson<ListChecksResponse>(`/api/v3/checks/?${qs}`)
}

// getCheck fetches a single check by slug, unique_key, or UUID. The SPA
// uses the slug for routing (/checks/$slug) since it's user-facing.
export function getCheck(id: string): Promise<Check> {
  return fetchJson<Check>(`/api/v3/checks/${encodeURIComponent(id)}`)
}

// Ping mirrors /api/v3/checks/{id}/pings/ rows. `id` is the unix-nanosecond
// timestamp used as the URL identifier in detail lookups. `type` is one of
// the PingKind values plus "exitstatus" (the HC.io spelling for
// numeric-exit pings).
export interface Ping {
  id: string
  type: string
  date: string
  exitstatus?: number | null
  body_size?: number
  truncated?: boolean
  has_body?: boolean
  remote_addr?: string
  ua?: string
}

export interface ListPingsResponse {
  pings: Ping[]
}

export function getPingsForCheck(id: string): Promise<ListPingsResponse> {
  return fetchJson<ListPingsResponse>(`/api/v3/checks/${encodeURIComponent(id)}/pings/`)
}

// PingDetail extends Ping with the captured body (when one was stored).
// Returned by the per-ping detail endpoint.
export interface PingDetail extends Ping {
  body?: string
}

export function getPing(checkID: string, pingID: string): Promise<PingDetail> {
  return fetchJson<PingDetail>(
    `/api/v3/checks/${encodeURIComponent(checkID)}/pings/${encodeURIComponent(pingID)}`,
  )
}

// TagSummary is one row of the /api/v3/tags/ index. `checks` is a slug
// list — fetch /api/v3/tags/{name} (or /api/v3/checks/?tag=name) for
// full check views.
export interface TagSummary {
  name: string
  status: CheckStatus
  n_checks: number
  checks: string[]
}

export interface ListTagsResponse {
  tags: TagSummary[]
}

export interface TagDetail {
  name: string
  status: CheckStatus
  checks: Check[]
}

export function listTags(): Promise<ListTagsResponse> {
  return fetchJson<ListTagsResponse>('/api/v3/tags/')
}

export function getTag(name: string): Promise<TagDetail> {
  return fetchJson<TagDetail>(`/api/v3/tags/${encodeURIComponent(name)}`)
}

// Status rank — worst wins, paused excluded. Mirrors the server-side
// rollup in internal/api/tags.go; kept in sync so derived UI matches the
// canonical view from /api/v3/tags/.
const statusRank: Record<CheckStatus, number> = {
  down: 4,
  grace: 3,
  new: 2,
  up: 1,
  paused: 0,
}

// rollupStatus collapses a set of checks to one combined status using the
// same worst-wins rule as the backend. Returns null for an empty input.
export function rollupStatus(checks: Check[]): CheckStatus | null {
  if (checks.length === 0) return null
  let worst: CheckStatus | null = null
  let worstRank = -1
  let sawActive = false
  for (const c of checks) {
    if (c.status === 'paused') continue
    sawActive = true
    const r = statusRank[c.status]
    if (r > worstRank) {
      worstRank = r
      worst = c.status
    }
  }
  return sawActive ? worst : 'paused'
}

// uniqueTags pulls every distinct tag from a list of checks. Tag strings
// arrive space-separated (HC.io convention); this normalizes into a
// sorted array.
export function uniqueTags(checks: Check[]): string[] {
  const set = new Set<string>()
  for (const c of checks) {
    for (const t of c.tags.split(' ')) {
      if (t) set.add(t)
    }
  }
  return [...set].sort()
}
