{ self, pkgs }:

let
  lib = pkgs.lib;

  # Build a NixOS system with the cadence module plus an extra config
  # snippet. Wrapped in tryEval so a type / required-field error doesn't
  # nuke the whole derivation evaluation.
  evalCase = extraModule:
    let
      sys = pkgs.nixos {
        imports = [
          self.nixosModules.cadence
          ({ ... }: extraModule)
        ];
      };
      probe = builtins.tryEval (
        lib.deepSeq sys.config.systemd.services.cadence.serviceConfig.ExecStart
          sys.config.assertions
      );
      failedAsserts =
        if probe.success
        then lib.filter (a: !a.assertion) probe.value
        else [ ];
    in
    {
      threw = !probe.success;
      assertionFired = failedAsserts != [ ];
      firstAssertionMessage =
        if failedAsserts == [ ]
        then null
        else (lib.head failedAsserts).message;
    };

  # Each case must fail to eval (typed-schema rejection) OR fire a module
  # assertion. Anything else is a test failure.
  cases = {
    missing-uuid-salt = {
      services.cadence = {
        enable = true;
        settings.checks = [{ slug = "x"; period = "5m"; }];
      };
    };

    unknown-field-typo = {
      services.cadence = {
        enable = true;
        settings = {
          server.uuid_salt = "x";
          banana = 1; # not a real cadence field
        };
      };
    };

    period-and-cron-both-set = {
      services.cadence = {
        enable = true;
        settings = {
          server.uuid_salt = "x";
          checks = [{ slug = "x"; period = "5m"; cron = "0 * * * *"; }];
        };
      };
    };

    period-and-cron-neither-set = {
      services.cadence = {
        enable = true;
        settings = {
          server.uuid_salt = "x";
          checks = [{ slug = "x"; }];
        };
      };
    };

    duplicate-slugs = {
      services.cadence = {
        enable = true;
        settings = {
          server.uuid_salt = "x";
          checks = [
            { slug = "dup"; period = "5m"; }
            { slug = "dup"; period = "10m"; }
          ];
        };
      };
    };

    unknown-ping-key-reference = {
      services.cadence = {
        enable = true;
        settings = {
          server.uuid_salt = "x";
          checks = [{ slug = "x"; period = "5m"; ping_keys = [ "ghost" ]; }];
        };
      };
    };

    unknown-channel-reference = {
      services.cadence = {
        enable = true;
        settings = {
          server.uuid_salt = "x";
          checks = [{ slug = "x"; period = "5m"; channels = [ "ghost" ]; }];
        };
      };
    };

    bad-duration-format = {
      services.cadence = {
        enable = true;
        settings = {
          server.uuid_salt = "x";
          checks = [{ slug = "x"; period = "five-minutes"; }];
        };
      };
    };

    bad-slug-format = {
      services.cadence = {
        enable = true;
        settings = {
          server.uuid_salt = "x";
          checks = [{ slug = "has spaces"; period = "5m"; }];
        };
      };
    };

    bad-channel-type = {
      services.cadence = {
        enable = true;
        settings = {
          server.uuid_salt = "x";
          channels = [{ name = "x"; type = "carrier-pigeon"; url = "https://x"; }];
        };
      };
    };
  };

  results = lib.mapAttrs (name: cfg:
    let r = evalCase cfg;
    in
    if r.threw then "OK ${name}: type/option eval threw"
    else if r.assertionFired then "OK ${name}: assertion fired (${r.firstAssertionMessage})"
    else "FAIL ${name}: expected eval rejection but config evaluated cleanly"
  ) cases;

  summary = lib.concatStringsSep "\n" (lib.attrValues results) + "\n";
  anyFail = lib.any (s: lib.hasPrefix "FAIL " s) (lib.attrValues results);
in
pkgs.runCommand "cadence-module-eval-failures"
{
  passAsFile = [ "summary" ];
  inherit summary;
} (
  if anyFail
  then ''
    echo '== cadence module eval-failure suite =='
    cat "$summaryPath"
    echo
    echo 'one or more bad configs unexpectedly evaluated successfully'
    exit 1
  ''
  else ''
    echo '== cadence module eval-failure suite =='
    cat "$summaryPath"
    cp "$summaryPath" $out
  ''
)
