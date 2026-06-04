---
title: Management API v3
description: Read endpoints for checks. Writes return 409 — YAML is the source of truth.
---

The Management API is HC.io v3 wire-compatible on the **read** side. Every write endpoint exists, authenticates, and then returns `409 Conflict` — checks live in the YAML config, not in the database.

## Authentication

Send `X-Api-Key: <key>` on every request. Keys are declared under `server.api_keys.read_write` or `server.api_keys.read_only`. A missing or unknown key returns `401 Unauthorized` with a JSON error.

The HC.io convention of putting `api_key` in a JSON body is not honored in v1 — the header form is the only path.

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

## checkView fields

| Field | Notes |
|---|---|
| `slug` | Always present. |
| `name` | Omitted if not set in config. |
| `tags` | **Space-separated string** (HC.io convention), not an array. |
| `status` | `new`, `up`, `grace`, `down`, `paused`. The internal `late` status is reported as `grace` for wire compatibility. |
| `started` | True between `/start` and the next closing ping. |
| `last_ping` | RFC3339 UTC. Omitted if the check has never pinged. |
| `next_ping` | RFC3339 UTC. Computed from cron or `last_ping + period`. Omitted if no last ping. |
| `grace` | Grace seconds (integer). |
| `schedule` | Cron expression, only set for cron-scheduled checks. |
| `timezone` | Always `"UTC"` when `schedule` is set — cron is evaluated in UTC. |
| `timeout` | Period seconds, only set for period-scheduled checks. |
| `n_pings` | Total stored ping count (capped by retention). |

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
