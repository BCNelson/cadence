---
title: Contributing
description: Get set up to hack on cadence — devenv, build, test, frontend, NixOS VM test.
---

## Get set up

cadence uses [devenv](https://devenv.sh) for the dev shell. The shell brings Go, Node, just, golangci-lint, and the pre-commit hooks.

```sh
git clone https://github.com/bcnelson/cadence
cd cadence
direnv allow
```

`direnv` enters the devenv shell on every `cd` into the repo. If you don't use direnv, run `nix develop` instead.

## Build and run

```sh
just build      # frontend + go build
just run        # build then ./cadence (needs a -c added via JUSTFILE_ARGS or similar)
just test       # go test -race with coverage
just coverage   # HTML report at coverage.html
just lint       # golangci-lint
just fmt        # go fmt + goimports
```

## Frontend

```sh
just dev              # Vite dev server with HMR, proxies API to backend
just frontend         # build SPA into internal/web/dist/ (for embedding)
just frontend-check   # tsc + eslint
```

Playwright is also installed for end-to-end browser tests; see `frontend/tests/e2e/`.

## NixOS VM test

The NixOS module ships with two checks:

```sh
nix build .#checks.x86_64-linux.cadence-module       # VM test (KVM required)
nix build .#checks.x86_64-linux.cadence-module-eval  # assertion failures
```

`nix flake check` runs both. The VM test exercises layered config, `extraConfigFiles`, `environmentFile` interpolation, the hardening flags, and `dataDir` override.

## Docs site

```sh
just docs-options    # build the NixOS options JSON
just docs-dev        # local Astro dev server
just docs            # production build into docs-site/dist
```

Editing `nix/module.nix` will refresh the generated options pages on the next docs build — no manual sync step.

## Conventions

- Table-driven tests with `t.Run` sub-tests.
- `context.Context` as the first arg on anything that touches I/O.
- `internal/` for non-exported packages; nothing under `pkg/` until a second consumer exists.
- No package-level mutable state; no `init()` for behavior.
- Errors are typed where callers need to discriminate, otherwise wrap with `fmt.Errorf("...: %w", err)`.

See `CLAUDE.md` for the full project conventions table.

## Pull requests

PRs should:

- Pass `just test`, `just lint`, and `just frontend-check`.
- Include tests for new behavior.
- Update the relevant docs page when changing user-facing behavior or adding a NixOS option. (The options reference auto-regenerates from `nix/module.nix` — you only need to update prose.)
