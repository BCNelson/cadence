---
title: Channels
description: Notification destinations referenced by checks.
---

A channel is where a notification goes. v1 ships one transport type, `webhook`; more can be added without disturbing the existing schema.

## Schema

```yaml
channels:
  - name: ops                  # required, referenced by checks
    type: webhook              # required, currently only "webhook"
    url: "https://hooks.example.com/cadence"
    method: POST               # optional, defaults to POST
    headers:                   # optional map
      Authorization: "Bearer ${env:OPS_TOKEN}"
      X-Source: "cadence"
```

## Fields

- **`name`** — referenced by checks via `channels: [name]`. Unique across the resolved config.
- **`type`** — `"webhook"`. Only value accepted in v1.
- **`url`** — destination URL. Use `${env:NAME}` for secrets in the path or query string.
- **`method`** — any of `GET`, `POST`, `PUT`, `PATCH`, `DELETE`. Defaults to `POST`.
- **`headers`** — extra HTTP headers sent with every notification.

cadence sets `Content-Type: application/json` and `User-Agent: cadence/v1` automatically; custom `headers` may override them.

## Dispatch behavior

When a check transitions into `down` or back to `up`:

- The same canonical payload is sent to **every** channel listed on the check.
- All channels for one transition fire concurrently. A 5xx on one channel does **not** block the others.
- Per-channel errors are aggregated and logged. cadence does not retry — the receiving system handles its own queue / retry semantics.
- The HTTP client has a 10s timeout.

The payload shape is documented under [webhooks](/cadence/alerting/webhooks/).

## Validation

A check referencing a channel name that isn't declared aborts startup. The same goes for `defaults.channels`. Misspellings fail loud, not silent.
