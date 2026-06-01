# cadence

One-line description goes here.

## Tech stack

| Concern | Choice |
|---|---|
| Language | Go 1.23+ |
| Frontend | React 19 + Vite + TanStack Router/Query + Tailwind v4 + Radix UI |
| Embed | `internal/web/dist/` via `//go:embed` |
| Logs | `log/slog` JSON |
| Tests | `go test -race` + testcontainers-go |
| Task runner | Just |
| Linter | golangci-lint v2 |

## Project layout

```
.
├── cmd/cadence/        # Main binary
├── internal/             # Domain packages (not importable externally)
├── frontend/             # React SPA (built and embedded into the Go binary)
├── db/migrations/        # goose-numbered SQL migrations
├── services/             # Placeholder for future go.work monorepo split
└── .github/workflows/    # CI + container publish
```

## Running locally

```sh
# First time — enters the devenv shell with Go, Node all wired up
direnv allow

# Build + run
just build
just run
```

## Testing

`just test` runs `go test -race` with coverage. Add testcontainers-driven integration tests as the project grows.

```sh
just test        # all tests
just coverage    # HTML report at coverage.html
```

## MCP servers

`.mcp.json` configures project-local MCP servers:

- **context7** — up-to-date library docs lookup (always on).
- **playwright** — browser automation for the React SPA.

process-compose MCP is configured separately at the devenv layer — devenv exposes the SSE endpoint, picked up by your global Claude Code config. Not in this file.

## CI

- `.github/workflows/ci.yml` runs on PRs: frontend build/lint, Go lint, Go test with coverage.
- `.github/workflows/container.yml` publishes a multi-arch image to GHCR on pushes to `main` and semver tags.

## Conventions

- Table-driven tests with `t.Run` sub-tests.
- `context.Context` first arg on every function that touches DB / I/O / external services.
- Errors are typed where callers need to discriminate; otherwise wrap with `fmt.Errorf("...: %w", err)`.
- `internal/` for non-exported packages; nothing under `pkg/` until a second consumer exists.
- No package-level mutable state. No `init()` for behavior — only registration of types.
