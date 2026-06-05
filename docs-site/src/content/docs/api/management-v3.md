---
title: Management API v3
description: Read endpoints for checks. Writes return 409 — YAML is the source of truth.
---

The Management API is HC.io v3 wire-compatible on the **read** side. Every write endpoint exists, authenticates, and then returns `409 Conflict` — checks live in the YAML config, not in the database.

## Authentication

Send `X-Api-Key: <key>` on every request. Keys are declared under `server.api_keys.read_write` or `server.api_keys.read_only`. A missing or unknown key returns `401 Unauthorized` with a JSON error.

For clients that can't set request headers — notably browser `EventSource` for the [SSE stream](#sse-stream) — the key may also be passed as an `api_key` query parameter (`?api_key=<key>`). The header wins if both are set. The HC.io convention of putting `api_key` in a JSON body is not honored in v1.

Be aware that query-string keys can show up in HTTP access logs and browser history. Prefer the header form whenever possible; reserve `?api_key=` for `EventSource` and similar header-less callers.

## SSE stream

`GET /events` opens a Server-Sent Events stream that emits a `transition` event for every check state change. The endpoint shares the management API key allow-list (any valid key is accepted) and supports both the header and `?api_key=` query forms.

## Read endpoints

### `GET /api/v3/checks/`

List all checks.

```json
{
  "checks": [
    {
      "slug": "nightly-backup",
      "name": "Nightly backup",
      "tags": "backup nightly",
      "status": "up",
      "started": false,
      "last_ping": "2026-06-03T03:00:01Z",
      "next_ping": "2026-06-04T03:00:01Z",
      "grace": 1800,
      "schedule": "0 3 * * *",
      "timezone": "UTC",
      "n_pings": 17
    }
  ]
}
```

### `GET /api/v3/checks/{id}`

Get one check. `{id}` is the UUID or the `unique_key` (SHA-1-truncated UUID returned to read-only clients). Slug lookups are deliberately not supported here — slugs would let public clients enumerate the namespace.

Returns the same `checkView` shape as above (not wrapped in `checks`).

### `GET /api/v3/checks/{id}/flips/`

Recent state transitions for a check, newest first. Cadence collapses its richer state machine onto HC.io's binary up/down view: any transition into `up` is reported as `up: 1`, anything else is `up: 0`. Internal transitions that don't move on the up/down axis (e.g. `up → late` while `late` still reads as down) are filtered out.

```json
[
  { "timestamp": "2026-06-03T03:00:01Z", "up": 1 },
  { "timestamp": "2026-06-02T03:11:14Z", "up": 0 }
]
```

The response is a bare JSON array (no wrapping object) to match HC.io.

### `GET /api/v3/checks/{id}/pings/`

Recent inbound ping history for a check, newest first.

```json
{
  "pings": [
    {
      "type": "exitstatus",
      "date": "2026-06-03T03:00:01Z",
      "exitstatus": 0,
      "body_size": 142,
      "remote_addr": "10.0.0.7",
      "ua": "curl/8.1.0"
    }
  ]
}
```

`type` values: `success`, `start`, `fail`, `log`, `exitstatus`. `exitstatus` is only present when `type == "exitstatus"`. Body content is not returned inline — it is captured server-side and exposed via the dashboard.

### `GET /api/v3/channels/`

Notification channels declared in config. **Requires a read-write key** — channels carry webhook URLs and similar transport secrets, so read-only viewers don't see the list.

```json
{
  "channels": [
    { "id": "hook", "name": "hook", "kind": "webhook" }
  ]
}
```

Transport details (URL, method, headers) are omitted because they often embed secrets. HC.io's own `/channels/` response also omits transport details.

### `GET /api/v3/badges/`

Per-check badge URL bundle. **Public** (no auth) — badges are designed for README embedding.

```json
{
  "badges": {
    "nightly-backup": {
      "svg":     "https://cadence.example.com/badge/abc123.svg",
      "svg3":    "https://cadence.example.com/badge/abc123-3.svg",
      "json":    "https://cadence.example.com/badge/abc123.json",
      "json3":   "https://cadence.example.com/badge/abc123-3.json",
      "shields": "https://cadence.example.com/badge/abc123.shields"
    }
  }
}
```

The badge URLs themselves dereference to the badge render handler. **The render handler (`/badge/{key}.svg`) is not implemented yet** — this endpoint emits the URLs so HC.io clients reading the index work today; rendering follows in a later release.

## checkView fields

| Field | Notes |
|---|---|
| `slug` | Always present. |
| `name` | Omitted if not set in config. |
| `tags` | **Space-separated string** (HC.io convention), not an array. |
| `status` | `new`, `up`, `grace`, `down`, `paused`. The internal `late` status is reported as `grace` for wire compatibility. |
| `has_open_run` | True between `/start` and the next closing ping. (HC.io reports this as `status: "started"`; cadence keeps it on a separate boolean for clarity.) |
| `last_ping` | RFC3339 UTC. Omitted if the check has never pinged. |
| `next_ping` | RFC3339 UTC. Computed from cron or `last_ping + period`. Omitted if no last ping. |
| `last_duration` | Seconds between the most recent `/start` and its closing ping. Omitted if no closed run is in the retained ping window. |
| `grace` | Grace seconds (integer). |
| `schedule` | Cron expression, only set for cron-scheduled checks. |
| `timezone` | Always `"UTC"` when `schedule` is set — cron is evaluated in UTC. |
| `timeout` | Period seconds, only set for period-scheduled checks. |
| `n_pings` | Total stored ping count (capped by retention). |
| `badge_url` | Canonical SVG badge URL. Omitted when `server.base_url` is unset. |

### Read-write keys see additionally

- `ping_url` — built from `server.base_url` if set, otherwise a path-only `/ping/{slug}`.
- `channels` — comma-separated channel names.

### Read-only keys see additionally

- `unique_key` — opaque 6-character hex (SHA-1-truncated UUID). Stable and non-secret; use it in URLs where the full UUID would be too long.

## Write endpoints — all return 409

| Pattern | Response |
|---|---|
| `POST /api/v3/checks/` | `409` — create not supported. |
| `POST /api/v3/checks/{id}` | `409` — update not supported. |
| `DELETE /api/v3/checks/{id}` | `409` — delete not supported. |
| `POST /api/v3/checks/{id}/pause` | `409` — pause not supported. |
| `POST /api/v3/checks/{id}/resume` | `409` — resume not supported. |

The 409 body is:

```json
{ "error": "cadence does not support <op> via API — checks are declared in the YAML config file (the source of truth)" }
```

The endpoints are explicitly registered (rather than 404ing) so clients get a real signal: cadence is rejecting the operation, not failing to route the URL.
