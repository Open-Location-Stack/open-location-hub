package hub

import (
	"context"
	"testing"
	"time"
)

func TestDeadlineSchedulerReplacesAndCancelsTimers(t *testing.T) {
	now := time.Now()
	s := newDeadlineScheduler(func() time.Time { return now })
	fired := []string{}
	s.schedule("replace", now.Add(time.Second), func() { t.Error("superseded callback fired") })
	s.schedule("replace", now.Add(3*time.Second), func() { fired = append(fired, "replace") })
	s.schedule("cancel", now, func() { t.Error("cancelled callback fired") })
	s.schedule("cancel", time.Time{}, nil)
	s.schedule("first", now.Add(2*time.Second), func() {
		fired = append(fired, "first")
		// Callbacks must be able to schedule other timers without deadlocking.
		s.schedule("nested", now, func() { fired = append(fired, "nested") })
	})
	now = now.Add(2 * time.Second)
	s.runDue()
	if len(fired) != 2 || fired[0] != "first" || fired[1] != "nested" {
		t.Fatalf("unexpected callbacks: %v", fired)
	}
	now = now.Add(time.Second)
	s.runDue()
	if len(fired) != 3 || fired[2] != "replace" || len(s.items) != 0 || len(s.byKey) != 0 {
		t.Fatalf("replacement or cleanup failed: %v", fired)
	}
}

func TestDeadlineSchedulerWakesForEarlierDeadlineAndStops(t *testing.T) {
	s := newDeadlineScheduler(time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { s.run(ctx); close(done) }()
	s.schedule("later", time.Now().Add(time.Hour), func() { t.Error("future timer fired") })
	fired := make(chan struct{})
	s.schedule("soon", time.Now().Add(10*time.Millisecond), func() { close(fired) })
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not wake for earlier deadline")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop")
	}
}
