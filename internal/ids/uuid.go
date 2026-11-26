package ids

import (
	"encoding/binary"
	"time"

	"github.com/google/uuid"
)

// NewUUID returns a time-sortable UUIDv7 for hub-issued identifiers.
// Keep generation behind this package so the repository can switch to a future
// Go standard-library UUIDv7 implementation with one localized change.
func NewUUID() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}

// NewString returns the canonical string form of a time-sortable UUIDv7.
func NewString() string {
	return NewUUID().String()
}

// EventUUID maps an event's timestamp and stable identity to a UUIDv7.
func EventUUID(at time.Time, identity [32]byte) uuid.UUID {
	var id uuid.UUID
	binary.BigEndian.PutUint64(id[:8], uint64(at.UnixMilli())<<16)
	copy(id[6:], identity[:10])
	id[6] = (id[6] & 0x0f) | 0x70
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}
