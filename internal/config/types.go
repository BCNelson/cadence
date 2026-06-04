// Package config loads cadence's YAML configuration.
//
// Pipeline: parse each layer (with recursive imports) -> deep-merge layers
// left-to-right -> interpolate (${file:...} then ${env:...}) -> validate ->
// resolve into a Registry. Definitions live in YAML; LevelDB never holds them.
package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the parsed-and-merged YAML, before defaults are applied and
// UUIDs are derived. Use Registry for the resolved, validated view.
type Config struct {
	Server    Server    `yaml:"server"`
	DataDir   string    `yaml:"data_dir"`
	Retention Retention `yaml:"retention"`
	PingKeys  []PingKey `yaml:"ping_keys"`
	Defaults  Defaults  `yaml:"defaults"`
	Channels  []Channel `yaml:"channels"`
	Checks    []Check   `yaml:"checks"`
}

type Server struct {
	Listen   string  `yaml:"listen"`
	BaseURL  string  `yaml:"base_url"`
	UUIDSalt string  `yaml:"uuid_salt"`
	APIKeys  APIKeys `yaml:"api_keys"`
}

type APIKeys struct {
	ReadWrite []string `yaml:"read_write"`
	ReadOnly  []string `yaml:"read_only"`
}

type Retention struct {
	Pings  int `yaml:"pings"`
	Events int `yaml:"events"`
}

type PingKey struct {
	Name string `yaml:"name"`
	Key  string `yaml:"key"`
}

type Defaults struct {
	Grace    Duration `yaml:"grace"`
	Timeout  Duration `yaml:"timeout"`
	PingKeys []string `yaml:"ping_keys"`
	Channels []string `yaml:"channels"`
}

type Channel struct {
	Name    string            `yaml:"name"`
	Type    string            `yaml:"type"`
	URL     string            `yaml:"url"`
	Method  string            `yaml:"method"`
	Headers map[string]string `yaml:"headers"`
}

// Check is a single declared check, before defaults are applied and UUID is
// derived. Period XOR Cron must be set (validated in resolve).
type Check struct {
	Slug     string   `yaml:"slug"`
	Name     string   `yaml:"name"`
	Period   Duration `yaml:"period"`
	Cron     string   `yaml:"cron"`
	Grace    Duration `yaml:"grace"`
	Timeout  Duration `yaml:"timeout"`
	PingKeys []string `yaml:"ping_keys"`
	Channels []string `yaml:"channels"`
	Tags     []string `yaml:"tags"`
	Enabled  *bool    `yaml:"enabled"`
	UUID     string   `yaml:"uuid"`
}

// Duration accepts Go duration strings (e.g. "5m", "1h", "24h"). YAML ints
// are rejected so a bare "60" is never silently treated as nanoseconds.
type Duration time.Duration

func (d Duration) String() string     { return time.Duration(d).String() }
func (d Duration) Std() time.Duration { return time.Duration(d) }

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("duration must be a string like \"5m\", got %v at line %d", kindName(n.Kind), n.Line)
	}
	if n.Value == "" {
		*d = 0
		return nil
	}
	parsed, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q at line %d: %w", n.Value, n.Line, err)
	}
	*d = Duration(parsed)
	return nil
}

func kindName(k yaml.Kind) string {
	switch k {
	case yaml.ScalarNode:
		return "scalar"
	case yaml.SequenceNode:
		return "sequence"
	case yaml.MappingNode:
		return "mapping"
	case yaml.DocumentNode:
		return "document"
	case yaml.AliasNode:
		return "alias"
	}
	return "unknown"
}
