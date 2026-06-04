---
title: Quickstart
description: Run cadence locally with a minimal config in under a minute.
---

This walks through the bare minimum: one check, one ping, one webhook.

## 1. Write a config

Create `cadence.yaml`:

```yaml
server:
  listen: "127.0.0.1:8080"
  uuid_salt: "change-me-to-a-random-string"

data_dir: "./data"

ping_keys:
  - name: cron
    key: "demo-ping-key"

channels:
  - name: ops
    type: webhook
    url: "https://example.invalid/hooks/cadence"

checks:
  - slug: nightly-backup
    period: 24h
    grace: 30m
    ping_keys: [cron]
    channels: [ops]
```

## 2. Run it

From the repo root:

```sh
just build
./cadence -c cadence.yaml
```

The dashboard is at <http://127.0.0.1:8080>.

## 3. Ping it

```sh
curl -fsS -H 'X-Ping-Key: demo-ping-key' \
  http://127.0.0.1:8080/ping/nightly-backup
```

You should see `OK` come back and the check flip to `up` in the dashboard.

## 4. What happens when it's late

If no ping arrives in 24h, the check moves through `up → late → down` (the `late` phase lasts `grace = 30m`, and is reported as `grace` in the v3 API for HC.io compatibility). On entry to `down`, cadence POSTs the canonical [alert payload](/cadence/alerting/webhooks/) to every channel listed on the check — once. The next successful ping fires the recovery alert and the cycle resets.

## Where to go next

- [Configuration reference](/cadence/configuration/overview/) — every top-level key with full semantics.
- [NixOS module](/cadence/install/nixos/) — the production install path. Hardened systemd unit, typed config, assertion-checked references.
- [Ping API](/cadence/api/ping/) — slug vs UUID forms, `/start` and `/fail`, body capture, the 404-not-403 rule.
