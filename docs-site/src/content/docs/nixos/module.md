---
title: NixOS module overview
description: Enable, configure, and harden cadence on NixOS.
---

The NixOS module (`nixosModules.cadence`) is the recommended install path. It brings:

- Typed configuration with eval-time validation.
- A hardened systemd unit (sandboxed, no caps, restricted syscalls).
- Automatic user/group creation and state-directory setup.
- Optional firewall hole punching for public listen addresses.
- A clean way to layer secrets in via `environmentFile` and `extraConfigFiles`.

For installation steps and a working example, see the [install page](/cadence/install/nixos/). This page covers the broader picture: how the pieces fit together and where to look when something doesn't.

## How configuration flows

```
services.cadence.settings        →  YAML in Nix store (no secrets)
services.cadence.extraConfigFiles →  YAML outside the store (secrets)
services.cadence.environmentFile  →  systemd EnvironmentFile (env-resolved tokens)

  ↓ all three become `-c …` arguments to the cadence binary

cadence merge + interpolate + validate
```

Field-level merge happens *inside cadence*, so `extraConfigFiles` overrides `settings` on a per-field basis (not whole-block). The same applies to the `${env:KEY}` interpolation — values from `environmentFile` substitute wherever the token appears.

## Where secrets go

Three rules:

1. **Never** inline a secret in `settings`. The generated settings file lives in the world-readable Nix store.
2. Reference env vars with `${env:KEY}` and supply them via `environmentFile`. This works for anything that needs to land *inline* in a string — webhook URLs, header tokens, `uuid_salt`.
3. For whole secret-bearing structures (a list of `ping_keys` entries), put them in a YAML file referenced from `extraConfigFiles`. Set the file mode to `0640 root:cadence` so only the daemon user can read it.

The VM test under `nix/test.nix` exercises all three patterns.

## What the assertions catch

The module fails `nixos-rebuild build` rather than letting bad config reach runtime:

- Duplicate `slug` in `checks`.
- Duplicate `name` in `channels` or `ping_keys`.
- A check referencing a `ping_keys` or `channels` name that isn't declared.
- `defaults.ping_keys` / `defaults.channels` referencing undeclared names.
- A check that sets both `period` and `cron`, or neither.

Type-level checks (wrong field name, bad duration string, invalid slug, unknown channel type) come from the option types themselves and fail with a Nix type error.

Eval-failure scenarios are covered by `nix/eval-failures.nix` — run them with `nix flake check` to verify the assertions still trip.

## Hardening

The systemd unit is locked down by default. The full list:

- `NoNewPrivileges = true`
- `ProtectSystem = "strict"`, `ProtectHome = true`
- `PrivateTmp = true`, `PrivateDevices = true`
- `ProtectKernelTunables = true`, `ProtectKernelModules = true`, `ProtectKernelLogs = true`
- `ProtectControlGroups = true`, `ProtectClock = true`, `ProtectHostname = true`
- `ProtectProc = "invisible"`
- `RestrictNamespaces = true`, `RestrictRealtime = true`, `RestrictSUIDSGID = true`
- `LockPersonality = true`, `MemoryDenyWriteExecute = true`
- `SystemCallArchitectures = "native"`
- `SystemCallFilter = [ "@system-service" "~@privileged" "~@resources" ]`
- `CapabilityBoundingSet = [ ]`, `AmbientCapabilities = [ ]`
- `ReadWritePaths = [ dataDir ]`

If you need to tighten further (e.g. `IPAddressDeny`), use a drop-in override on `cadence.service` rather than editing the module.

## State directory

The unit's `WorkingDirectory` is `dataDir`. The path is created (or chowned) on activation via `systemd.tmpfiles.rules` rather than `StateDirectory=`, so any path works — not just `/var/lib/<name>`-style ones. Default is `/var/lib/cadence`, mode `0750`, owned by `cadence:cadence`.

## Reference

- [General options](/cadence/nixos/options/general/) — `enable`, `package`, `user`, `group`, `dataDir`, `listen`, `openFirewall`, `extraConfigFiles`, `environmentFile`.
- [Server](/cadence/nixos/options/server/), [Retention](/cadence/nixos/options/retention/), [Ping keys](/cadence/nixos/options/ping-keys/), [Defaults](/cadence/nixos/options/defaults/), [Channels](/cadence/nixos/options/channels/), [Checks](/cadence/nixos/options/checks/) — the typed `services.cadence.settings.*` tree.
