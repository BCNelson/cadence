{ pkgs, lib, config, ... }:

{
  devenv.root =
    let
      devenvRoot = builtins.getEnv "DEVENV_ROOT";
    in
    if devenvRoot != "" then devenvRoot else builtins.toString ./.;

  languages.go.enable = true;

  languages.javascript = {
    enable = true;
    package = pkgs.nodejs_22;
    npm.enable = true;
  };

  packages = with pkgs; [
    gotools
    golangci-lint
    delve
    git
    just
    air
  ];

  env = {
    GOPATH = "${config.env.DEVENV_STATE}/go";
    GOCACHE = "${config.env.DEVENV_STATE}/go-cache";
    GOMODCACHE = "${config.env.DEVENV_STATE}/go-mod-cache";
    NODE_ENV = "development";
  };

  dotenv.enable = true;

  enterShell = ''
    echo "cadence dev shell"
    echo "Go $(go version | cut -d' ' -f3)"
    echo "Node.js $(node --version)"
  '';

  git-hooks.hooks = {
    gofmt.enable = true;
    govet.enable = true;
  };
}
