package hub

import (
	"context"
	"math"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"go.uber.org/zap"
)

// publishCollisionEvents serializes pair transitions with timer callbacks. The
// spatial index finds possible new contacts; active pairs are always reconsidered,
// even when a trackable jumps completely outside that index's search window.
func (s *Service) publishCollisionEvents(ctx context.Context, motions []gen.TrackableMotion) error {
	if s.bus == nil || !s.cfg.CollisionsEnabled || len(motions) == 0 {
		return nil
	}
	s.eventScheduler().runDue()
	s.collisionMu.Lock()
	defer s.collisionMu.Unlock()
	stageCtx, span := s.telemetry().StartSpan(ctx, "hub.process.collision_events")
	defer span.End()
	started := time.Now()
	defer func() {
		s.telemetry().RecordProcessingDuration(stageCtx, "collision_evaluation", "location", time.Since(started))
	}()
	active := s.processingState().listCollisionMotions()
	latest := make(map[string]gen.TrackableMotion, len(active)+len(motions))
	indexed := make([]indexedCollisionMotion, 0, len(active))
	maxRadius := 0.0
	for _, motion := range active {
		latest[motion.Id] = motion
		point, err := point2D(motion.Location.Position)
		if err != nil {
			continue
		}
		if entry, ok := newIndexedCollisionMotion(motion, point); ok {
			indexed = append(indexed, entry)
		}
		if trackable, err := s.trackableByID(ctx, motion.Id); err == nil {
			maxRadius = math.Max(maxRadius, trackableRadius(trackable))
		}
	}
	index := newCollisionSpatialIndex(indexed)
	for _, motion := range motions {
		left, err := s.trackableByID(ctx, motion.Id)
		if err != nil {
			continue
		}
		point, err := point2D(motion.Location.Position)
		if err != nil {
			return err
		}
		candidates := map[string]gen.TrackableMotion{}
		for _, candidate := range index.Nearby(motion.Location, point, trackableRadius(left)+maxRadius) {
			if candidate.motion.Id != motion.Id {
				candidates[candidate.motion.Id] = latest[candidate.motion.Id]
			}
		}
		for _, pair := range s.processingState().collisionsForTrackable(motion.Id) {
			other := pair.leftMotion
			if other.Id == motion.Id {
				other = pair.rightMotion
			}
			if recent, ok := latest[other.Id]; ok {
				other = recent
			}
			candidates[other.Id] = other
		}
		for _, other := range candidates {
			right, err := s.trackableByID(ctx, other.Id)
			if err != nil {
				continue
			}
			if err = s.transitionCollision(ctx, motion, left, other, right); err != nil {
				span.RecordError(err)
				return err
			}
		}
		latest[motion.Id] = motion
		s.processingState().setCollisionMotion(motion.Id, motion, s.cfg.CollisionStateTTL)
		maxRadius = math.Max(maxRadius, trackableRadius(left))
		if entry, ok := newIndexedCollisionMotion(motion, point); ok {
			index.add(entry)
		}
	}
	return nil
}

func (s *Service) transitionCollision(ctx context.Context, left gen.TrackableMotion, lt gen.Trackable, right gen.TrackableMotion, rt gen.Trackable) error {
	key := collisionPairKey(left.Id, right.Id)
	state, active := s.processingState().GetCollisionState(key)
	lp, err := point2D(left.Location.Position)
	if err != nil {
		return err
	}
	rp, err := point2D(right.Location.Position)
	if err != nil {
		return err
	}
	colliding, area, distance := motionsCollide(left, lt, right, rt, lp, rp, 0)
	compatible := collisionFramesMatch(left.Location, right.Location)
	if !compatible {
		colliding = false
		area = nil
	}
	if !active && !colliding {
		return nil
	}
	policy, tolerance := s.collisionExitPolicy(ctx, left, lt, right, rt)
	physical := exitOutside
	if colliding {
		physical = exitInside
	} else if compatible && math.Hypot(math.Max(0, distance-trackableRadius(lt)-trackableRadius(rt)), locationHeightGap(left.Location, lt.Extrusion, right.Location, rt.Extrusion)) <= tolerance {
		physical = exitTolerance
	}
	now := s.processingState().nowUTC()
	if !active {
		state = activeCollisionState{Active: true, StartTime: now}
	}
	state.leftMotion, state.rightMotion, state.leftTrackable, state.rightTrackable = left, right, lt, rt
	state.distance = distance
	state.LastSeen = now
	state.timers = state.timers.advance(now, physical, policy)
	// Collision membership follows table 14, not the observation cache TTL.
	s.processingState().SetCollisionState(key, state, 0)
	deadline := state.timers.deadline()
	if !deadline.IsZero() && !deadline.After(now) {
		return s.expireCollision(key, deadline)
	}
	s.eventScheduler().schedule(key, deadline, func() {
		s.collisionMu.Lock()
		defer s.collisionMu.Unlock()
		if err := s.expireCollision(key, deadline); err != nil && s.logger != nil {
			s.logger.Error("collision timeout publication failed", zap.Error(err))
		}
	})
	if !colliding {
		return nil
	}
	kind := gen.Colliding
	if !active {
		kind = gen.CollisionStart
	}
	event := collisionEventForPair(kind, now, state.StartTime, left, lt, right, rt, area, distance, 0)
	return s.emitCollision(event, left)
}

func (s *Service) collisionExitPolicy(ctx context.Context, left gen.TrackableMotion, lt gen.Trackable, right gen.TrackableMotion, rt gen.Trackable) (exitTimerPolicy, float64) {
	forTrackable := func(motion gen.TrackableMotion, trackable gen.Trackable) fenceExitPolicy {
		policy := fenceExitPolicy{}
		applyFencePolicyOverride(&policy, trackable.ExitTolerance, trackable.ToleranceTimeout, trackable.ExitDelay)
		if provider, ok := s.providerByID(ctx, motion.Location.ProviderId); ok {
			applyFencePolicyOverride(&policy, provider.ExitTolerance, provider.ToleranceTimeout, provider.ExitDelay)
		}
		return policy
	}
	l, r := forTrackable(left, lt), forTrackable(right, rt)
	maximum := func(a, b time.Duration) time.Duration {
		if a < 0 || b < 0 {
			return -1
		}
		if a > b {
			return a
		}
		return b
	}
	return exitTimerPolicy{fenceTimeout: -1, toleranceTimeout: maximum(l.ToleranceTimeout, r.ToleranceTimeout), exitDelay: maximum(l.ExitDelay, r.ExitDelay)}, l.ExitTolerance + r.ExitTolerance
}

// Caller holds collisionMu, including while emitting the ordered edge.
func (s *Service) expireCollision(key string, deadline time.Time) error {
	state, ok := s.processingState().GetCollisionState(key)
	if !ok || !state.timers.deadline().Equal(deadline) {
		return nil
	}
	s.processingState().DeleteCollisionState(key)
	s.eventScheduler().schedule(key, time.Time{}, nil)
	event := collisionEventForPair(gen.CollisionEnd, deadline, state.StartTime, state.leftMotion, state.leftTrackable, state.rightMotion, state.rightTrackable, nil, state.distance, 0)
	event.EndTime = &deadline
	return s.emitCollision(event, state.leftMotion)
}
func (s *Service) emitCollision(event gen.CollisionEvent, leading gen.TrackableMotion) error {
	busEvent, err := newEvent(EventCollisionEvent, ScopeEPSG4326, timeValue(event.CollisionTime), leading.Location.ProviderId, leading.Id, "", s.cfg.HubID, CollisionEnvelope{Event: event})
	if err == nil {
		s.bus.Emit(busEvent)
	}
	return err
}

func collisionFramesMatch(left, right gen.Location) bool {
	if locationCRS(left) != locationCRS(right) {
		return false
	}
	if locationCRS(left) == "local" && left.Source != right.Source {
		return false
	}
	if elevationName(left.ElevationRef) != elevationName(right.ElevationRef) {
		return false
	}
	// Missing floor matches all floors, as with fence floor filtering.
	return left.Floor == nil || right.Floor == nil || *left.Floor == *right.Floor
}
