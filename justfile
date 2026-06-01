# Version info from git
VERSION := `git describe --tags --always 2>/dev/null || echo "dev"`
COMMIT := `git rev-parse --short HEAD 2>/dev/null || echo "unknown"`
BUILD_DATE := `date -u +%Y-%m-%dT%H:%M:%SZ`
LDFLAGS := "-X main.version=" + VERSION + " -X main.commit=" + COMMIT + " -X main.buildDate=" + BUILD_DATE

# Build the binary (frontend bundled first)
build: frontend
    go build -ldflags '{{LDFLAGS}}' -o cadence ./cmd/cadence

# Run the binary
run: build
    ./cadence

# Run all Go tests with coverage
test:
    go test -race -count=1 -timeout=300s -coverprofile=coverage.out -covermode=atomic ./cmd/... ./internal/...
    go tool cover -func=coverage.out

# Coverage HTML report at coverage.html (filtered via .coverignore if present)
coverage: test
    @if [ -f .coverignore ]; then \
        pattern=$(grep -v '^#' .coverignore | grep -v '^$' | paste -sd'|' -); \
        grep -v -E "$pattern" coverage.out > coverage.filtered && mv coverage.filtered coverage.out; \
    fi
    go tool cover -html=coverage.out -o coverage.html
    @echo "Coverage report: coverage.html"

# Lint
lint:
    golangci-lint run ./cmd/... ./internal/...

# Format
fmt:
    gofmt -w .
    goimports -w .

# Tidy modules
tidy:
    go mod tidy

# Typecheck and lint frontend
frontend-check:
    cd frontend && npx tsc --noEmit && npx eslint .

# Build frontend and copy into Go embed dir
frontend:
    rm -rf internal/web/dist
    cd frontend && npm ci && npm run build
    mkdir -p internal/web/dist
    cp -r frontend/dist/* internal/web/dist/

# Run Vite dev server (also starts Go backend via Vite plugin)
dev:
    cd frontend && npm run dev

# Build container image (uses Dockerfile multi-stage)
docker-build:
    docker build \
        --build-arg VERSION={{VERSION}} \
        --build-arg COMMIT={{COMMIT}} \
        --build-arg BUILD_DATE={{BUILD_DATE}} \
        -t cadence:{{VERSION}} \
        -t cadence:latest \
        .

# Clean build artifacts
clean:
    rm -f cadence coverage.out coverage.html
    rm -rf internal/web/dist
    mkdir -p internal/web/dist
    @echo '<!doctype html><html><body>Run just frontend</body></html>' > internal/web/dist/index.html
