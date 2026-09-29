package hub

import "time"

// exitArea describes physical containment, independently of logical membership
// retained by a pending exit timer.
type exitArea uint8

const (
	exitInside exitArea = iota
	exitTolerance
	exitOutside
)

// A negative duration means infinite. Zero means immediate; it must not be
// conflated with an absent (infinite) deadline.
type exitTimerPolicy struct {
	fenceTimeout     time.Duration
	toleranceTimeout time.Duration
	exitDelay        time.Duration
}

type exitTimers struct {
	area              exitArea
	fenceDeadline     time.Time
	toleranceDeadline time.Time
	outsideDeadline   time.Time
}

// advance implements the nine transitions in Hub 2.0.0 table 13. Collisions use
// the same transitions with an infinite fence timeout (table 14).
func (t exitTimers) advance(now time.Time, area exitArea, policy exitTimerPolicy) exitTimers {
	next := t
	next.fenceDeadline = exitDeadline(now, policy.fenceTimeout)
	switch area {
	case exitInside:
		next.toleranceDeadline = time.Time{}
		next.outsideDeadline = time.Time{}
	case exitTolerance:
		if t.area == exitInside {
			next.toleranceDeadline = exitDeadline(now, policy.toleranceTimeout)
		}
		next.outsideDeadline = time.Time{}
	case exitOutside:
		if t.area == exitInside {
			next.toleranceDeadline = exitDeadline(now, policy.toleranceTimeout)
		}
		if t.area != exitOutside {
			next.outsideDeadline = exitDeadline(now, policy.exitDelay)
		}
	}
	next.area = area
	return next
}

func exitDeadline(now time.Time, duration time.Duration) time.Time {
	if duration < 0 {
		return time.Time{}
	}
	return now.Add(duration)
}

func (t exitTimers) deadline() time.Time {
	var earliest time.Time
	for _, deadline := range []time.Time{t.fenceDeadline, t.toleranceDeadline, t.outsideDeadline} {
		if !deadline.IsZero() && (earliest.IsZero() || deadline.Before(earliest)) {
			earliest = deadline
		}
	}
	return earliest
}
