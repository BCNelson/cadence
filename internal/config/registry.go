package config

import (
	"time"

	"github.com/google/uuid"
)

// Registry is the immutable, resolved view consumed by the rest of the system.
// All defaults are applied and every check has a stable UUID. Lookups are by
// slug (globally unique) or by UUID.
type Registry struct {
	Server    Server
	DataDir   string
	Retention Retention

	// PingKeys maps key name to raw secret. Empty if none configured.
	PingKeys map[string]string

	// Channels maps channel name to channel definition.
	Channels map[string]Channel

	// Checks maps slug to resolved check. UUIDs are unique across all checks.
	Checks map[string]*ResolvedCheck

	bySlug map[string]*ResolvedCheck
	byUUID map[string]*ResolvedCheck
}

// ResolvedCheck is a Check with defaults applied and UUID derived.
type ResolvedCheck struct {
	Slug     string
	Name     string
	UUID     uuid.UUID
	Period   time.Duration
	Cron     string
	Grace    time.Duration
	Timeout  time.Duration
	PingKeys []string
	Channels []string
	Tags     []string
	Enabled  bool

	// PinnedUUID is true if the UUID came from the check's `uuid:` field
	// rather than being derived. UUID-form pings against a check with a
	// pinned UUID are authorized regardless of ping_keys.
	PinnedUUID bool

	// Inherited records which inheritable fields took their value from
	// `defaults:` rather than the check itself. Used by configtool to
	// surface where each field came from, so operators can see overlay
	// effects without diffing YAML by hand.
	Inherited Inherited
}

// Inherited flags inheritable fields whose value came from the global
// `defaults:` block. False means the check declared the value itself.
type Inherited struct {
	Grace    bool
	Timeout  bool
	PingKeys bool
	Channels bool
}

// CheckBySlug returns the check with the given slug, or nil.
func (r *Registry) CheckBySlug(slug string) *ResolvedCheck {
	return r.bySlug[slug]
}

// CheckByUUID returns the check with the given UUID, or nil.
func (r *Registry) CheckByUUID(u uuid.UUID) *ResolvedCheck {
	return r.byUUID[u.String()]
}
