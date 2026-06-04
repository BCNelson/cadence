{ self, pkgs }:

pkgs.testers.runNixOSTest {
  name = "cadence-module";

  nodes.machine = { config, ... }: {
    imports = [ self.nixosModules.cadence ];

    # Environment file is normally a secret; for the test we write it
    # directly via environment.etc so the VM can find it on boot.
    environment.etc."cadence/env".text = ''
      CADENCE_UUID_SALT=salt-from-env-file
    '';

    # Layered config: `settings` is the base; `extraConfigFiles` is layered
    # on top (so a user's secret file can override). The test relies on
    # this ordering by putting a SLUG-bearing check ONLY in the extra file
    # and asserting it shows up in /api/v1/checks.
    environment.etc."cadence/extra.yaml".text = ''
      checks:
        - slug: from-extra-file
          period: 5m
    '';

    services.cadence = {
      enable = true;
      listen = "127.0.0.1:8765";
      # Deliberately NOT /var/lib/cadence — exercises the override path so
      # tmpfiles creates and chowns the directory rather than relying on
      # systemd's StateDirectory shorthand.
      dataDir = "/srv/cadence-data";
      environmentFile = "/etc/cadence/env";
      extraConfigFiles = [ "/etc/cadence/extra.yaml" ];
      settings = {
        # ${env:...} interpolation has to resolve through systemd's
        # EnvironmentFile for the daemon to start at all (uuid_salt is
        # required).
        server = {
          uuid_salt = "\${env:CADENCE_UUID_SALT}";
          api_keys.read_only = [ "test-ro-key" ];
        };
        retention = { pings = 10; events = 10; };
        checks = [
          { slug = "from-settings"; period = "1m"; }
        ];
      };
    };

    environment.systemPackages = [ pkgs.curl pkgs.jq ];
  };

  testScript = ''
    machine.wait_for_unit("cadence.service")
    machine.wait_for_open_port(8765)

    # Liveness.
    machine.succeed("curl -fsS http://127.0.0.1:8765/healthz | grep -q ok")

    # dataDir override: directory was created at the custom path with
    # service-user ownership, NOT at the default /var/lib/cadence.
    machine.succeed("test -d /srv/cadence-data")
    machine.succeed("stat -c '%U:%G' /srv/cadence-data | grep -q '^cadence:cadence$'")
    machine.succeed("stat -c '%a' /srv/cadence-data | grep -q '^750$'")
    machine.fail("test -e /var/lib/cadence")
    # And the LevelDB store actually landed there — `CURRENT` is the
    # sentinel goleveldb writes on first open.
    machine.succeed("test -f /srv/cadence-data/CURRENT")

    # The settings file is in the Nix store and world-readable (no secrets).
    machine.succeed(
        "systemctl show cadence.service -p ExecStart --value | grep -q '/nix/store/'"
    )

    # Both checks are present — proves `settings` and `extraConfigFiles`
    # were both loaded and merged. (If the env-file interpolation had
    # failed, the daemon would have refused to start on a missing
    # uuid_salt, so this also proves env interpolation works.)
    out = machine.succeed(
        "curl -fsS -H 'X-Api-Key: test-ro-key' "
        "http://127.0.0.1:8765/api/v3/checks/ | jq -r '.checks[].slug' | sort"
    )
    assert "from-settings" in out, f"missing 'from-settings' check: {out!r}"
    assert "from-extra-file" in out, f"missing 'from-extra-file' check: {out!r}"

    # systemd hardening sanity: confirm a few of the load-bearing knobs are
    # actually applied (catches accidental regressions from option churn).
    for prop in [
        "NoNewPrivileges=yes",
        "ProtectSystem=strict",
        "MemoryDenyWriteExecute=yes",
        "PrivateTmp=yes",
    ]:
        machine.succeed(
            f"systemctl show cadence.service -p {prop.split('=')[0]} --value "
            f"| grep -q '^{prop.split('=')[1]}$'"
        )
  '';
}
