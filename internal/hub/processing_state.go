package hub

import (
	"context"
	"sync"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
)

type expiringLocation struct {
	value     gen.Location
	expiresAt time.Time
}

type expiringProximityState struct {
	value     proximityResolutionState
	expiresAt time.Time
}

type expiringMotion struct {
	value     gen.TrackableMotion
	expiresAt time.Time
}

type expiringCollisionState struct {
	value     activeCollisionState
	expiresAt time.Time
}

type expiringKalmanTrack struct {
	value     kalmanTrackState
	expiresAt time.Time
}

type expiringFenceMembership struct {
	expiresAt time.Time
}

// ProcessingState keeps transient decision state in memory.
type ProcessingState struct {
	mu                      sync.RWMutex
	now                     func() time.Time
	dedup                   map[string]time.Time
	latestLocations         map[string]expiringLocation
	latestTrackableLocation map[string]expiringLocation
	proximity               map[string]expiringProximityState
	fenceMembership         map[string]map[string]expiringFenceMembership
	motions                 map[string]expiringMotion
	collisions              map[string]expiringCollisionState
	kalmanTracks            map[string]expiringKalmanTrack
}

// NewProcessingState constructs an empty in-memory transient state store.
func NewProcessingState(now func() time.Time) *ProcessingState {
	if now == nil {
		now = time.Now
	}
	return &ProcessingState{
		now:                     now,
		dedup:                   map[string]time.Time{},
		latestLocations:         map[string]expiringLocation{},
		latestTrackableLocation: map[string]expiringLocation{},
		proximity:               map[string]expiringProximityState{},
		fenceMembership:         map[string]map[string]expiringFenceMembership{},
		motions:                 map[string]expiringMotion{},
		collisions:              map[string]expiringCollisionState{},
		kalmanTracks:            map[string]expiringKalmanTrack{},
	}
}

func (s *ProcessingState) nowUTC() time.Time {
	return s.now().UTC()
}

func (s *ProcessingState) Deduplicate(key string, ttl time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowUTC()
	if expiresAt, ok := s.dedup[key]; ok && expiresAt.After(now) {
		return false
	}
	s.dedup[key] = now.Add(ttl)
	return true
}

func (s *ProcessingState) SetLatestLocation(key string, value gen.Location, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latestLocations[key] = expiringLocation{value: value, expiresAt: s.nowUTC().Add(ttl)}
}

func (s *ProcessingState) SetTrackableLocation(key string, value gen.Location, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latestTrackableLocation[key] = expiringLocation{value: value, expiresAt: s.nowUTC().Add(ttl)}
}

func (s *ProcessingState) GetProximityState(key string) (proximityResolutionState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.proximity[key]
	if !ok || !item.expiresAt.After(s.nowUTC()) {
		delete(s.proximity, key)
		return proximityResolutionState{}, false
	}
	return item.value, true
}

func (s *ProcessingState) SetProximityState(key string, value proximityResolutionState, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.proximity[key] = expiringProximityState{value: value, expiresAt: s.nowUTC().Add(ttl)}
}

func (s *ProcessingState) DeleteProximityState(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.proximity, key)
}

func (s *ProcessingState) IsInsideFence(trackableID, fenceID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.fenceMembership[trackableID]
	if !ok {
		return false
	}
	membership, ok := current[fenceID]
	if !ok || !membership.expiresAt.After(s.nowUTC()) {
		delete(current, fenceID)
		if len(current) == 0 {
			delete(s.fenceMembership, trackableID)
		}
		return false
	}
	return true
}

func (s *ProcessingState) SetInsideFence(trackableID, fenceID string, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.fenceMembership[trackableID]
	if current == nil {
		current = map[string]expiringFenceMembership{}
		s.fenceMembership[trackableID] = current
	}
	current[fenceID] = expiringFenceMembership{expiresAt: s.nowUTC().Add(ttl)}
}

func (s *ProcessingState) ClearInsideFence(trackableID, fenceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.fenceMembership[trackableID]
	if current == nil {
		return
	}
	delete(current, fenceID)
	if len(current) == 0 {
		delete(s.fenceMembership, trackableID)
	}
}

func (s *ProcessingState) SetMotion(trackableID string, value gen.TrackableMotion, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.motions[trackableID] = expiringMotion{value: value, expiresAt: s.nowUTC().Add(ttl)}
}

func (s *ProcessingState) GetMotion(trackableID string) (gen.TrackableMotion, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.motions[trackableID]
	if !ok || !item.expiresAt.After(s.nowUTC()) {
		delete(s.motions, trackableID)
		return gen.TrackableMotion{}, false
	}
	return item.value, true
}

func (s *ProcessingState) DeleteMotion(trackableID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.motions, trackableID)
}

func (s *ProcessingState) ListActiveMotions() []gen.TrackableMotion {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowUTC()
	motions := make([]gen.TrackableMotion, 0, len(s.motions))
	for key, item := range s.motions {
		if !item.expiresAt.After(now) {
			delete(s.motions, key)
			continue
		}
		motions = append(motions, item.value)
	}
	return motions
}

func (s *ProcessingState) GetCollisionState(key string) (activeCollisionState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.collisions[key]
	if !ok || !item.expiresAt.After(s.nowUTC()) {
		delete(s.collisions, key)
		return activeCollisionState{}, false
	}
	return item.value, true
}

func (s *ProcessingState) SetCollisionState(key string, value activeCollisionState, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.collisions[key] = expiringCollisionState{value: value, expiresAt: s.nowUTC().Add(ttl)}
}

func (s *ProcessingState) DeleteCollisionState(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.collisions, key)
}

func (s *ProcessingState) GetKalmanTrackState(trackableID string) (kalmanTrackState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.kalmanTracks[trackableID]
	if !ok || !item.expiresAt.After(s.nowUTC()) {
		delete(s.kalmanTracks, trackableID)
		return kalmanTrackState{}, false
	}
	return item.value, true
}

func (s *ProcessingState) SetKalmanTrackState(trackableID string, value kalmanTrackState, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kalmanTracks[trackableID] = expiringKalmanTrack{value: value, expiresAt: s.nowUTC().Add(ttl)}
}

func (s *ProcessingState) DeleteKalmanTrackState(trackableID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.kalmanTracks, trackableID)
}

// StartSweeper periodically removes expired entries from in-memory transient
// state so stale keys do not accumulate indefinitely.
func (s *ProcessingState) StartSweeper(ctx context.Context, interval time.Duration) {
	if s == nil {
		return
	}
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.SweepExpired()
			}
		}
	}()
}

// SweepExpired removes transient entries whose TTL has elapsed.
func (s *ProcessingState) SweepExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowUTC()
	for key, expiresAt := range s.dedup {
		if !expiresAt.After(now) {
			delete(s.dedup, key)
		}
	}
	for key, item := range s.latestLocations {
		if !item.expiresAt.After(now) {
			delete(s.latestLocations, key)
		}
	}
	for key, item := range s.latestTrackableLocation {
		if !item.expiresAt.After(now) {
			delete(s.latestTrackableLocation, key)
		}
	}
	for key, item := range s.proximity {
		if !item.expiresAt.After(now) {
			delete(s.proximity, key)
		}
	}
	for trackableID, memberships := range s.fenceMembership {
		for fenceID, membership := range memberships {
			if !membership.expiresAt.After(now) {
				delete(memberships, fenceID)
			}
		}
		if len(memberships) == 0 {
			delete(s.fenceMembership, trackableID)
		}
	}
	for key, item := range s.motions {
		if !item.expiresAt.After(now) {
			delete(s.motions, key)
		}
	}
	for key, item := range s.collisions {
		if !item.expiresAt.After(now) {
			delete(s.collisions, key)
		}
	}
	for key, item := range s.kalmanTracks {
		if !item.expiresAt.After(now) {
			delete(s.kalmanTracks, key)
		}
	}
}
