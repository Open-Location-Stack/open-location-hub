package hub

import "time"

type exitArea = FenceArea

const (
	exitInside    = FenceInside
	exitTolerance = FenceTolerance
	exitOutside   = FenceOutside
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

// FenceDeadlines is the recoverable state of the three OMLOX exit timers.
type FenceDeadlines struct {
	Area                      FenceArea
	Fence, Tolerance, Outside time.Time
}

// FenceArea classifies physical containment independently of membership.
type FenceArea uint8

const (
	// FenceInside includes a trackable touching the fence.
	FenceInside FenceArea = iota
	// FenceTolerance is outside the fence but still within the exit tolerance.
	FenceTolerance
	// FenceOutside is beyond the exit tolerance.
	FenceOutside
)

// Advance applies the same exit timer transitions as the compatibility runtime.
func (f FenceDeadlines) Advance(now time.Time, area FenceArea, policy FenceRoutingPolicy) FenceDeadlines {
	next := f.timers().advance(now, area, exitTimerPolicy{
		fenceTimeout: time.Duration(policy.FenceTimeoutNanos), toleranceTimeout: time.Duration(policy.ToleranceTimeoutNanos),
		exitDelay: time.Duration(policy.ExitDelayNanos),
	})
	return FenceDeadlines{Area: next.area, Fence: next.fenceDeadline, Tolerance: next.toleranceDeadline, Outside: next.outsideDeadline}
}

// Next returns the first enabled deadline, or the zero time if all are disabled.
func (f FenceDeadlines) Next() time.Time { return f.timers().deadline() }

func (f FenceDeadlines) timers() exitTimers {
	return exitTimers{area: f.Area, fenceDeadline: f.Fence, toleranceDeadline: f.Tolerance, outsideDeadline: f.Outside}
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
