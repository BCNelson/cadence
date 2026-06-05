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

## Tags & rollups

Every string in `tags:` is a free-form label — there's no registry to declare them in. A check can carry as many as it wants. Once tags exist on a check, three things light up:

- **Filter on the list endpoint.** `GET /api/v3/checks/?tag=prod&tag=db` returns only checks bearing **all** the listed tags (AND, matching Healthchecks.io).
- **Per-tag rollup endpoints.** `GET /api/v3/tags/` lists every tag with its combined status; `GET /api/v3/tags/{name}` returns the full check views for one tag with that combined status alongside.
- **Per-tag badges.** `/badge/tag/{name}.svg`, `.json`, `.shields` render a badge representing the rollup. The special name `*` rolls up every check. Append `-3` (e.g. `/badge/tag/{name}-3.svg`) for the 3-state palette that shows `late` distinctly instead of collapsing it into `down`.

The rollup rule is **worst-wins, paused excluded**: `down > late > new > up`. A paused member doesn't drag a tag down; if every member is paused, the tag itself reports `paused`. So pausing a noisy check stops it from masking the tag's real signal.

Tags are passed through verbatim — they're case-sensitive and not normalized. The space-separated form returned by `/api/v3/checks/` is the Healthchecks.io wire convention; the rollup endpoints return them as JSON arrays.
