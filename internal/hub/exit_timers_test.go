package hub

import (
	"testing"
	"time"
)

func TestExitTimerTransitionTable(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	oldTolerance := now.Add(3 * time.Second)
	oldOutside := now.Add(time.Second)
	policy := exitTimerPolicy{fenceTimeout: 20 * time.Second, toleranceTimeout: 5 * time.Second, exitDelay: 2 * time.Second}
	for _, previous := range []exitArea{exitInside, exitTolerance, exitOutside} {
		for _, current := range []exitArea{exitInside, exitTolerance, exitOutside} {
			state := exitTimers{area: previous, toleranceDeadline: oldTolerance, outsideDeadline: oldOutside}
			next := state.advance(now, current, policy)
			if !next.fenceDeadline.Equal(now.Add(20 * time.Second)) {
				t.Fatal("every observation must refresh the fence deadline")
			}
			var wantTolerance, wantOutside time.Time
			if current != exitInside {
				wantTolerance = oldTolerance
				if previous == exitInside {
					wantTolerance = now.Add(5 * time.Second)
				}
			}
			if current == exitOutside {
				wantOutside = oldOutside
				if previous != exitOutside {
					wantOutside = now.Add(2 * time.Second)
				}
			}
			if !next.toleranceDeadline.Equal(wantTolerance) || !next.outsideDeadline.Equal(wantOutside) {
				t.Errorf("transition %d -> %d: got %+v want tolerance=%s outside=%s", previous, current, next, wantTolerance, wantOutside)
			}
		}
	}
}

func TestExitTimerInfiniteImmediateAndMinimum(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	infinite := exitTimerPolicy{fenceTimeout: -1, toleranceTimeout: -1, exitDelay: -1}
	for _, area := range []exitArea{exitInside, exitTolerance, exitOutside} {
		if deadline := (exitTimers{}).advance(now, area, infinite).deadline(); !deadline.IsZero() {
			t.Fatalf("infinite policy produced deadline %s", deadline)
		}
	}
	immediate := infinite
	immediate.exitDelay = 0
	if got := (exitTimers{}).advance(now, exitOutside, immediate).deadline(); !got.Equal(now) {
		t.Fatalf("zero delay must be immediate: %s", got)
	}
	policy := exitTimerPolicy{fenceTimeout: time.Second, toleranceTimeout: 2 * time.Second, exitDelay: 3 * time.Second}
	if got := (exitTimers{}).advance(now, exitOutside, policy).deadline(); !got.Equal(now.Add(time.Second)) {
		t.Fatalf("earliest timer must win: %s", got)
	}
	// Returning inside cancels both exit timers, including an imminent outside exit.
	state := (exitTimers{}).advance(now, exitOutside, policy).advance(now.Add(time.Millisecond), exitInside, infinite)
	if !state.deadline().IsZero() {
		t.Fatal("return inside failed to cancel exit timers")
	}
}
