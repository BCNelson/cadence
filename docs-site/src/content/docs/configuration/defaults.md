---
title: Defaults
description: Fallback values applied to checks that don't set them.
---

`defaults:` provides fallbacks for fields a check omits. Per-check fields override defaults; "absent" inherits, "set to empty" doesn't.

## Schema

```yaml
defaults:
  grace: 5m
  timeout: 1h
  ping_keys: [cron]
  channels: [ops]
```

## Semantics

| Per-check field | Default field | Behavior |
|---|---|---|
| `grace: 30m` | `grace: 5m` | Per-check wins (30m). |
| (omitted) | `grace: 5m` | Default applies (5m). |
| `grace: 0s` | `grace: 5m` | Per-check wins (0s — no grace at all). |
| `channels: [ops]` | `channels: [pager]` | Per-check wins (`ops` only — list replaces). |
| (omitted) | `channels: [pager]` | Default applies (`pager`). |
| `channels: []` | `channels: [pager]` | Per-check wins (empty — genuinely silent). |

The `[] vs absent` distinction is preserved through the YAML emitter. Explicitly setting a field to its empty value is **opting out** of the default — not redundant.

## What can be defaulted

- **`grace`** — see [checks](/cadence/configuration/checks/) for the state machine.
- **`timeout`** — applies to `/start`-opened runs.
- **`ping_keys`** — list of names from the [ping-keys registry](/cadence/configuration/ping-keys/).
- **`channels`** — list of names from [channels](/cadence/configuration/channels/).

Other check fields (`period`, `cron`, `slug`, etc.) have no defaults — they're always per-check.

## Validation

References in `defaults.ping_keys` and `defaults.channels` must resolve to declared entries, same as per-check references.
