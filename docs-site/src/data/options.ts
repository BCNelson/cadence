// Typed wrapper around the auto-generated nixos-options.json.
//
// The JSON is produced by `nix build .#docs-options` (see ../../scripts/
// sync-options.sh) and is gitignored — it's regenerated on every docs build
// from the descriptions in nix/module.nix.

import rawOptions from './nixos-options.json';

export interface NixOptionLiteral {
  _type: 'literalExpression' | 'literalMD';
  text: string;
}

export interface NixOptionDeclaration {
  name: string;
  url: string;
}

export interface NixOption {
  name: string;
  description?: string;
  type: string;
  default?: NixOptionLiteral;
  example?: NixOptionLiteral;
  declarations: NixOptionDeclaration[];
  loc: string[];
  readOnly: boolean;
  internal?: boolean;
  visible?: boolean;
}

// nixosOptionsDoc emits an object keyed by option path; flatten to a sorted
// list and inject the path as `name` so consumers don't have to keep both.
const entries = Object.entries(rawOptions as Record<string, Omit<NixOption, 'name'>>);

export const options: NixOption[] = entries
  .map(([name, body]) => ({ name, ...body }))
  .sort((a, b) => a.name.localeCompare(b.name));

export function optionsWithPrefix(prefix: string): NixOption[] {
  // Match either the prefix itself (the umbrella submodule option) or any
  // strict descendant. The * wildcard in option paths (e.g.
  // `services.cadence.settings.checks.*.slug`) is preserved as-is.
  const direct = prefix;
  const childPrefix = prefix + '.';
  return options.filter(o => o.name === direct || o.name.startsWith(childPrefix));
}

// Stable URL anchor for an option name. Lowercase, dots/asterisks/underscores
// → hyphens, collapse repeats.
export function optionAnchor(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
}
