---
title: Health check
description: GET /healthz — minimal liveness endpoint.
---

`GET /healthz` returns `200 OK` with the body `ok` whenever the HTTP server is accepting connections. It does not authenticate.

```sh
curl -fsS http://cadence/healthz   # → ok
```

Suitable for:

- Kubernetes liveness/readiness probes.
- Docker / systemd `HEALTHCHECK`s.
- Reverse-proxy upstream checks.

The endpoint does not exercise the LevelDB store or the engine — it confirms only that the HTTP listener is up. If you need deeper checks, ping the management API with a real `X-Api-Key`.
