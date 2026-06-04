---
title: Dashboard
description: The embedded React dashboard for browsing check state.
---

cadence ships with an embedded React dashboard, served on the same listener as the API at `/`. The frontend is bundled into the Go binary via `//go:embed internal/web/dist/` — there is no separate frontend service to deploy.

## Access

Open the listener URL in a browser (default <http://localhost:8080>). No login: the dashboard shows what the daemon knows about. If you expose the listener publicly, put it behind your usual reverse-proxy auth.

## What you'll see

- **Check list** with current status (`new`, `up`, `late`/`grace`, `down`), the last ping time, and the next expected ping.
- **Per-check detail** — recent pings (with captured body and exit code if provided), recent state events, schedule, and the resolved configuration view.
- **Live updates** — the dashboard subscribes to [`/events`](/cadence/api/sse/) so new pings and transitions render without a refresh.

## Configuration is read-only

The dashboard is intentionally view-only. There is no "edit check" button — checks are declared in YAML and the API returns `409 Conflict` on any write. To add or change a check, edit your config and restart the daemon.

## Development

To work on the frontend, see [contributing](/cadence/contributing/). `just dev` starts the Vite dev server with hot module reload and proxies API requests to the Go backend.
