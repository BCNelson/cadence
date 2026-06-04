{ self }:
{ pkgs }:

let
  inherit (pkgs) lib;

  # Evaluate the cadence module on its own. We don't need a full NixOS eval —
  # nixosOptionsDoc only inspects option declarations, not `config`. The
  # `mkIf cfg.enable` guard inside the module keeps the systemd / users
  # config branches from being forced. `_module.check = false` lets the
  # module assign to options outside its own tree (systemd.services, etc.)
  # without needing the surrounding NixOS module set imported.
  eval = lib.evalModules {
    modules = [
      (import "${self}/nix/module.nix" { inherit self; })
      { _module.check = false; }
    ];
    specialArgs = { inherit pkgs; };
  };

  repoBlob = "https://github.com/bcnelson/cadence/blob/main";

  optionsDoc = pkgs.nixosOptionsDoc {
    # Restrict the doc to services.cadence.* — everything else in the
    # evaluated tree is either internal (_module) or accidental.
    options = { services.cadence = eval.options.services.cadence; };

    # Every cadence option is declared in nix/module.nix. The
    # `lib.evalModules` standalone path drops per-option declaration paths,
    # so point them all at the file and let the reader ctrl-F by name.
    transformOptions = opt: opt // {
      declarations = [{
        name = "nix/module.nix";
        url = "${repoBlob}/nix/module.nix";
      }];
    };
  };
in
pkgs.runCommand "cadence-options-json" { } ''
  mkdir -p $out
  cp ${optionsDoc.optionsJSON}/share/doc/nixos/options.json $out/options.json
''
