// configtool validates and previews a cadence configuration without
// starting the daemon. It accepts the same -c flag layering as cadence
// itself, so CI can use it as a config lint gate.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/bcnelson/cadence/internal/config"
	"gopkg.in/yaml.v3"
)

// repeatableFlag collects every value of a flag that may appear more than
// once on the command line (here: -c).
type repeatableFlag []string

func (f *repeatableFlag) String() string     { return fmt.Sprintf("%v", *f) }
func (f *repeatableFlag) Set(v string) error { *f = append(*f, v); return nil }

func main() {
	var paths repeatableFlag
	flag.Var(&paths, "c", "configuration file or directory (repeat for layering, left -> right)")
	flag.Parse()

	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "configtool: validates and previews a cadence configuration")
		fmt.Fprintln(os.Stderr, "usage: configtool -c <config-path> [-c overlay.yaml ...]")
		flag.Usage()
		os.Exit(2)
	}

	reg, err := config.Load(paths, config.Options{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "configtool: %v\n", err)
		os.Exit(1)
	}

	if err := printResolved(reg); err != nil {
		fmt.Fprintf(os.Stderr, "configtool: render: %v\n", err)
		os.Exit(1)
	}
}

// printResolved emits a YAML view of the resolved registry. Ping-key
// secrets are masked because operators frequently paste configtool output
// into chat / PRs.
func printResolved(reg *config.Registry) error {
	view := map[string]any{
		"server":    reg.Server,
		"data_dir":  reg.DataDir,
		"retention": reg.Retention,
		"ping_keys": maskedKeys(reg.PingKeys),
		"channels":  sortedChannels(reg.Channels),
		"checks":    sortedChecks(reg.Checks),
	}
	enc := yaml.NewEncoder(os.Stdout)
	enc.SetIndent(2)
	defer enc.Close() //nolint:errcheck // best-effort flush on stdout
	return enc.Encode(view)
}

func maskedKeys(in map[string]string) []map[string]string {
	names := make([]string, 0, len(in))
	for n := range in {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]map[string]string, 0, len(names))
	for _, n := range names {
		out = append(out, map[string]string{"name": n, "key": "***"})
	}
	return out
}

func sortedChannels(in map[string]config.Channel) []config.Channel {
	names := make([]string, 0, len(in))
	for n := range in {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]config.Channel, 0, len(names))
	for _, n := range names {
		out = append(out, in[n])
	}
	return out
}

func sortedChecks(in map[string]*config.ResolvedCheck) []map[string]any {
	slugs := make([]string, 0, len(in))
	for s := range in {
		slugs = append(slugs, s)
	}
	sort.Strings(slugs)
	out := make([]map[string]any, 0, len(slugs))
	for _, s := range slugs {
		c := in[s]
		row := map[string]any{
			"slug":    c.Slug,
			"uuid":    c.UUID.String(),
			"enabled": c.Enabled,
		}
		if c.Name != "" {
			row["name"] = c.Name
		}
		if c.Period > 0 {
			row["period"] = c.Period.String()
		}
		if c.Cron != "" {
			row["cron"] = c.Cron
		}
		if c.Grace > 0 {
			row["grace"] = c.Grace.String()
		}
		if c.Timeout > 0 {
			row["timeout"] = c.Timeout.String()
		}
		if len(c.PingKeys) > 0 {
			row["ping_keys"] = c.PingKeys
		}
		if len(c.Channels) > 0 {
			row["channels"] = c.Channels
		}
		if len(c.Tags) > 0 {
			row["tags"] = c.Tags
		}
		if c.PinnedUUID {
			row["uuid_pinned"] = true
		}
		// Per the design rule: a check with no ping_keys after resolution
		// (neither declared nor inherited) is "open" — UUID-only access.
		// Surface it explicitly so an accidental `ping_keys: []` shows up
		// at a glance instead of hiding behind an empty list.
		if len(c.PingKeys) == 0 {
			row["access"] = "open (UUID-only)"
		}
		if names := inheritedNames(c.Inherited); len(names) > 0 {
			row["inherited_from_defaults"] = names
		}
		out = append(out, row)
	}
	return out
}

// inheritedNames returns the sorted list of inheritable field names that
// took their value from the global defaults block.
func inheritedNames(in config.Inherited) []string {
	var out []string
	if in.Grace {
		out = append(out, "grace")
	}
	if in.Timeout {
		out = append(out, "timeout")
	}
	if in.PingKeys {
		out = append(out, "ping_keys")
	}
	if in.Channels {
		out = append(out, "channels")
	}
	return out
}
