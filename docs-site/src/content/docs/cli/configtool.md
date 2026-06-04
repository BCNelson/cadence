---
title: configtool
description: Validate and preview a cadence configuration without starting the daemon.
---

`configtool` accepts the same `-c` flag layering as `cadence` itself. It loads, merges, interpolates, and resolves the configuration, then prints the result as YAML to stdout. Ping-key secrets are masked.

Use it as a CI lint gate or as a sanity check before deploying a config change.

## Synopsis

```
configtool [-c PATH]...
```

## Flags

| Flag | Repeatable | Description |
|---|---|---|
| `-c PATH` / `--config PATH` | yes | Same semantics as [`cadence -c`](/cadence/cli/cadence/). |

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Config loaded and rendered successfully. |
| `1` | Validation or load error. The message goes to stderr. |
| `2` | No `-c` provided. |

## Output

YAML with these top-level keys:

- `server` — unmasked.
- `data_dir` — unmasked.
- `retention` — unmasked.
- `ping_keys` — names listed, secrets replaced with `***`.
- `channels` — sorted by name.
- `checks` — sorted by slug. Each entry shows the **resolved** values (after defaults merge), the derived UUID, and a `uuid_pinned: true` flag when the UUID came from config rather than from the slug+salt derivation.

The output is the same shape the daemon sees internally — useful for catching unintended overrides or surprising defaults.

## Examples

```sh
# Validate (exit code is what matters)
configtool -c cadence.yaml > /dev/null

# Preview the resolved config
configtool -c base.yaml -c overlays/prod.yaml

# CI lint gate
- name: lint cadence config
  run: configtool -c configs/ > /dev/null
```
