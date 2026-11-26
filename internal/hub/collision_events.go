package hub

import (
	"context"
	"math"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/ids"
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
	trackables, maxRadius, err := s.collisionTrackables(ctx)
	if err != nil {
		return err
	}
	state := s.processingState()
	for _, motion := range motions {
		left, ok := trackables[motion.Id]
		if !ok {
			continue
		}
		point, err := point2D(motion.Location.Position)
		if err != nil {
			return err
		}
		candidates := map[string]gen.TrackableMotion{}
		for _, candidate := range state.closestCollisionMotions(motion.Location, point, trackableRadius(left)+maxRadius) {
			if candidate.Id != motion.Id {
				candidates[candidate.Id] = candidate
			}
		}
		for _, pair := range state.collisionsForTrackable(motion.Id) {
			other := pair.leftMotion
			if other.Id == motion.Id {
				other = pair.rightMotion
			}
			if recent, ok := state.getCollisionMotion(other.Id); ok {
				other = recent
			}
			candidates[other.Id] = other
		}
		for _, other := range candidates {
			right, ok := trackables[other.Id]
			if !ok {
				continue
			}
			if err = s.transitionCollision(ctx, motion, left, other, right); err != nil {
				span.RecordError(err)
				return err
			}
		}
		state.setCollisionMotion(motion, point, s.cfg.CollisionStateTTL)
	}
	return nil
}

func (s *Service) collisionTrackables(ctx context.Context) (map[string]gen.Trackable, float64, error) {
	if cache := s.metadataCache(); cache != nil {
		snapshot := cache.current()
		return snapshot.trackablesByID, snapshot.maxTrackableRadius, nil
	}
	items, err := s.ListTrackables(ctx)
	if err != nil {
		return nil, 0, err
	}
	trackables := make(map[string]gen.Trackable, len(items))
	for _, item := range items {
		trackables[item.Id.String()] = item
	}
	return trackables, largestTrackableRadius(items), nil
}

func largestTrackableRadius(trackables []gen.Trackable) float64 {
	radius := 0.0
	for _, trackable := range trackables {
		radius = math.Max(radius, trackableRadius(trackable))
	}
	return radius
}

func (s *Service) transitionCollision(ctx context.Context, left gen.TrackableMotion, lt gen.Trackable, right gen.TrackableMotion, rt gen.Trackable) (err error) {
	started := time.Now()
	outcome := "no_change"
	defer func() {
		s.telemetry().RecordMachineEvaluation(ctx, "collision_pair", "observation", "live", outcome, time.Since(started), err)
	}()
	key := collisionPairKey(left.Id, right.Id)
	state, active := s.processingState().GetCollisionState(key)
	contact, err := CollisionContactForPair(left, lt, right, rt)
	if err != nil {
		return err
	}
	if !active && !contact.Inside {
		return nil
	}
	policy, tolerance := s.collisionExitPolicy(ctx, left, lt, right, rt)
	now := s.processingState().nowUTC()
	if !active {
		state = activeCollisionState{Active: true, StartTime: now}
	}
	state.leftMotion, state.rightMotion, state.leftTrackable, state.rightTrackable = left, right, lt, rt
	state.distance = contact.Distance
	state.LastSeen = now
	state.timers = state.timers.advance(now, contact.ExitArea(tolerance), policy)
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
	if !contact.Inside {
		return nil
	}
	kind := gen.Colliding
	if !active {
		kind = gen.CollisionStart
		outcome = "entry"
	}
	event := CollisionEventForPair(ids.NewUUID(), kind, now, state.StartTime, left, lt, right, rt, contact.Area, contact.Distance)
	return s.emitCollision(event, left)
}

func (s *Service) collisionExitPolicy(ctx context.Context, left gen.TrackableMotion, lt gen.Trackable, right gen.TrackableMotion, rt gen.Trackable) (exitTimerPolicy, float64) {
	lp, _ := s.providerByID(ctx, left.Location.ProviderId)
	rp, _ := s.providerByID(ctx, right.Location.ProviderId)
	policy := CollisionPolicyForPair(ResolveCollisionAssetPolicy(lt, lp), ResolveCollisionAssetPolicy(rt, rp))
	return exitTimerPolicy{fenceTimeout: -1, toleranceTimeout: time.Duration(policy.ToleranceTimeoutNanos), exitDelay: time.Duration(policy.ExitDelayNanos)}, policy.ExitTolerance
}

// CollisionPolicyForPair applies the OMLOX maximum timer and summed tolerance.
func CollisionPolicyForPair(left, right CollisionAssetPolicy) FenceRoutingPolicy {
	maximum := func(a, b int64) int64 {
		if a < 0 || b < 0 {
			return -1
		}
		if a > b {
			return a
		}
		return b
	}
	return FenceRoutingPolicy{FenceTimeoutNanos: -1, ToleranceTimeoutNanos: maximum(left.ToleranceTimeoutNanos, right.ToleranceTimeoutNanos),
		ExitDelayNanos: maximum(left.ExitDelayNanos, right.ExitDelayNanos), ExitTolerance: left.ExitTolerance + right.ExitTolerance}
}

// CollisionAssetPolicy holds the per-asset extent and timer overrides used in pairs.
type CollisionAssetPolicy struct {
	Radius, ExitTolerance                 float64
	ToleranceTimeoutNanos, ExitDelayNanos int64
}

// ResolveCollisionAssetPolicy applies provider overrides before contact evaluation.
func ResolveCollisionAssetPolicy(trackable gen.Trackable, provider gen.LocationProvider) CollisionAssetPolicy {
	policy := fenceExitPolicy{}
	applyFencePolicyOverride(&policy, trackable.ExitTolerance, trackable.ToleranceTimeout, trackable.ExitDelay)
	applyFencePolicyOverride(&policy, provider.ExitTolerance, provider.ToleranceTimeout, provider.ExitDelay)
	return CollisionAssetPolicy{
		Radius: trackableRadius(trackable), ExitTolerance: policy.ExitTolerance,
		ToleranceTimeoutNanos: int64(policy.ToleranceTimeout), ExitDelayNanos: int64(policy.ExitDelay),
	}
}

// Caller holds collisionMu, including while emitting the ordered edge.
func (s *Service) expireCollision(key string, deadline time.Time) (err error) {
	ctx := context.Background()
	started := time.Now()
	outcome := "no_change"
	defer func() {
		s.telemetry().RecordMachineEvaluation(ctx, "collision_pair", "timer", "live", outcome, time.Since(started), err)
	}()
	state, ok := s.processingState().GetCollisionState(key)
	if !ok || !state.timers.deadline().Equal(deadline) {
		return nil
	}
	outcome = "exit"
	s.telemetry().RecordQueueWait(ctx, "collision_pair_timer", s.processingState().nowUTC().Sub(deadline))
	s.processingState().DeleteCollisionState(key)
	s.eventScheduler().schedule(key, time.Time{}, nil)
	event := CollisionEventForPair(ids.NewUUID(), gen.CollisionEnd, deadline, state.StartTime, state.leftMotion, state.leftTrackable, state.rightMotion, state.rightTrackable, nil, state.distance)
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
