package config

import "github.com/google/uuid"

// DeriveUUID returns a stable, unguessable UUID for a check.
//
// Stable: same (salt, slug) always yields the same UUID — ping URLs built
// from it survive restarts and config rearrangement.
//
// Unguessable: derived from a secret salt, so the UUID functions as a
// credential rather than being trivially computable from the slug.
//
// Implementation: UUIDv5 chained — first the salt is hashed into a
// namespace UUID, then the slug is hashed under that namespace.
func DeriveUUID(salt, slug string) uuid.UUID {
	ns := uuid.NewSHA1(uuid.NameSpaceOID, []byte(salt))
	return uuid.NewSHA1(ns, []byte(slug))
}
