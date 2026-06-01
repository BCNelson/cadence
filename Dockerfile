# Frontend and Go builds run on the native build platform; the frontend output
# is arch-independent and the Go binary is cross-compiled via GOOS/GOARCH, so
# multi-arch builds avoid slow QEMU emulation.
FROM --platform=$BUILDPLATFORM node:22-slim AS frontend
WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.23 AS builder
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
COPY --from=frontend /app/frontend/dist ./internal/web/dist/
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
    -o /out/cadence ./cmd/cadence

# :nonroot runs as UID/GID 65532.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/cadence /cadence
ENTRYPOINT ["/cadence"]
