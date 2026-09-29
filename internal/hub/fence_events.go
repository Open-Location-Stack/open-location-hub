package hub

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"sync"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/ids"
	"go.uber.org/zap"
)

func (s *Service) eventScheduler() *deadlineScheduler {
	s.eventTimerInit.Do(func() { s.eventTimers = newDeadlineScheduler(func() time.Time { return s.processingState().nowUTC() }) })
	return s.eventTimers
}
func (s *Service) fenceLock(member, fence string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(member))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(fence))
	return &s.fenceLocks[h.Sum32()%uint32(len(s.fenceLocks))]
}
func fenceTimerKey(member, fence string) string {
	return fmt.Sprintf("fence:%d:%s:%s", len(member), member, fence)
}
func providerMembershipKey(id string) string { return "provider:" + id }

func (s *Service) publishFenceEvents(ctx context.Context, location gen.Location) error {
	return s.publishFenceObservation(ctx, location, location.Trackables != nil && len(*location.Trackables) > 0)
}

func (s *Service) publishFenceObservation(ctx context.Context, location gen.Location, trackableEvents bool) error {
	if s.bus == nil {
		return nil
	}
	// Expiry precedes an observation arriving at/after the deadline. A subsequent
	// inside observation can therefore produce a fresh entry.
	s.eventScheduler().runDue()
	// Evaluate each observation once in the world frame where possible. Local
	// fences are transformed at metadata refresh, so crossing between zones
	// cannot strand membership in the previous coordinate frame.
	if projected, err := s.locationToWGS84(ctx, location); err == nil {
		location = projected
	}
	provider, hasProvider := s.providerByID(ctx, location.ProviderId)
	fences, err := s.fenceCandidatesForLocation(ctx, location)
	if err != nil {
		return err
	}
	members := []string{providerMembershipKey(location.ProviderId)}
	if trackableEvents {
		members = *location.Trackables
	}
	for _, member := range members {
		var trackable gen.Trackable
		hasTrackable := false
		if trackableEvents {
			trackable, err = s.trackableByID(ctx, member)
			hasTrackable = err == nil
		}
		candidates := make(map[string]gen.Fence, len(fences))
		for _, fence := range fences {
			candidates[fence.Id.String()] = fence
		}
		for _, id := range s.processingState().ListInsideFences(member) {
			if _, ok := candidates[id]; ok {
				continue
			}
			if fence, ok := s.fenceByID(ctx, id); ok {
				if cache := s.metadataCache(); cache != nil && locationCRS(location) == "EPSG:4326" {
					if world, ok := cache.current().worldFencesByID[id]; ok {
						fence = world
					}
				}
				candidates[id] = fence
			} else {
				s.processingState().ClearInsideFence(member, id)
				s.eventScheduler().schedule(fenceTimerKey(member, id), time.Time{}, nil)
			}
		}
		for id, fence := range candidates {
			if cache := s.metadataCache(); cache != nil {
				scope, ok, _ := cache.current().locationFenceScopeKey(location)
				fenceScope, valid := fenceScopeKey(fence)
				if !ok || !valid || scope != fenceScope {
					continue
				}
			}
			containment, err := fenceContainmentForLocation(fence, location, trackableRadius(trackable))
			if err != nil {
				continue
			}
			var extrusion *float64
			if hasTrackable {
				extrusion = trackable.Extrusion
			} else {
				zero := 0.0
				extrusion = &zero
			}
			verticalGap := fenceHeightGap(fence, location, extrusion)
			containment.OutsideDistance = math.Hypot(containment.OutsideDistance, verticalGap)
			containment.Inside = containment.Inside && verticalGap == 0
			if elevationName(fence.ElevationRef) != elevationName(location.ElevationRef) {
				// The specification leaves mixed height datums undefined. Do not
				// compare them as if their z-coordinates shared a reference.
				continue
			}
			if !fenceMatchesFloor(fence, location) {
				// Section 8.4 excludes events for nonmatching floors. Cancel any
				// previous membership so its timer cannot later emit on that floor.
				lock := s.fenceLock(member, id)
				lock.Lock()
				s.processingState().ClearInsideFence(member, id)
				s.eventScheduler().schedule(fenceTimerKey(member, id), time.Time{}, nil)
				lock.Unlock()
				continue
			}
			policy := resolveFenceExitPolicy(fence, trackable, hasTrackable, provider, hasProvider)
			area := exitOutside
			if containment.Inside {
				area = exitInside
			} else if containment.OutsideDistance <= policy.ExitTolerance {
				area = exitTolerance
			}
			lock := s.fenceLock(member, id)
			lock.Lock()
			err = s.transitionFenceMembership(ctx, member, fence, location, area, policy)
			lock.Unlock()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// Caller holds the member/fence stripe through state transition and publication,
// keeping entry, cancellation, and timer expiry ordered for that membership.
func (s *Service) transitionFenceMembership(ctx context.Context, member string, fence gen.Fence, location gen.Location, area exitArea, policy fenceExitPolicy) error {
	state := s.processingState()
	now := state.nowUTC()
	id := fence.Id.String()
	if cache := s.metadataCache(); cache != nil {
		if _, ok := cache.FenceByID(id); !ok {
			return nil
		}
	}
	membership, inside := state.FenceMembershipState(member, id)
	if !inside && area != exitInside {
		return nil
	}
	if !inside {
		membership = expiringFenceMembership{entryTime: now}
	}
	membership.timers = membership.timers.advance(now, area, exitTimerPolicy{fenceTimeout: policy.FenceTimeout, toleranceTimeout: policy.ToleranceTimeout, exitDelay: policy.ExitDelay})
	membership.fence = fence
	membership.location = location
	state.putFenceMembership(member, id, membership)
	if !inside {
		if err := s.emitMembershipEvent(ctx, member, membership, gen.RegionEntry, now); err != nil {
			return err
		}
	}
	deadline := membership.timers.deadline()
	if !deadline.IsZero() && !deadline.After(now) {
		return s.expireFenceMembership(ctx, member, id, deadline)
	}
	s.eventScheduler().schedule(fenceTimerKey(member, id), deadline, func() {
		lock := s.fenceLock(member, id)
		lock.Lock()
		defer lock.Unlock()
		if err := s.expireFenceMembership(context.Background(), member, id, deadline); err != nil && s.logger != nil {
			s.logger.Error("fence timeout publication failed", zap.Error(err))
		}
	})
	return nil
}
func (s *Service) expireFenceMembership(ctx context.Context, member, id string, deadline time.Time) error {
	membership, ok := s.processingState().FenceMembershipState(member, id)
	if !ok || !membership.timers.deadline().Equal(deadline) {
		return nil
	}
	s.processingState().ClearInsideFence(member, id)
	s.eventScheduler().schedule(fenceTimerKey(member, id), time.Time{}, nil)
	return s.emitMembershipEvent(ctx, member, membership, gen.RegionExit, deadline)
}
func (s *Service) emitMembershipEvent(ctx context.Context, member string, membership expiringFenceMembership, eventType gen.FenceEventEventType, at time.Time) error {
	fence, location := membership.fence, membership.location
	event := gen.FenceEvent{Id: ids.NewUUID(), FenceId: fence.Id, EventType: eventType, ProviderId: &location.ProviderId, ForeignId: fence.ForeignId, Properties: fence.Properties, EntryTime: &membership.entryTime}
	if member != providerMembershipKey(location.ProviderId) {
		event.TrackableId = &member
	} else {
		// The provider event reports associations without becoming a trackable event.
		if location.Trackables != nil {
			associations := gen.StringIdList(append([]string(nil), (*location.Trackables)...))
			event.Trackables = &associations
		} else if cache := s.metadataCache(); cache != nil {
			for _, trackable := range cache.TrackablesByProviderID(location.ProviderId) {
				if event.Trackables == nil {
					event.Trackables = &gen.StringIdList{}
				}
				*event.Trackables = append(*event.Trackables, trackable.Id.String())
			}
		}
	}
	if eventType == gen.RegionExit {
		event.ExitTime = &at
	}
	return s.publishFenceEvent(ctx, fence, event)
}
func (s *ProcessingState) putFenceMembership(member, fence string, value expiringFenceMembership) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fenceMembership[member] == nil {
		s.fenceMembership[member] = map[string]expiringFenceMembership{}
	}
	s.fenceMembership[member][fence] = value
}
