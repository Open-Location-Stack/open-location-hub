package hub

import (
	"context"
	"hash/fnv"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
)

// Candidate updates are serialized per trackable, not across the whole hub.
// Provider/source queue ordering and event-time comparisons jointly prevent an
// older sample from replacing a newer one, even across concurrent transports.
func (s *Service) processTrackableCandidate(ctx context.Context, id string, location gen.Location) error {
	lock := s.trackableSelectionLock(id)
	lock.Lock()
	defer lock.Unlock()
	trackable := gen.Trackable{}
	if s.metadata != nil || s.queries != nil {
		if item, err := s.trackableByID(ctx, id); err == nil {
			trackable = item
		} else {
			return nil
		}
	}
	state := s.processingState()
	ttl := s.cfg.LocationTTL
	if location.ProviderType == "rfid" || location.ProviderType == "ibeacon" {
		if s.cfg.ProximityTTL > 0 {
			ttl = s.cfg.ProximityTTL
		}
	}
	candidates := state.updateTrackableCandidate(id, location, ttl)
	return s.applyTrackableSelection(ctx, id, trackable, candidates, false)
}

func (s *Service) trackableSelectionLock(id string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return &s.selectionLocks[h.Sum32()%uint32(len(s.selectionLocks))]
}

// ReprocessTrackable re-evaluates retained provider observations after a metadata
// write. It preserves the provider data and emits events only for changed output.
func (s *Service) ReprocessTrackable(ctx context.Context, id gen.TrackableId) error {
	lock := s.trackableSelectionLock(id.String())
	lock.Lock()
	defer lock.Unlock()
	trackable, err := s.GetTrackable(ctx, id)
	if err != nil {
		return err
	}
	state := s.processingState()
	candidates := state.seedTrackableCandidates(trackable)
	return s.applyTrackableSelection(ctx, id.String(), trackable, candidates, true)
}

func (s *Service) applyTrackableSelection(ctx context.Context, id string, trackable gen.Trackable, candidates []expiringLocation, force bool) error {
	rules, err := s.rulesForTrackable(trackable)
	if err != nil {
		return err
	}
	state := s.processingState()
	if len(candidates) == 0 {
		if force {
			s.clearTrackableRuntime(id)
		}
		return nil
	}
	// Sorting makes equal-priority/time selection deterministic. Prefer an existing
	// selection on an exact tie so queue interleaving cannot cause oscillation.
	sort.Slice(candidates, func(i, j int) bool {
		return latestLocationKey(candidates[i].value.ProviderId, candidates[i].value.Source) < latestLocationKey(candidates[j].value.ProviderId, candidates[j].value.Source)
	})
	previous, hadPrevious := state.trackableInput(id)
	now := state.nowUTC()
	var selected expiringLocation
	bestPriority := -1.0
	for _, candidate := range candidates {
		provider := gen.LocationProvider{}
		if s.metadata != nil || s.queries != nil {
			provider, _ = s.providerByID(ctx, candidate.value.ProviderId)
		}
		priority := locationRulePriority(rules, ruleContext{location: candidate.value, provider: provider, now: now})
		newer := candidateTime(candidate).After(candidateTime(selected))
		equal := candidateTime(candidate).Equal(candidateTime(selected))
		previousCandidate := hadPrevious && dedupLocationKey(candidate.value) == dedupLocationKey(previous.value)
		if priority > bestPriority || priority == bestPriority && (newer || equal && previousCandidate) {
			selected = candidate
			bestPriority = priority
		}
	}
	sameInput := hadPrevious && dedupLocationKey(previous.value) == dedupLocationKey(selected.value)
	if sameInput && !force {
		return nil
	}
	// Without rules, an update must be strictly newer to move the trackable.
	if !force && len(rules) == 0 && hadPrevious && !candidateTime(selected).After(candidateTime(previous)) {
		return nil
	}
	selectedInput := selected
	selected.value.Trackables = &gen.StringIdList{id}
	associated := true
	selected.value.Associated = &associated
	stage := s.decisionStage
	if stage == nil {
		stage = passthroughDecisionStage{}
	}
	var results []decisionLocationResult
	if sameInput && force {
		unchanged, ok := state.GetTrackableLocation(latestTrackableLocationKey(id))
		if ok {
			results = []decisionLocationResult{{Location: unchanged, Emit: true}}
		}
	}
	if results == nil {
		results, err = stage.Process(ctx, selected.value)
		if err != nil {
			return err
		}
	}
	for _, result := range results {
		remaining := selected.expiresAt.Sub(now)
		state.SetTrackableLocation(latestTrackableLocationKey(id), result.Location, remaining)
		motion := gen.TrackableMotion{Id: id, Location: result.Location, Name: trackable.Name, Geometry: trackable.Geometry, Extrusion: trackable.Extrusion, Properties: trackable.Properties}
		if trackable.Radius != nil {
			motion.Geometry = circularGeometry(result.Location, trackableRadius(trackable))
		}
		previousMotion, hasMotion := state.GetMotion(id)
		changed := !hasMotion || !reflect.DeepEqual(previousMotion, motion)
		state.SetMotion(id, motion, remaining)
		if s.bus != nil {
			if force && !changed {
				// Re-evaluate containment (e.g. after a fence edit), without
				// replaying motion or collision continuation notifications.
				if e := s.publishFenceEvents(ctx, result.Location); e != nil {
					return e
				}
				continue
			}
			if err := s.publishSelectedTrackable(ctx, result.Location, result.Emit); err != nil {
				return err
			}
		}
	}
	state.setTrackableInput(id, selectedInput)
	return nil
}

func candidateTime(candidate expiringLocation) time.Time {
	if at := locationTime(candidate.value); !at.IsZero() {
		return at
	}
	return candidate.receivedAt
}

func (s *Service) publishSelectedTrackable(ctx context.Context, location gen.Location, emit bool) error {
	if err := s.publishFenceEvents(ctx, location); err != nil {
		return err
	}
	variants, err := s.locationVariants(ctx, location)
	if err != nil {
		return err
	}
	originals, err := s.buildTrackableMotionsForLocation(ctx, location)
	if err != nil {
		return err
	}
	for _, variant := range []struct {
		location *gen.Location
		scope    EventScope
	}{{variants.Local, ScopeLocal}, {variants.WGS84, ScopeEPSG4326}} {
		if variant.location == nil {
			continue
		}
		motions, err := s.buildTrackableMotionsForLocation(ctx, *variant.location)
		if err != nil {
			return err
		}
		if emit {
			if err := s.publishTrackableMotionEvents(variant.location.ProviderId, variant.scope, motions, originals); err != nil {
				return err
			}
			originals = nil
		}
		if variant.scope == ScopeEPSG4326 {
			if err := s.enqueueCollisionWork(ctx, motions); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *ProcessingState) updateTrackableCandidate(id string, location gen.Location, ttl time.Duration) []expiringLocation {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowUTC()
	candidates := s.trackableCandidates[id]
	if candidates == nil {
		candidates = map[string]expiringLocation{}
		s.trackableCandidates[id] = candidates
	}
	key := latestLocationKey(location.ProviderId, location.Source)
	value := expiringLocation{value: location, receivedAt: now, expiresAt: now.Add(ttl)}
	old, exists := candidates[key]
	if !exists || !candidateTime(value).Before(candidateTime(old)) {
		candidates[key] = value
	}
	out := make([]expiringLocation, 0, len(candidates))
	for key, item := range candidates {
		if !item.expiresAt.After(now) {
			delete(candidates, key)
			continue
		}
		out = append(out, item)
	}
	return out
}
func (s *ProcessingState) trackableInput(id string) (expiringLocation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.trackableInputs[id]
	return item, ok && item.expiresAt.After(s.nowUTC())
}
func (s *ProcessingState) setTrackableInput(id string, location expiringLocation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trackableInputs[id] = location
}

func (s *ProcessingState) seedTrackableCandidates(trackable gen.Trackable) []expiringLocation {
	s.mu.Lock()
	defer s.mu.Unlock()
	providers := map[string]bool{}
	for _, id := range stringSliceValue(trackable.LocationProviders) {
		providers[id] = true
	}
	now := s.nowUTC()
	candidates := map[string]expiringLocation{}
	out := []expiringLocation{}
	for providerID := range providers {
		for key := range s.providerLocationKeys[providerID] {
			item := s.latestLocations[key]
			if item.expiresAt.After(now) {
				candidates[key] = item
				out = append(out, item)
			}
		}
	}
	s.trackableCandidates[trackable.Id.String()] = candidates
	return out
}
