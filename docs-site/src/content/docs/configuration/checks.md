---
title: Checks
description: Declaring monitored checks — schedule, grace, auth, alerting.
---

A check is one thing you want to know is still running. Each entry in `checks:` becomes one monitored unit.

## Schema

```yaml
checks:
  - slug: nightly-backup       # required, globally unique, [a-zA-Z0-9_-]+
    name: "Nightly backup"     # optional, defaults to slug in the dashboard
    period: 24h                # XOR with `cron`
    # cron: "0 3 * * *"        # XOR with `period`; 5 fields, UTC
    grace: 30m                 # how long after the deadline before going down
    timeout: 1h                # max duration for a /start-opened run
    ping_keys: [cron]          # names from the ping_keys registry
    channels: [ops]            # names from the channels list
    tags: [backup, nightly]    # arbitrary, surfaced in the dashboard + API
    enabled: true              # optional, default true
    uuid: "abc-...-def"        # optional; pin a literal UUID instead of deriving
```

## Schedule — `period` vs `cron`

Set **exactly one**:

- `period`: a Go duration string. cadence expects the next ping within `period`. The check transitions to `late` at `period` and to `down` at `period + grace`.
- `cron`: a 5-field cron expression evaluated in UTC. Use this for jobs that run at calendar boundaries (`0 3 * * *` for 03:00 nightly) where a fixed interval would drift.

Validation fails the daemon if a check sets both or neither.

## State machine

```
new → up → late → down
         ↑      ↓
         └──────┘   (recovery on next successful ping)
```

- **`new`** — the check exists in config but has never been pinged.
- **`up`** — within the expected window of the last ping.
- **`late`** — past the deadline, within the grace window. **Reported as `grace` in the v3 API** for HC.io wire compatibility.
- **`down`** — past `period + grace` with no ping. Alerts fire **once** on entry.

A separate `started` boolean tracks whether `/start` has been called without a matching success/fail — it's not a state.

## Identity

Each check's UUID is `UUIDv5(server.uuid_salt, slug)`. That makes it:

- **Stable** across restarts and across machines that share the same salt.
- **Unguessable** to anyone who doesn't know the salt — even though the slug is in the config.

LevelDB keys are namespaced by UUID, so renaming a slug starts the check with fresh history rather than corrupting the old series. Set `uuid:` explicitly to migrate from another system that already issued an ID you need to keep.

## Auth

Set `ping_keys: [name1, name2]` to require one of those keys on slug-form pings. Leave the list empty to make the check "open" — open checks only accept UUID-form pings (the slug form is rejected so the slug can't accidentally become a secret).

See [ping keys](/cadence/configuration/ping-keys/) for the full auth model.
