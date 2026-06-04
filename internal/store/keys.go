package store

import (
	"encoding/binary"
	"time"

	"github.com/google/uuid"
)

// Key layout. All keys are UUID-namespaced so a slug rename (new UUID)
// starts a fresh series.
//
//   s/<uuid>                       -> CheckState           (single key)
//   p/<uuid>/<be-int64-nanos>      -> Ping                 (capped ring)
//   e/<uuid>/<be-int64-nanos>      -> Event                (capped ring)
//   l/<uuid>/<be-int64-nanos>      -> LogEntry             (referenced by Ping.BodyKey)
//
// The big-endian nanos encoding sorts lexicographically the same way it
// sorts numerically, so LevelDB iteration is in time order without a
// separate index. uuid bytes are the canonical 16-byte form (not the
// 36-char string) to keep keys compact.

const (
	prefixState = "s/"
	prefixPing  = "p/"
	prefixEvent = "e/"
	prefixLog   = "l/"
)

func stateKey(u uuid.UUID) []byte {
	out := make([]byte, 0, len(prefixState)+16)
	out = append(out, prefixState...)
	out = append(out, u[:]...)
	return out
}

func tsKey(prefix string, u uuid.UUID, t time.Time) []byte {
	out := make([]byte, len(prefix)+16+8)
	copy(out, prefix)
	copy(out[len(prefix):], u[:])
	binary.BigEndian.PutUint64(out[len(prefix)+16:], uint64(t.UnixNano())) //nolint:gosec // intentional cast
	return out
}

func tsKeyPrefix(prefix string, u uuid.UUID) []byte {
	out := make([]byte, len(prefix)+16)
	copy(out, prefix)
	copy(out[len(prefix):], u[:])
	return out
}

// keyRange returns the [start, limit) byte range for all entries under
// (prefix, uuid). LevelDB iterators use a half-open range so limit is the
// successor of the longest matching key.
func keyRange(prefix string, u uuid.UUID) (start, limit []byte) {
	start = tsKeyPrefix(prefix, u)
	limit = append([]byte(nil), start...)
	// Successor: bump the last byte of the uuid; if it overflows, ripple.
	for i := len(limit) - 1; i >= 0; i-- {
		limit[i]++
		if limit[i] != 0 {
			return start, limit
		}
	}
	// All bytes overflowed — caller would have to use nil end. Not reachable
	// for real UUIDs because they're 16 bytes preceded by a printable prefix.
	return start, nil
}
