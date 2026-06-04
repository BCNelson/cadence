{ self }:
{ config, lib, pkgs, ... }:

let
  cfg = config.services.cadence;

  yamlFormat = pkgs.formats.yaml { };

  # Go's time.ParseDuration accepts e.g. "300ms", "1.5h", "2h45m". The unit
  # suffix is one of ns, us (or µs), ms, s, m, h, and segments may repeat.
  # ASCII-only `us` is what the YAML loader will see from Nix; µs is not
  # representable as a plain Nix string without effort.
  durationPattern = "-?([0-9]+(\\.[0-9]+)?(ns|us|ms|s|m|h))+";
  durationType = lib.types.strMatching durationPattern // {
    description = "Go time.ParseDuration string (e.g. \"5m\", \"1h30m\", \"500ms\")";
  };

  apiKeysType = lib.types.submodule {
    options = {
      read_write = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
        description = ''
          API keys granting read+write to /api/v3. Prefer `''${env:NAME}`
          references over inline values (the settings file is in the
          world-readable Nix store).
        '';
      };
      read_only = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
        description = "API keys granting read-only access to /api/v3.";
      };
    };
  };

  serverType = lib.types.submodule {
    options = {
      listen = lib.mkOption {
        type = lib.types.str;
        default = ":8080";
        description = "TCP bind address.";
      };
      base_url = lib.mkOption {
        type = lib.types.str;
        default = "";
        example = "https://cadence.example.com";
        description = "External URL used to build ping URLs shown in the dashboard.";
      };
      uuid_salt = lib.mkOption {
        type = lib.types.str;
        example = "\${env:CADENCE_UUID_SALT}";
        description = ''
          REQUIRED. Salt mixed into the SHA-1 used to derive each check's
          UUID from its slug. Treat as a secret — supply via `''${env:NAME}`
          (with {option}`environmentFile`) or via {option}`extraConfigFiles`,
          not inline. Changing this value re-derives every UUID, breaking
          existing ping URLs.
        '';
      };
      api_keys = lib.mkOption {
        type = apiKeysType;
        default = { };
        description = "Management API authentication.";
      };
    };
  };

  retentionType = lib.types.submodule {
    options = {
      pings = lib.mkOption {
        type = lib.types.ints.positive;
        default = 100;
        description = "Maximum recent pings retained per check.";
      };
      events = lib.mkOption {
        type = lib.types.ints.positive;
        default = 200;
        description = "Maximum recent events retained per check.";
      };
    };
  };

  pingKeyType = lib.types.submodule {
    options = {
      name = lib.mkOption {
        type = lib.types.str;
        description = "Logical name (referenced by checks via `ping_keys`).";
      };
      key = lib.mkOption {
        type = lib.types.str;
        description = ''
          Secret token authorizing slug-form `/ping/<key>/<slug>` requests.
          Prefer `''${env:NAME}` or {option}`extraConfigFiles` over inline.
        '';
      };
    };
  };

  defaultsType = lib.types.submodule {
    options = {
      grace = lib.mkOption {
        type = lib.types.nullOr durationType;
        default = null;
        description = "Default grace period applied when a check omits one.";
      };
      timeout = lib.mkOption {
        type = lib.types.nullOr durationType;
        default = null;
        description = "Default execution timeout applied when a check omits one.";
      };
      ping_keys = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
        description = "Ping keys applied to checks that don't list their own.";
      };
      channels = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
        description = "Channels applied to checks that don't list their own.";
      };
    };
  };

  channelType = lib.types.submodule {
    options = {
      name = lib.mkOption {
        type = lib.types.str;
        description = "Channel identifier (referenced by checks).";
      };
      type = lib.mkOption {
        type = lib.types.enum [ "webhook" ];
        description = "Channel transport type.";
      };
      url = lib.mkOption {
        type = lib.types.str;
        description = "Destination URL (use `''${env:NAME}` for secrets in the path).";
      };
      method = lib.mkOption {
        type = lib.types.nullOr (lib.types.enum [ "GET" "POST" "PUT" "PATCH" "DELETE" ]);
        default = null;
        description = "HTTP method. Defaults to POST inside cadence if unset.";
      };
      headers = lib.mkOption {
        type = lib.types.attrsOf lib.types.str;
        default = { };
        example = lib.literalExpression ''{ "Authorization" = "Bearer \''${env:OPS_TOKEN}"; }'';
        description = "Custom HTTP headers sent with each notification.";
      };
    };
  };

  checkType = lib.types.submodule {
    options = {
      slug = lib.mkOption {
        type = lib.types.strMatching "[a-zA-Z0-9_-]+";
        description = "URL-safe identifier. Must be globally unique across all checks.";
      };
      name = lib.mkOption {
        type = lib.types.nullOr lib.types.str;
        default = null;
        description = "Human-readable name (defaults to slug in the dashboard).";
      };
      period = lib.mkOption {
        type = lib.types.nullOr durationType;
        default = null;
        description = "Expected interval between pings. Mutually exclusive with {option}`cron`.";
      };
      cron = lib.mkOption {
        type = lib.types.nullOr lib.types.str;
        default = null;
        example = "0 3 * * *";
        description = ''
          Standard 5-field cron expression evaluated in UTC. Mutually exclusive
          with {option}`period`.
        '';
      };
      grace = lib.mkOption {
        type = lib.types.nullOr durationType;
        default = null;
        description = "Grace period before this check transitions to `down`. Overrides {option}`services.cadence.settings.defaults.grace`.";
      };
      timeout = lib.mkOption {
        type = lib.types.nullOr durationType;
        default = null;
        description = "Maximum runtime for a `/start`-opened run before it is treated as failed. Overrides {option}`services.cadence.settings.defaults.timeout`.";
      };
      ping_keys = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
        description = "Names of ping_keys that may sign for this check. Empty inherits defaults.";
      };
      channels = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
        description = "Notification channels. Empty inherits defaults.";
      };
      tags = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
        description = "Arbitrary tags surfaced in the dashboard and `/api/v3` responses.";
      };
      enabled = lib.mkOption {
        type = lib.types.nullOr lib.types.bool;
        default = null;
        description = "Set false to keep the check defined but skip evaluation. Defaults to true.";
      };
      uuid = lib.mkOption {
        type = lib.types.nullOr lib.types.str;
        default = null;
        description = "Pin a stable UUID. If unset, derived from slug + uuid_salt.";
      };
    };
  };

  settingsType = lib.types.submodule {
    options = {
      data_dir = lib.mkOption {
        type = lib.types.str;
        default = "";
        description = "Set automatically from {option}`services.cadence.dataDir`.";
      };
      server = lib.mkOption {
        type = serverType;
        default = { };
        description = "HTTP server, identity salt, and management API key configuration.";
      };
      retention = lib.mkOption {
        type = retentionType;
        default = { };
        description = "Per-check retention limits for pings and events.";
      };
      ping_keys = lib.mkOption {
        type = lib.types.listOf pingKeyType;
        default = [ ];
        description = "Named ping-key registry. Checks reference entries by `name`; rotating a secret is a one-line change here.";
      };
      defaults = lib.mkOption {
        type = defaultsType;
        default = { };
        description = "Fallbacks applied to checks that don't set the corresponding field.";
      };
      channels = lib.mkOption {
        type = lib.types.listOf channelType;
        default = [ ];
        description = "Notification channels referenced by checks via `channels`.";
      };
      checks = lib.mkOption {
        type = lib.types.listOf checkType;
        default = [ ];
        description = "Health checks to monitor. Each entry's `slug` must be globally unique.";
      };
    };
  };

  # Drop nulls AND empty lists/attrs before serialising. Cadence
  # distinguishes "field absent" (defaults apply) from "field set to []"
  # (no defaults) — see resolveCheck in internal/config/load.go. Keeping
  # null/empty in the YAML would silently break `defaults.ping_keys` /
  # `defaults.channels` for users who set them.
  prune = x:
    if lib.isAttrs x then
      let pruned = lib.mapAttrs (_: prune) x;
      in lib.filterAttrs
        (_: v: v != null && !(lib.isList v && v == [ ]) && !(lib.isAttrs v && v == { }))
        pruned
    else if lib.isList x then
      map prune x
    else
      x;

  settingsFile = yamlFormat.generate "cadence-settings.yaml" (prune cfg.settings);

  configArgs = lib.concatStringsSep " " (
    [ "-c ${settingsFile}" ]
    ++ map (p: "-c ${lib.escapeShellArg p}") cfg.extraConfigFiles
  );

  # Reference-integrity helpers for assertions.
  declaredPingKeys = map (k: k.name) cfg.settings.ping_keys;
  declaredChannels = map (c: c.name) cfg.settings.channels;
  duplicates = xs:
    lib.unique (lib.filter (x: lib.count (y: y == x) xs > 1) xs);
in
{
  options.services.cadence = {
    enable = lib.mkEnableOption "the cadence monitoring daemon";

    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.cadence;
      defaultText = lib.literalExpression "cadence.packages.\${system}.cadence";
      description = "The cadence package to run.";
    };

    user = lib.mkOption {
      type = lib.types.str;
      default = "cadence";
      description = "User under which cadence runs.";
    };

    group = lib.mkOption {
      type = lib.types.str;
      default = "cadence";
      description = "Group under which cadence runs.";
    };

    dataDir = lib.mkOption {
      type = lib.types.path;
      default = "/var/lib/cadence";
      example = "/srv/cadence";
      description = ''
        Working directory and LevelDB store location. The path is created
        on activation (mode 0750, owned by {option}`user`:{option}`group`)
        regardless of where it lives, and added to the unit's
        `ReadWritePaths` so the sandbox can write to it. Maps to `data_dir`
        in the generated settings unless overridden in `settings.data_dir`.
      '';
    };

    settings = lib.mkOption {
      type = settingsType;
      default = { };
      example = lib.literalExpression ''
        {
          server.uuid_salt = "\''${env:CADENCE_UUID_SALT}";
          retention = { pings = 100; events = 200; };
          checks = [
            { slug = "nightly-backup"; cron = "0 3 * * *"; grace = "30m"; }
          ];
        }
      '';
      description = ''
        Typed cadence configuration, serialised to YAML and passed as the
        first `-c` layer. Field names match `internal/config/types.go`
        (snake_case). The shape is enforced at eval time, so typos and
        missing required fields fail at `nixos-rebuild build` rather than
        at daemon startup.
      '';
    };

    extraConfigFiles = lib.mkOption {
      type = lib.types.listOf lib.types.path;
      default = [ ];
      example = lib.literalExpression ''[ "/run/cadence/secrets.yaml" ]'';
      description = ''
        Extra YAML files layered on top of {option}`settings`, in order. Use
        these for secret material (ping keys, API keys, webhook tokens) kept
        outside the Nix store; readability is the daemon user's responsibility.
        Field-level merge happens inside cadence — values here override the
        typed `settings` block on a per-field basis.
      '';
    };

    environmentFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      example = "/run/secrets/cadence.env";
      description = ''
        Optional systemd `EnvironmentFile` whose `KEY=value` lines are exposed
        to the daemon and resolvable via `''${env:KEY}` in YAML.
      '';
    };

    listen = lib.mkOption {
      type = lib.types.str;
      default = ":8080";
      description = ''
        Default listen address baked into the generated settings. Ignored if
        {option}`settings.server.listen` is set explicitly.
      '';
    };

    openFirewall = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        Open the TCP port parsed from {option}`listen` in the system firewall.
        Only meaningful for `:PORT` or `0.0.0.0:PORT` style addresses.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    assertions =
      let
        slugs = map (c: c.slug) cfg.settings.checks;
        channelNames = declaredChannels;
        pingKeyNames = declaredPingKeys;
        dupSlugs = duplicates slugs;
        dupChannels = duplicates channelNames;
        dupPingKeys = duplicates pingKeyNames;
        unknownDefaultPK = lib.filter (n: !(lib.elem n pingKeyNames)) cfg.settings.defaults.ping_keys;
        unknownDefaultCh = lib.filter (n: !(lib.elem n channelNames)) cfg.settings.defaults.channels;
        # Per-check reference errors collected into a single assertion to
        # keep the message readable.
        checkRefErrors = lib.flatten (map
          (c:
            (map (n: "check \"${c.slug}\".ping_keys references unknown \"${n}\"")
              (lib.filter (n: !(lib.elem n pingKeyNames)) c.ping_keys))
            ++ (map (n: "check \"${c.slug}\".channels references unknown \"${n}\"")
              (lib.filter (n: !(lib.elem n channelNames)) c.channels))
          )
          cfg.settings.checks);
        periodCronErrors = lib.flatten (map
          (c:
            let
              has = x: x != null;
              hasPeriod = has c.period;
              hasCron = has c.cron;
            in
            lib.optional (hasPeriod == hasCron)
              "check \"${c.slug}\" must specify exactly one of `period` or `cron`."
          )
          cfg.settings.checks);
      in
      [
        {
          assertion = dupSlugs == [ ];
          message = "services.cadence.settings.checks: duplicate slugs: ${lib.concatStringsSep ", " dupSlugs}.";
        }
        {
          assertion = dupChannels == [ ];
          message = "services.cadence.settings.channels: duplicate names: ${lib.concatStringsSep ", " dupChannels}.";
        }
        {
          assertion = dupPingKeys == [ ];
          message = "services.cadence.settings.ping_keys: duplicate names: ${lib.concatStringsSep ", " dupPingKeys}.";
        }
        {
          assertion = unknownDefaultPK == [ ];
          message = "services.cadence.settings.defaults.ping_keys references undefined ping_key(s): ${lib.concatStringsSep ", " unknownDefaultPK}.";
        }
        {
          assertion = unknownDefaultCh == [ ];
          message = "services.cadence.settings.defaults.channels references undefined channel(s): ${lib.concatStringsSep ", " unknownDefaultCh}.";
        }
        {
          assertion = checkRefErrors == [ ];
          message = "services.cadence.settings: " + lib.concatStringsSep "; " checkRefErrors;
        }
        {
          assertion = periodCronErrors == [ ];
          message = "services.cadence.settings: " + lib.concatStringsSep "; " periodCronErrors;
        }
      ];

    users.users = lib.mkIf (cfg.user == "cadence") {
      cadence = {
        isSystemUser = true;
        group = cfg.group;
        home = cfg.dataDir;
        description = "cadence monitoring daemon";
      };
    };

    users.groups = lib.mkIf (cfg.group == "cadence") {
      cadence = { };
    };

    services.cadence.settings = {
      data_dir = lib.mkDefault (toString cfg.dataDir);
      server.listen = lib.mkDefault cfg.listen;
    };

    # Ensure dataDir exists with the right ownership for any path the user
    # picks. StateDirectory only works for `/var/lib/<name>`-style paths,
    # so tmpfiles is the portable option.
    systemd.tmpfiles.rules = [
      "d ${cfg.dataDir} 0750 ${cfg.user} ${cfg.group} - -"
    ];

    networking.firewall.allowedTCPPorts =
      let
        # Split on the final ":" to extract host and port. Only open the
        # firewall when the bind address is reachable from outside —
        # "127.0.0.1:PORT" doesn't need a hole and is skipped.
        parts = lib.splitString ":" cfg.listen;
        portStr = lib.last parts;
        hostStr = lib.concatStringsSep ":" (lib.init parts);
        isPort = builtins.match "[0-9]+" portStr != null;
        publicHost = lib.elem hostStr [ "" "0.0.0.0" "[::]" "*" ];
        port = if isPort && publicHost then lib.toInt portStr else null;
      in
      lib.mkIf (cfg.openFirewall && port != null) [ port ];

    systemd.services.cadence = {
      description = "cadence monitoring daemon";
      wantedBy = [ "multi-user.target" ];
      after = [ "network.target" ];

      serviceConfig = {
        ExecStart = "${lib.getExe cfg.package} ${configArgs}";
        User = cfg.user;
        Group = cfg.group;
        WorkingDirectory = cfg.dataDir;
        Restart = "on-failure";
        RestartSec = "5s";
        EnvironmentFile = lib.mkIf (cfg.environmentFile != null) cfg.environmentFile;

        # Hardening — cadence only needs to read its config, write to dataDir,
        # and make outbound HTTP for webhook channels.
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectKernelLogs = true;
        ProtectControlGroups = true;
        ProtectClock = true;
        ProtectHostname = true;
        ProtectProc = "invisible";
        RestrictNamespaces = true;
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
        SystemCallArchitectures = "native";
        SystemCallFilter = [ "@system-service" "~@privileged" "~@resources" ];
        CapabilityBoundingSet = [ "" ];
        AmbientCapabilities = [ "" ];
        ReadWritePaths = [ cfg.dataDir ];
      };
    };
  };
}
