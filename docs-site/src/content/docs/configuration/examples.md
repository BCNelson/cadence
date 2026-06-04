---
title: Examples
description: Worked configurations covering the common shapes.
---

## Minimal — one check, one webhook

```yaml
server:
  listen: ":8080"
  uuid_salt: "${env:CADENCE_UUID_SALT}"

data_dir: "/var/lib/cadence"

ping_keys:
  - name: cron
    key: "${env:CADENCE_CRON_KEY}"

channels:
  - name: ops
    type: webhook
    url: "${env:OPS_WEBHOOK_URL}"

checks:
  - slug: nightly-backup
    cron: "0 3 * * *"
    grace: 30m
    ping_keys: [cron]
    channels: [ops]
```

## With defaults

```yaml
server:
  listen: ":8080"
  uuid_salt: "${env:CADENCE_UUID_SALT}"

defaults:
  grace: 5m
  ping_keys: [cron]
  channels: [ops]

ping_keys:
  - name: cron
    key: "${env:CADENCE_CRON_KEY}"

channels:
  - name: ops
    type: webhook
    url: "${env:OPS_WEBHOOK_URL}"

checks:
  - { slug: heartbeat-web,        period: 1m }
  - { slug: heartbeat-worker,     period: 1m }
  - { slug: nightly-backup,       cron: "0 3 * * *", grace: 30m }
  - { slug: weekly-prune,         cron: "0 4 * * 0", grace: 2h }
```

The four checks all inherit `grace: 5m` (except `nightly-backup` and `weekly-prune`, which override), `ping_keys: [cron]`, and `channels: [ops]`.

## Layered with imports

`/etc/cadence/base.yaml`:

```yaml
server:
  listen: ":8080"
  uuid_salt: "${env:CADENCE_UUID_SALT}"
data_dir: "/var/lib/cadence"

import:
  - checks/
  - channels.yaml
```

`/etc/cadence/channels.yaml`:

```yaml
channels:
  - name: ops
    type: webhook
    url: "${env:OPS_WEBHOOK_URL}"
```

`/etc/cadence/checks/web.yaml`:

```yaml
defaults:
  channels: [ops]

checks:
  - { slug: web-heartbeat, period: 1m, ping_keys: [cron] }
```

`/etc/cadence/checks/cron.yaml`:

```yaml
checks:
  - { slug: backup,  cron: "0 3 * * *", grace: 30m, channels: [ops], ping_keys: [cron] }
  - { slug: cleanup, cron: "0 4 * * 0", grace: 2h,  channels: [ops], ping_keys: [cron] }
```

`/etc/cadence/secrets.d/00-keys.yaml` (root-owned, mode 0640, cadence group):

```yaml
ping_keys:
  - { name: cron, key: "real-secret-value" }
```

Start with:

```sh
cadence -c /etc/cadence/base.yaml -c /etc/cadence/secrets.d/
```

## Open check (no key required)

```yaml
checks:
  - slug: anonymous-heartbeat
    period: 5m
    ping_keys: []              # explicit empty list = open
    channels: [ops]
```

An open check only accepts UUID-form pings: `curl http://cadence/ping/<uuid>`. The slug form is rejected (so the slug can't accidentally become a secret). See [ping keys](/cadence/configuration/ping-keys/) for the full rules.

## Pinned UUID for migration

```yaml
checks:
  - slug: migrated-from-hc-io
    period: 1h
    uuid: "11111111-2222-3333-4444-555555555555"
    ping_keys: []              # the pinned UUID is itself the auth
```

Useful when migrating from Healthchecks.io — the cron jobs that already point at `hc-ping.com/<uuid>` can be repointed at `cadence/ping/<uuid>` without changing the UUID.
