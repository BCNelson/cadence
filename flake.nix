{
  description = "cadence - self-hosted monitoring daemon";

  inputs = {
    nixpkgs.url = "github:cachix/devenv-nixpkgs/rolling";
    devenv.url = "github:cachix/devenv";
  };

  outputs = { self, nixpkgs, devenv, ... } @ inputs:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forEachSystem = nixpkgs.lib.genAttrs systems;
      # Stable version derived from git; tags/dirty state aren't visible to
      # the flake sandbox, so this stays "dev" unless overridden via override.
      version = "0.1.0";
      commit = self.shortRev or self.dirtyShortRev or "unknown";
    in
    {
      packages = forEachSystem (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          cadence = pkgs.callPackage ./nix/package.nix {
            inherit version commit;
          };
        in
        {
          inherit cadence;
          default = cadence;
        }
      );

      nixosModules = rec {
        cadence = import ./nix/module.nix { inherit self; };
        default = cadence;
      };

      checks = forEachSystem (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        # NixOS VM tests run under KVM; gate to Linux systems only.
        nixpkgs.lib.optionalAttrs (nixpkgs.lib.hasSuffix "-linux" system) {
          cadence-module = import ./nix/test.nix { inherit self pkgs; };
          cadence-module-eval = import ./nix/eval-failures.nix { inherit self pkgs; };
        }
      );

      devShells = forEachSystem (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        {
          default = devenv.lib.mkShell {
            inherit inputs pkgs;
            modules = [
              ./devenv.nix
            ];
          };
        }
      );
    };
}
