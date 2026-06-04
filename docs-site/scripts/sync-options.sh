#!/usr/bin/env bash
# Build the NixOS options JSON from nix/module.nix and copy it into the
# docs-site src/data/ directory so the Astro content loader can pick it up.
#
# Runs as a `prebuild` / `predev` step, but also safe to invoke directly.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo="$(cd "$here/.." && pwd)"
out="$here/src/data/nixos-options.json"

mkdir -p "$(dirname "$out")"

echo "Building cadence NixOS options JSON…"
result="$(nix build --no-link --print-out-paths "$repo#docs-options")"

cp "$result/options.json" "$out"
chmod u+w "$out"

echo "Wrote $out ($(jq 'keys | length' "$out") options)"
