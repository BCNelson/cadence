---
title: Ping keys
description: The named ping-key registry and the slug vs UUID auth model.
---

Ping keys are named shared secrets. Checks reference them by `name`, so the same secret can authorize many checks and rotating it is a one-line registry change.

## Schema

```yaml
ping_keys:
  - name: cron
    key: "${env:CADENCE_CRON_KEY}"
  - name: ci
    key: "${env:CADENCE_CI_KEY}"

checks:
  - slug: nightly-backup
    period: 24h
    ping_keys: [cron]          # this check accepts the "cron" key
  - slug: deploy-smoke
    period: 1h
    ping_keys: [cron, ci]      # accepts either
```

Use `${env:...}` or [`extraConfigFiles`](/cadence/install/nixos/#secrets) for real keys; never inline a production secret in a file that gets committed.

## The auth model

cadence is intentionally **many-to-many** — not partitioned by project, not tied to a single owner. The ping URL form determines how auth works:

### Slug form — `/ping/{slug}`

Requires a key. Cadence checks `X-Ping-Key` header first, falling back to `?ping_key=` query parameter. The provided key must be the *secret value* of one of the names listed in the check's `ping_keys`.

A wrong key, an unknown slug, or an unknown key **all return 404**, not 403. The slug namespace stays non-enumerable — clients can't probe to learn what exists.

### UUID form — `/ping/{uuid}`

Two valid cases:

1. The check has `ping_keys: []` (empty list). The check is **open** — any UUID-form ping succeeds without a key. The slug form is rejected for open checks so the slug can't accidentally become a secret.
2. The check has a `uuid:` field pinned in config (`PinnedUUID`). The URL UUID must match the pinned value exactly. The pinned UUID is itself the auth — ping_keys are ignored for this path.

A derived UUID (the default — `UUIDv5(uuid_salt, slug)`) is **not** a credential. It's just a stable identifier. If you want HC.io-style "anyone with the URL can ping," set `ping_keys: []` to make the check open.

## Rotation

To rotate a key:

1. Update the secret value of one `ping_keys` entry.
2. Restart the daemon.

All checks that reference that name now require the new value. No check definitions move; no UUIDs change.
