---
title: Imports and layering
description: "-c layering, the import: key, merge precedence, directory expansion."
---

cadence has two ways to compose configuration: the **`-c` flag** at the CLI level and the **`import:`** key inside a YAML file. The semantics are the same — the difference is who declares the composition.

## `-c` layering

```sh
cadence -c base.yaml -c overlays/prod.yaml -c /etc/cadence/secrets.d/
```

- Each `-c` is one layer.
- Layers merge left to right; **rightmost wins**.
- A `-c` pointing at a directory expands to every `*.yaml` / `*.yml` file inside, sorted lexically.
- The flag is repeatable. The [NixOS module](/cadence/install/nixos/) uses this to layer `extraConfigFiles` on top of the generated `settings`.

## `import:` inside a file

```yaml
import:
  - ./checks/web.yaml
  - ./checks/cron.yaml
  - ./channels.d/

defaults:
  grace: 5m
```

- Accepts a string or a list of strings.
- Paths resolve relative to the *directory of the importing file*.
- A path pointing at a directory expands to its `*.yaml` / `*.yml` contents, sorted lexically.
- **The importing file's own body always wins** over the files it imports. Imports merge *underneath* — they provide a base, the importer overlays.
- Cycles are rejected with a clear error.

## Combined precedence

For a `cadence -c A.yaml -c B.yaml` invocation where `A.yaml` imports `A1.yaml`:

```
A1 (imported)  <  A's own body  <  B
```

Lower in this list wins. The end result is that imports give you reusable building blocks; `-c` layers give you environment-specific overlays.

## Merge rules

See the [overview](/cadence/configuration/overview/#merge-semantics) for the full keyed-vs-scalar-list rules. Quick summary:

- `checks`, `channels`, `ping_keys` — keyed deep-merge (by `slug` / `name`).
- `defaults.*` scalar lists, `api_keys.*`, per-check `channels`/`ping_keys`/`tags` — **replace** rather than append.
- Maps (e.g. `channels[*].headers`) deep-merge.
- Duplicate slug or duplicate channel/ping-key name in a single resolved layer is a hard error.

## Practical layout

A common production layout:

```
/etc/cadence/
├── base.yaml           # server, defaults, channels
├── checks/             # one file per logical group
│   ├── web.yaml
│   ├── cron.yaml
│   └── infrastructure.yaml
└── secrets.d/          # not committed; chmod 0640 cadence:cadence
    ├── 10-keys.yaml    # ping_keys with real secret values
    └── 20-channels.yaml # channels with auth tokens
```

```sh
cadence -c /etc/cadence/base.yaml -c /etc/cadence/checks/ -c /etc/cadence/secrets.d/
```
