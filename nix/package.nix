{
  lib,
  buildGoModule,
  buildNpmPackage,
  nodejs_22,
  nix-gitignore,
  version ? "dev",
  commit ? "unknown",
  buildDate ? "1970-01-01T00:00:00Z",
}:

let
  src = nix-gitignore.gitignoreSource [ ] ../.;

  frontend = buildNpmPackage {
    pname = "cadence-frontend";
    inherit version;
    src = "${src}/frontend";
    nodejs = nodejs_22;

    # Recompute with `nix build .#cadence` and copy the suggested hash.
    npmDepsHash = "sha256-rMB3W+LZOHM/GBenk72pnNfB1LBDeuljYRKz/GPE0CQ=";

    # Playwright is only needed for e2e tests; skip browser download during
    # the package install so the build stays hermetic.
    env.PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD = "1";
    env.PLAYWRIGHT_SKIP_VALIDATE_HOST_REQUIREMENTS = "true";

    installPhase = ''
      runHook preInstall
      mkdir -p $out
      cp -r dist/. $out/
      runHook postInstall
    '';
  };
in
buildGoModule {
  pname = "cadence";
  inherit version;
  inherit src;

  # Recompute with `nix build .#cadence` and copy the suggested hash.
  vendorHash = "sha256-PJsCOi9NmXfzzmI5rlv0iRZaCraFGP07iav4Y4NIg8E=";

  subPackages = [ "cmd/cadence" ];

  # Pure-Go deps (no cgo) — keep parity with the Dockerfile.
  env.CGO_ENABLED = "0";

  # Drop the placeholder dist files committed for `go build` ergonomics
  # and replace them with the real bundle produced by the frontend
  # derivation above.
  preBuild = ''
    rm -rf internal/web/dist
    mkdir -p internal/web/dist
    cp -r ${frontend}/. internal/web/dist/
  '';

  ldflags = [
    "-s"
    "-w"
    "-X main.version=${version}"
    "-X main.commit=${commit}"
    "-X main.buildDate=${buildDate}"
  ];

  doCheck = false;

  passthru = {
    inherit frontend;
  };

  meta = {
    description = "Self-hosted Healthchecks.io-style monitor with YAML config and embedded React dashboard";
    homepage = "https://github.com/bcnelson/cadence";
    license = lib.licenses.mit;
    mainProgram = "cadence";
    platforms = lib.platforms.unix;
  };
}
