# cadence

A self-hosted, config-driven monitoring daemon — a single-binary [Healthchecks.io](https://healthchecks.io) alternative where YAML is the source of truth.

Your services ping cadence on a schedule. If a ping doesn't arrive within `period + grace`, the check goes `down` and webhooks fire. The ping endpoints and the read side of the v3 management API are wire-compatible with Healthchecks.io, so existing cron snippets and dashboards keep working.

## Documentation

Full documentation is at **<https://bcnelson.github.io/cadence/>**, including:

- [Quickstart](https://bcnelson.github.io/cadence/quickstart/)
- [NixOS module](https://bcnelson.github.io/cadence/install/nixos/) (the recommended install path)
- [Configuration reference](https://bcnelson.github.io/cadence/configuration/overview/) — schema, layering, interpolation
- [HTTP API](https://bcnelson.github.io/cadence/api/ping/) — ping endpoints + v3 management
- [NixOS options reference](https://bcnelson.github.io/cadence/nixos/options/general/) — auto-generated from `nix/module.nix`

## Local development

```sh
direnv allow   # enters the devenv shell
just build
just test
```

See [docs/contributing](https://bcnelson.github.io/cadence/contributing/) for the full setup and conventions.

## License

Not yet licensed.
