---
title: Docker
description: Run cadence from the official GHCR container image.
---

Multi-arch images (`linux/amd64`, `linux/arm64`) are published to the GitHub Container Registry on every push to `main` and on semver tags.

## Run

```sh
docker run --rm \
  -p 8080:8080 \
  -v $PWD/cadence.yaml:/etc/cadence/cadence.yaml:ro \
  -v cadence-data:/data \
  ghcr.io/bcnelson/cadence:latest \
  -c /etc/cadence/cadence.yaml
```

- Mount your config read-only into the container.
- Mount a named volume (or a host directory) for the LevelDB store; set `data_dir: /data` in the config to match.
- The image runs as a non-root user (UID 65532). The mounted data volume must be writable by that UID.

## Docker Compose

```yaml
services:
  cadence:
    image: ghcr.io/bcnelson/cadence:latest
    command: ["-c", "/etc/cadence/cadence.yaml"]
    ports:
      - "8080:8080"
    volumes:
      - ./cadence.yaml:/etc/cadence/cadence.yaml:ro
      - cadence-data:/data
    environment:
      CADENCE_UUID_SALT: "${CADENCE_UUID_SALT}"
    restart: unless-stopped

volumes:
  cadence-data:
```

Pass secrets through environment variables and reference them in YAML with `${env:CADENCE_UUID_SALT}` — see [interpolation](/cadence/configuration/interpolation/).

## Smoke test

```sh
curl -fsS http://127.0.0.1:8080/healthz   # → ok
```
