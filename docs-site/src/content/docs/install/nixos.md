---
title: NixOS module
description: Enable cadence as a NixOS service with typed configuration and a hardened systemd unit.
---

The flake exposes `nixosModules.cadence`. Importing it adds `services.cadence.*` to your NixOS config, with the full schema enforced at eval time — typos and missing required fields fail at `nixos-rebuild build` rather than at daemon startup.

## Enable

In your flake:

```nix
{
  inputs.cadence.url = "github:bcnelson/cadence";

  outputs = { self, nixpkgs, cadence, ... }: {
    nixosConfigurations.my-host = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        cadence.nixosModules.cadence
        ./configuration.nix
      ];
    };
  };
}
```

Then in `configuration.nix`:

```nix
{ ... }: {
  services.cadence = {
    enable = true;
    openFirewall = true;
    environmentFile = "/run/secrets/cadence.env";
    settings = {
      server.uuid_salt = "\${env:CADENCE_UUID_SALT}";
      checks = [
        { slug = "nightly-backup"; cron = "0 3 * * *"; grace = "30m"; channels = [ "ops" ]; }
      ];
      channels = [
        { name = "ops"; type = "webhook"; url = "https://hooks.example.com/cadence"; }
      ];
    };
  };
}
```

## Secrets

The generated settings YAML lands in the world-readable Nix store, so never inline secrets in `settings`. Three channels are designed for secret material:

- **`environmentFile`** — a `KEY=value` file passed to systemd as `EnvironmentFile=`. Reference each variable in YAML with `${env:KEY}` ([interpolation reference](/cadence/configuration/interpolation/)).
- **`${file:...}` interpolation in `settings`** — pair cadence with any secret manager that drops a file on disk: agenix (`age.secrets.<name>.path`), sops-nix (`sops.secrets.<name>.path`), Kubernetes projected volumes, or systemd `LoadCredential=`. The token itself is non-secret, so it's safe to inline. The secret file's `owner`/`mode` must permit reads by `services.cadence.user` (cadence runs as a fixed user, so set the secret's `owner` to that user). When the path itself comes from `environmentFile`, compose the two: `"${file:${env:CADENCE_UUID_SALT_FILE}}"` — env resolves first, then file reads its contents.
- **`extraConfigFiles`** — extra YAML files (typically on tmpfs or owned by the cadence user) layered on top of `settings`. Field-level merge happens inside cadence, so these override `settings` per-field.

The VM test under `nix/test.nix` exercises the first and third channels.

## Hardening

The systemd unit ships with the common hardening knobs enabled by default: `NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, `MemoryDenyWriteExecute`, an empty `CapabilityBoundingSet`, and a `SystemCallFilter` that drops `@privileged` and `@resources`. `ReadWritePaths` is set to just `dataDir`. If you tighten further (e.g. add `IPAddressDeny`), do it in an override drop-in rather than editing the module.

## Assertions

The module fails `nixos-rebuild build` on misconfigurations rather than letting them through to runtime:

- Duplicate `slug`, `name` (channels), or `name` (ping_keys).
- A check referencing a `ping_keys` or `channels` entry that isn't declared.
- `defaults.ping_keys` / `defaults.channels` referencing undeclared names.
- A check that sets both `period` and `cron`, or neither.

## Reference

The full option tree is auto-generated from the module source on every docs build:

- [General](/cadence/nixos/options/general/) — enable, package, user/group, dataDir, listen, firewall, secrets handling.
- [Server](/cadence/nixos/options/server/) — listen, base URL, UUID salt, API keys.
- [Retention](/cadence/nixos/options/retention/) — per-check ping and event caps.
- [Ping keys](/cadence/nixos/options/ping-keys/) — shared-secret registry.
- [Defaults](/cadence/nixos/options/defaults/) — fallbacks for omitted check fields.
- [Channels](/cadence/nixos/options/channels/) — webhook destinations.
- [Checks](/cadence/nixos/options/checks/) — the monitored checks themselves.
