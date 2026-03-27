package ids

import "github.com/google/uuid"

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
