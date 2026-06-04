---
title: Binary release
description: Build cadence from source as a single static binary.
---

cadence is a single Go binary with the React dashboard embedded via `//go:embed`. There are no runtime dependencies beyond a writable data directory.

## Build from source

```sh
git clone https://github.com/bcnelson/cadence
cd cadence
just build
./cadence -c cadence.yaml
```

`just build` compiles the frontend with Vite, copies the bundle into `internal/web/dist/`, then `go build`s the daemon. The resulting `./cadence` binary is everything you need to ship.

## Cross-compiling

Standard Go cross-compilation works — `CGO_ENABLED=0` is set in the build script, so there's no glibc to worry about:

```sh
GOOS=linux GOARCH=arm64 just build
```

## Running

```sh
./cadence -c cadence.yaml
./cadence -c base.yaml -c overrides.yaml   # layered (rightmost wins)
./cadence -c config.d/                      # all *.yaml in the directory
./cadence -version                          # print version and exit
```

See the [CLI reference](/cadence/cli/cadence/) for the full flag list.

## Smoke test

```sh
curl -fsS http://127.0.0.1:8080/healthz   # → ok
```
