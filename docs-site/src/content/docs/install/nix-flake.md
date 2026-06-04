---
title: Nix flake
description: Run cadence from the flake without installing it system-wide.
---

The repository is a flake. The default package builds the daemon (with the frontend embedded) and is suitable for ad-hoc runs or pinning in another flake.

## Run without installing

```sh
nix run github:bcnelson/cadence -- -c ./cadence.yaml
```

## Install into a profile

```sh
nix profile install github:bcnelson/cadence
cadence -c ./cadence.yaml
```

## Pin in your own flake

```nix
{
  inputs.cadence.url = "github:bcnelson/cadence";

  outputs = { self, nixpkgs, cadence }: {
    packages.x86_64-linux.default =
      cadence.packages.x86_64-linux.default;
  };
}
```

## On NixOS

For a real deployment, use the [NixOS module](/cadence/install/nixos/) instead — it brings a hardened systemd unit, typed configuration, and eval-time assertions on top of this same package.

## Smoke test

```sh
curl -fsS http://127.0.0.1:8080/healthz   # → ok
```
