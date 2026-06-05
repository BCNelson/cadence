---
title: Ping endpoints
description: /ping/* — success, start, fail, log, exit codes, body capture.
---

The ping API is the inbound surface that your cron jobs and services hit. It is wire-compatible with Healthchecks.io's ping endpoints.

## Routes

| Pattern | Meaning |
|---|---|
| `GET\|POST\|HEAD /ping/{id}` | Success ping. |
| `GET\|POST\|HEAD /ping/{id}/start` | Open a run. The next success/fail closes it. |
| `GET\|POST\|HEAD /ping/{id}/fail` | Mark the check as down on this ping. |
| `GET\|POST\|HEAD /ping/{id}/log` | Record a ping without changing status. |
| `GET\|POST\|HEAD /ping/{id}/{exit_code}` | Numeric exit code: `0` is success, non-zero is fail. |

`{id}` is either a check's `slug` or its `uuid`. See [ping keys](/cadence/configuration/ping-keys/) for how auth differs between the two.

Every successful ping returns `200 OK` with the body `OK`. Every failure returns `404 Not Found` — there is no auth-error response.

## The 404-not-403 rule

Wrong key, unknown slug, missing check, wrong UUID all return **404**, not 403. The slug namespace stays non-enumerable — clients can't probe to learn what checks exist. This matches HC.io's behavior; do not change it for clarity.

## Body capture

The request body is captured and stored as part of the ping log, capped at the store's `MaxBodyBytes`. Cadence advertises the cap via response header:

```
Ping-Body-Limit: 10000
```

The header name matches HC.io's, so existing clients reading it get the value unchanged.

Bodies over the cap are truncated; the stored ping is flagged as truncated for the dashboard.

## Runs (`/start`)

`/start` opens a run for this check. The next `/ping/{id}`, `/ping/{id}/0`, `/ping/{id}/fail`, or `/ping/{id}/<nonzero>` closes it.

A run with no closing ping inside the check's `timeout` is treated as a failure. Use `/start` from your cron job wrapper to detect a hung process that's still alive:

```sh
curl -fsS -H 'X-Ping-Key: $KEY' http://cadence/ping/nightly-backup/start
do-the-actual-work
curl -fsS -H 'X-Ping-Key: $KEY' http://cadence/ping/nightly-backup/$?
```

If `do-the-actual-work` hangs past `timeout`, the run is recorded as failed even without an explicit `/fail`.

## Examples

```sh
# Slug form with header auth
curl -fsS -H 'X-Ping-Key: secret' http://cadence/ping/nightly-backup

# Slug form with query-string auth
curl -fsS 'http://cadence/ping/nightly-backup?ping_key=secret'

# UUID form for an open check (ping_keys: [])
curl -fsS http://cadence/ping/11111111-2222-3333-4444-555555555555

# Exit-code form (success when $? = 0, fail otherwise)
do-thing; curl -fsS -H 'X-Ping-Key: secret' "http://cadence/ping/nightly-backup/$?"

# POST with body (captured up to the cap)
my-script 2>&1 | curl -fsS -X POST --data-binary @- \
  -H 'X-Ping-Key: secret' \
  http://cadence/ping/nightly-backup
```
