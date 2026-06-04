---
title: Configuration overview
description: Top-level YAML keys, layering, and merge semantics.
---

cadence reads one or more YAML files via the repeatable `-c` flag. All files are parsed (with recursive `import:` expansion), deep-merged in left-to-right order, interpolated, validated, then resolved into the in-memory registry the daemon serves from.

## Top-level keys

| Key | Type | Purpose |
|---|---|---|
| `server` | object | HTTP listener, base URL, UUID salt, management API keys. |
| `data_dir` | string | LevelDB store path. Default: `./data`. |
| `retention` | object | Per-check ping and event caps. |
| `ping_keys` | list | Named shared-secret registry. |
| `defaults` | object | Fallback values for omitted check fields. |
| `channels` | list | Notification destinations. |
| `checks` | list | Monitored checks. |
| `import` | string \| list | YAML files (or globs) to merge *before* this file's own body. |

Schema reference is split by area: [checks](/cadence/configuration/checks/), [channels](/cadence/configuration/channels/), [ping keys](/cadence/configuration/ping-keys/), [defaults](/cadence/configuration/defaults/), [interpolation](/cadence/configuration/interpolation/), [imports & layering](/cadence/configuration/imports/).

## The `-c` flag

```sh
cadence -c base.yaml -c overlays/prod.yaml -c /etc/cadence/secrets.d/
```

- Repeatable. Each `-c` is one layer.
- Layers merge left to right. **Rightmost wins.**
- A `-c` pointing at a directory expands to every `*.yaml` and `*.yml` file in it, sorted lexically.
- An importing file's body always wins over the files it imports — imports are merged *underneath* the importer.

## Merge semantics

cadence uses keyed deep-merge for the lists that have a natural identity:

- `checks` (key: `slug`)
- `channels` (key: `name`)
- `ping_keys` (key: `name`)

Matching items deep-merge field by field; non-matching items append. A duplicate slug (or channel/ping-key name) within a single resolved layer is a hard error.

Other lists — `defaults.channels`, `defaults.ping_keys`, `server.api_keys.read_write`, `server.api_keys.read_only`, `checks[*].channels`, `checks[*].ping_keys`, `checks[*].tags` — **replace** rather than append. Last layer wins as a whole list. This is deliberate: appending would make it impossible to *remove* a default in an overlay.

Maps deep-merge as expected (so `channels[*].headers` accumulates header by header across layers).

## Validation

After merge and interpolation, cadence enforces:

- `server.uuid_salt` is set.
- Every check has exactly one of `period` or `cron`.
- Every reference (`check.ping_keys`, `check.channels`, `defaults.ping_keys`, `defaults.channels`) resolves to a declared name.
- Slugs are globally unique.
- Cron expressions parse as 5-field UTC expressions.
- Duration strings parse via Go's `time.ParseDuration`.

A failed validation aborts startup with a message identifying the offending file and line.

## Reload

cadence does not currently hot-reload. Restart the daemon to pick up config changes — the LevelDB state is keyed by check UUID, so renaming a slug (which changes its UUID) starts that check with fresh history rather than corrupting the old one.
