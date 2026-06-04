package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

// Options control loader behavior. Zero value gives the production defaults.
type Options struct {
	// Env overrides os.LookupEnv during interpolation. Used by tests.
	Env envLookup
}

// Load reads and resolves config from one or more layer paths and returns
// the immutable Registry consumed by the rest of the system. Each path may
// be a file or a directory (expanded to its *.yaml children sorted
// lexically). Layers merge left-to-right: the rightmost path wins.
func Load(paths []string, opts Options) (*Registry, error) {
	if len(paths) == 0 {
		return nil, errors.New("config: no paths provided")
	}
	env := opts.Env
	if env == nil {
		env = os.LookupEnv
	}

	merged := map[string]any{}
	for _, p := range paths {
		layer, err := loadLayer(p, nil)
		if err != nil {
			return nil, err
		}
		merged = mergeMaps(merged, layer)
	}

	resolved, err := interpolate(merged, env)
	if err != nil {
		return nil, err
	}

	cfg, err := decodeConfig(resolved)
	if err != nil {
		return nil, err
	}

	return resolve(cfg)
}

// decodeConfig re-marshals the interpolated tree through YAML to produce a
// typed Config. The round-trip is cheap and lets the typed structs handle
// duration parsing, field name matching, and strict type checks without us
// hand-rolling map-to-struct reflection. KnownFields catches typos in
// user-facing field names.
func decodeConfig(tree any) (*Config, error) {
	raw, err := yaml.Marshal(tree)
	if err != nil {
		return nil, fmt.Errorf("config: re-marshal merged tree: %w", err)
	}
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: decode into typed config: %w", err)
	}
	return &cfg, nil
}

// resolve validates the config, applies defaults, and builds the Registry.
func resolve(cfg *Config) (*Registry, error) {
	if cfg.Server.UUIDSalt == "" {
		return nil, errors.New("config: server.uuid_salt is required")
	}

	reg := &Registry{
		Server:    cfg.Server,
		DataDir:   cfg.DataDir,
		Retention: cfg.Retention,
		PingKeys:  make(map[string]string, len(cfg.PingKeys)),
		Channels:  make(map[string]Channel, len(cfg.Channels)),
		Checks:    make(map[string]*ResolvedCheck, len(cfg.Checks)),
		bySlug:    make(map[string]*ResolvedCheck, len(cfg.Checks)),
		byUUID:    make(map[string]*ResolvedCheck, len(cfg.Checks)),
	}

	for i, pk := range cfg.PingKeys {
		if pk.Name == "" {
			return nil, fmt.Errorf("config: ping_keys[%d]: name is required", i)
		}
		if pk.Key == "" {
			return nil, fmt.Errorf("config: ping_keys[%q]: key is required", pk.Name)
		}
		if _, dup := reg.PingKeys[pk.Name]; dup {
			return nil, fmt.Errorf("config: duplicate ping_key name %q", pk.Name)
		}
		reg.PingKeys[pk.Name] = pk.Key
	}

	for i, ch := range cfg.Channels {
		if ch.Name == "" {
			return nil, fmt.Errorf("config: channels[%d]: name is required", i)
		}
		if _, dup := reg.Channels[ch.Name]; dup {
			return nil, fmt.Errorf("config: duplicate channel name %q", ch.Name)
		}
		reg.Channels[ch.Name] = ch
	}

	for _, name := range cfg.Defaults.PingKeys {
		if _, ok := reg.PingKeys[name]; !ok {
			return nil, fmt.Errorf("config: defaults.ping_keys references unknown ping_key %q", name)
		}
	}
	for _, name := range cfg.Defaults.Channels {
		if _, ok := reg.Channels[name]; !ok {
			return nil, fmt.Errorf("config: defaults.channels references unknown channel %q", name)
		}
	}

	seenUUIDs := make(map[string]string, len(cfg.Checks))
	for i := range cfg.Checks {
		rc, err := resolveCheck(&cfg.Checks[i], &cfg.Defaults, reg)
		if err != nil {
			return nil, err
		}
		if existingSlug, dup := seenUUIDs[rc.UUID.String()]; dup {
			return nil, fmt.Errorf("config: checks %q and %q resolve to the same UUID %s — pinned uuid: collision?",
				existingSlug, rc.Slug, rc.UUID)
		}
		seenUUIDs[rc.UUID.String()] = rc.Slug
		reg.Checks[rc.Slug] = rc
		reg.bySlug[rc.Slug] = rc
		reg.byUUID[rc.UUID.String()] = rc
	}

	return reg, nil
}

func resolveCheck(c *Check, def *Defaults, reg *Registry) (*ResolvedCheck, error) {
	if c.Slug == "" {
		return nil, errors.New("config: check missing required slug")
	}
	if _, dup := reg.bySlug[c.Slug]; dup {
		return nil, fmt.Errorf("config: duplicate check slug %q", c.Slug)
	}

	hasPeriod := c.Period > 0
	hasCron := c.Cron != ""
	if hasPeriod == hasCron {
		return nil, fmt.Errorf("config: check %q must specify exactly one of period or cron", c.Slug)
	}

	pingKeys := c.PingKeys
	if pingKeys == nil {
		pingKeys = def.PingKeys
	}
	for _, name := range pingKeys {
		if _, ok := reg.PingKeys[name]; !ok {
			return nil, fmt.Errorf("config: check %q references unknown ping_key %q", c.Slug, name)
		}
	}

	channels := c.Channels
	if channels == nil {
		channels = def.Channels
	}
	for _, name := range channels {
		if _, ok := reg.Channels[name]; !ok {
			return nil, fmt.Errorf("config: check %q references unknown channel %q", c.Slug, name)
		}
	}

	grace := time.Duration(c.Grace)
	if grace == 0 {
		grace = time.Duration(def.Grace)
	}
	timeout := time.Duration(c.Timeout)
	if timeout == 0 {
		timeout = time.Duration(def.Timeout)
	}

	enabled := true
	if c.Enabled != nil {
		enabled = *c.Enabled
	}

	var u uuid.UUID
	pinned := false
	if c.UUID != "" {
		parsed, err := uuid.Parse(c.UUID)
		if err != nil {
			return nil, fmt.Errorf("config: check %q has invalid pinned uuid: %w", c.Slug, err)
		}
		u = parsed
		pinned = true
	} else {
		u = DeriveUUID(reg.Server.UUIDSalt, c.Slug)
	}

	return &ResolvedCheck{
		Slug:       c.Slug,
		Name:       c.Name,
		UUID:       u,
		Period:     time.Duration(c.Period),
		Cron:       c.Cron,
		Grace:      grace,
		Timeout:    timeout,
		PingKeys:   pingKeys,
		Channels:   channels,
		Tags:       c.Tags,
		Enabled:    enabled,
		PinnedUUID: pinned,
	}, nil
}
