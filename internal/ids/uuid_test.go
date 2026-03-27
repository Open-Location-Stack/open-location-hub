package ids

import (
	"testing"
	"time"
)

func TestNewUUIDReturnsVersion7(t *testing.T) {
	t.Parallel()

	id := NewUUID()
	if got := id.Version(); got != 7 {
		t.Fatalf("expected version 7 UUID, got version %d", got)
	}
}

func TestNewStringIsTimeSortable(t *testing.T) {
	t.Parallel()

	first := NewString()
	time.Sleep(2 * time.Millisecond)
	second := NewString()
	time.Sleep(2 * time.Millisecond)
	third := NewString()

	if !(first < second && second < third) {
		t.Fatalf("expected lexical order to follow creation time, got %q, %q, %q", first, second, third)
	}
}
