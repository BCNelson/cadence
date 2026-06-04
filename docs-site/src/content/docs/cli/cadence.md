---
title: cadence
description: The monitoring daemon binary.
---

`cadence` is the daemon. It loads YAML configuration, opens the LevelDB store, starts the engine, the HTTP APIs, the SSE bus, and serves the embedded React dashboard.

## Synopsis

```
cadence [-c PATH]... [-version]
```

## Flags

| Flag | Repeatable | Description |
|---|---|---|
| `-c PATH` / `--config PATH` | yes | Configuration file or directory. At least one is required. Layered left-to-right; rightmost wins. Directories expand to their `*.yaml` / `*.yml` files sorted lexically. |
| `-version` | no | Print version (format: `cadence VERSION (COMMIT, built DATE)`) and exit. |

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Clean shutdown (SIGINT / SIGTERM). |
| `1` | Startup failed — bad config, store open failure, engine init error. The underlying error is logged. |
| `2` | No `-c` provided. |

## Defaults

- Listen address: `:8080` (overridden by `server.listen` in config).
- Data directory: `./data` (overridden by `data_dir` in config).
- Logs: JSON to stderr via `log/slog`.

## Signals

- `SIGINT`, `SIGTERM` — graceful shutdown with a 10-second HTTP shutdown grace period.

## Examples

```sh
# Single file
cadence -c cadence.yaml

# Layered (rightmost wins)
cadence -c base.yaml -c overlays/prod.yaml

# Directory expansion
cadence -c /etc/cadence/config.d/

# Mixed
cadence -c /etc/cadence/base.yaml -c /etc/cadence/secrets.d/

# Version
cadence -version
# cadence 0.1.0 (abc1234, built 2026-06-03T12:00:00Z)
```

## See also

- [configtool](/cadence/cli/configtool/) — validate and preview a config without starting the daemon.
- [Configuration overview](/cadence/configuration/overview/) — schema and layering rules.
