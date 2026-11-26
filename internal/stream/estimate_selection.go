package stream

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"slices"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"google.golang.org/protobuf/proto"
)

const (
	maxEstimateCandidates     = 128
	maxEstimateCandidateBytes = 512 << 10
)

type candidateRetention struct {
	location  time.Duration
	proximity time.Duration
}

type cachedLocationCandidate struct {
	input    *streampb.AssetInput
	location gen.Location
	size     int
}

type assetEstimateState struct {
	checkpoint *streampb.EstimateCheckpoint
	candidates []cachedLocationCandidate
}

func (s *assetEstimateState) filter() *streampb.EstimateCheckpoint {
	if s == nil {
		return nil
	}
	return s.checkpoint
}

func cacheLocationCandidate(input *streampb.AssetInput) (cachedLocationCandidate, error) {
	location, err := decodeObservationLocation(input.Observation)
	return cachedLocationCandidate{input: input, location: location, size: proto.Size(input)}, err
}

func (c candidateRetention) expired(candidate *streampb.AssetInput, now int64) bool {
	obs := candidate.GetObservation()
	retention := c.location
	if obs.Kind == Proximity {
		retention = c.proximity
	}
	return time.Unix(0, now).Sub(time.Unix(0, obs.ReceivedAtUnixNano)) >= retention
}

func (w *EstimateWorker) estimateConfigured(input *streampb.AssetInput, previous *assetEstimateState, config *locatingConfiguration) (*streampb.MotionEstimate, *assetEstimateState, string, error) {
	if input.Observation.OriginHubId != w.configuration.hubID {
		return nil, nil, "unconfigured_origin", nil
	}
	rules, exists := config.shards[configurationShard(input.TrackableId)].rules[input.TrackableId]
	if !exists {
		return nil, nil, "trackable_unavailable", nil
	}
	if input.Observation.ReceivedAtUnixNano == 0 {
		return nil, nil, "", fmt.Errorf("missing observation receive time")
	}

	filter := previous.filter()
	now := max(filter.GetEvaluatedAtUnixNano(), input.Observation.ReceivedAtUnixNano)
	candidates, outcome, err := w.retention.update(previous, input, now)
	if err != nil {
		return nil, nil, "", err
	}

	var chosen cachedLocationCandidate
	best := -1.0
	evaluateAt := time.Unix(0, now)
	for _, candidate := range candidates {
		loc := candidate.location
		priority := rules.Priority(loc, config.shards[configurationShard(loc.ProviderId)].providers[loc.ProviderId], evaluateAt)
		at := observationTime(candidate.input.Observation)
		if chosen.input == nil || priority > best || priority == best &&
			(at > observationTime(chosen.input.Observation) ||
				at == observationTime(chosen.input.Observation) && bytes.Equal(candidate.input.EventId, filter.GetSelectedInputId())) {
			chosen, best = candidate, priority
		}
	}
	selected := chosen.input
	canAdvance := selected != nil && !bytes.Equal(selected.EventId, filter.GetSelectedInputId()) &&
		(len(rules) > 0 || filter == nil || observationTime(selected.Observation) > filter.ObservedAtUnixNano)

	var result *streampb.MotionEstimate
	var next *streampb.EstimateCheckpoint
	if canAdvance {
		result, next, outcome, err = w.model.estimateLocation(selected, filter, chosen.location, len(rules) > 0)
		if err != nil {
			return nil, nil, "", err
		}
		next.SelectedInputId = selected.EventId
		result.ConfigurationRevision = proto.Int64(config.revision)
		result.TriggerAssetInputId = input.EventId
		identity := append([]byte("configured-motion-estimate:v1:"), result.EventId...)
		identity = binary.AppendVarint(identity, config.revision)
		id := sha256.Sum256(append(identity, input.EventId...))
		result.EventId = id[:]
	} else if filter != nil {
		// Preserve the filter when a lower priority observation changes only the
		// candidate set kept for the next trigger.
		next = &streampb.EstimateCheckpoint{
			SchemaVersion: filter.SchemaVersion, Sequence: filter.Sequence, Frame: filter.Frame,
			ObservedAtUnixNano: filter.ObservedAtUnixNano, Axes: filter.Axes, Position: filter.Position,
			SelectedInputId: filter.SelectedInputId,
			FenceRouting:    filter.FenceRouting, CollisionRouting: filter.CollisionRouting,
		}
	} else {
		return nil, nil, outcome, nil
	}

	next.EvaluatedAtUnixNano = now
	next.Candidates = make([]*streampb.AssetInput, 0, len(candidates))
	for _, candidate := range candidates {
		next.Candidates = append(next.Candidates, candidate.input)
	}
	return result, &assetEstimateState{checkpoint: next, candidates: candidates}, outcome, nil
}

func (c candidateRetention) update(previous *assetEstimateState, input *streampb.AssetInput, now int64) ([]cachedLocationCandidate, string, error) {
	var current []cachedLocationCandidate
	if previous != nil {
		current = previous.candidates
	}
	next := make([]cachedLocationCandidate, 0, len(current)+1)
	replaces := -1
	totalBytes := 0
	obs := input.Observation
	for _, candidate := range current {
		if c.expired(candidate.input, now) {
			continue
		}
		existing := candidate.input.Observation
		if obs.ProviderId == existing.ProviderId && obs.Source == existing.Source {
			replaces = len(next)
		}
		next = append(next, candidate)
		totalBytes += candidate.size
	}

	if c.expired(input, now) || replaces >= 0 && observationTime(next[replaces].input.Observation) >= observationTime(obs) {
		return next, "out_of_order", nil
	}
	if replaces >= 0 {
		totalBytes -= next[replaces].size
	}
	size := proto.Size(input)
	if totalBytes+size > maxEstimateCandidateBytes || replaces < 0 && len(next) >= maxEstimateCandidates {
		return next, "candidate_limit", nil
	}
	location, err := decodeObservationLocation(obs)
	if err != nil {
		return nil, "", err
	}
	candidate := cachedLocationCandidate{input: input, location: location, size: size}
	if replaces >= 0 {
		next[replaces] = candidate
	} else {
		next = append(next, candidate)
		slices.SortFunc(next, func(a, b cachedLocationCandidate) int {
			return cmp.Or(cmp.Compare(a.input.Observation.ProviderId, b.input.Observation.ProviderId),
				cmp.Compare(a.input.Observation.Source, b.input.Observation.Source))
		})
	}
	return next, "no_selection_change", nil
}
