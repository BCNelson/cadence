---
title: SSE event stream
description: Server-Sent Events for live dashboard updates.
---

`GET /events` is a Server-Sent Events stream that broadcasts state transitions and pings as they happen. The embedded React dashboard subscribes to this stream so the UI updates without polling.

## Connecting

```sh
curl -N http://cadence/events
```

`-N` disables curl's output buffering. Each event is delivered as it occurs.

## Event format

Standard SSE — each event is one or more `event:` / `data:` lines separated by a blank line. The `data:` payload is JSON.

The stream is intended for the dashboard; the exact event names and payload shape are not yet part of the public stable surface. If you need a stable external feed, build it on top of webhook notifications instead.

## Notes

- No authentication is required to subscribe — the stream is open to anyone who can reach the listener. If you expose the dashboard publicly, put it behind your usual reverse-proxy auth.
- The connection stays open until the client closes it or the daemon shuts down.
