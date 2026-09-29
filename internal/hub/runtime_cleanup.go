package hub

import (
	"context"
	"time"

	"github.com/google/uuid"
)

func (s *Service) clearMemberFences(member string) {
	for _, id := range s.processingState().ListInsideFences(member) {
		lock := s.fenceLock(member, id)
		lock.Lock()
		s.processingState().ClearInsideFence(member, id)
		s.eventScheduler().schedule(fenceTimerKey(member, id), time.Time{}, nil)
		lock.Unlock()
	}
}
func (s *Service) clearFenceRuntime(id string) {
	state := s.processingState()
	state.mu.RLock()
	members := []string{}
	for member, fences := range state.fenceMembership {
		if _, ok := fences[id]; ok {
			members = append(members, member)
		}
	}
	state.mu.RUnlock()
	for _, member := range members {
		lock := s.fenceLock(member, id)
		lock.Lock()
		state.ClearInsideFence(member, id)
		s.eventScheduler().schedule(fenceTimerKey(member, id), time.Time{}, nil)
		lock.Unlock()
	}
}

// Caller holds the trackable's selection lock. Deletion is administrative and
// cancels pending events; it does not fabricate a new location or separation.
func (s *Service) clearTrackableRuntime(id string) {
	state := s.processingState()
	state.mu.Lock()
	delete(state.trackableCandidates, id)
	delete(state.trackableInputs, id)
	delete(state.latestTrackableLocation, latestTrackableLocationKey(id))
	delete(state.motions, id)
	delete(state.kalmanTracks, id)
	state.mu.Unlock()
	s.locatingRuleCache.Delete(id)
	s.clearMemberFences(id)
	s.collisionMu.Lock()
	for _, pair := range state.collisionsForTrackable(id) {
		key := collisionPairKey(pair.leftMotion.Id, pair.rightMotion.Id)
		state.DeleteCollisionState(key)
		s.eventScheduler().schedule(key, time.Time{}, nil)
	}
	state.mu.Lock()
	delete(state.collisionMotions, id)
	state.mu.Unlock()
	s.collisionMu.Unlock()
}

func (s *Service) deleteProviderState(ctx context.Context, id string) bool {
	state := s.processingState()
	state.mu.Lock()
	removed := false
	affected := map[string]bool{}
	for key := range state.providerLocationKeys[id] {
		state.deleteLatestLocationLocked(key)
		removed = true
	}
	for trackable, candidates := range state.trackableCandidates {
		for key, item := range candidates {
			if item.value.ProviderId == id {
				delete(candidates, key)
			}
		}
		if selected, ok := state.trackableInputs[trackable]; ok && selected.value.ProviderId == id {
			affected[trackable] = true
		}
	}
	state.mu.Unlock()
	s.clearMemberFences(providerMembershipKey(id))
	for trackable := range affected {
		lock := s.trackableSelectionLock(trackable)
		lock.Lock()
		s.clearTrackableRuntime(trackable)
		lock.Unlock()
		if parsed, err := uuid.Parse(trackable); err == nil {
			// Remaining assigned providers can supply a replacement selection.
			_ = s.ReprocessTrackable(ctx, parsed)
		}
	}
	return removed
}
