---
title: Webhooks
description: Channel schema, canonical payload, fire-once semantics.
---

cadence fires webhooks on two transitions: into `down` (the check missed its deadline plus grace) and back to `up` (a successful ping arrived after being down). v1 supports only `type: webhook` channels.

## Fire-once semantics

Cadence sends **one** alert on entry into `down` and **one** alert on recovery. There is no repeat-while-down. This is deliberate — it avoids alert storms and forces upstream channels (your incident manager, your chat bridge) to own re-notification policy.

A check that flaps from `up → down → up → down` will fire two `down` alerts and two `recover` alerts — one per genuine transition.

## Canonical payload

The same JSON shape is sent for both `down` and `recover` events. Channels can branch on `event`.

```json
{
  "event": "down",
  "check": {
    "slug":   "nightly-backup",
    "name":   "Nightly backup",
    "uuid":   "f47ac10b-58cc-5372-a567-0e02b2c3d479",
    "tags":   ["backup", "nightly"],
    "status": "down"
  },
  "from":   "up",
  "to":     "down",
  "at":     "2026-06-03T04:00:14Z",
  "reason": "no ping for 24h30m"
}
```

| Field | Type | Notes |
|---|---|---|
| `event` | string | `"down"` or `"recover"`. |
| `check.slug` | string | Stable across renames? No — slug renames produce new UUIDs. |
| `check.name` | string | Omitted if not set in config. |
| `check.uuid` | string (UUID) | Stable across restarts (derived from slug + uuid_salt, or pinned). |
| `check.tags` | array | Omitted if empty. |
| `check.status` | string | Matches `to`. Surfaced inside `check` for HC.io-style consumers that only read the nested object. |
| `from` | string | Previous state. |
| `to` | string | New state — `down` or `up`. |
| `at` | string | RFC3339 UTC timestamp of the transition. |
| `reason` | string | Engine-supplied human-readable reason; omitted when empty. |

This is the **only** payload shape in v1. Per-channel templating is a future enhancement.

## HTTP delivery

- `POST` by default; override with `method:` on the channel.
- `Content-Type: application/json; charset=utf-8` is set automatically.
- `User-Agent: cadence/v1` is set automatically.
- Custom `headers:` from the channel are added (and may override the defaults).
- 10-second client timeout.
- Non-2xx responses are logged as a per-channel error.
- **No retry.** The receiving system handles its own queue / re-delivery semantics.

## Channels that need a body shape adapter

The canonical payload is fine for ops endpoints you write yourself. For third-party services (Slack, Discord, PagerDuty, etc.) that expect a specific JSON shape, point the channel at a small adapter (a Cloudflare Worker, an AWS Lambda, or a few lines in your existing reverse proxy) that reformats the payload. v1 deliberately doesn't ship per-service templates — the stable contract is the canonical payload.

See [channels](/cadence/configuration/channels/) for the channel schema and [examples](/cadence/configuration/examples/) for end-to-end YAML.
