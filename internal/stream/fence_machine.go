package stream

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/hub"
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
)

type fenceMembership struct {
	observation *streampb.FenceEvaluation
	entered     time.Time
	deadlines   hub.FenceDeadlines
}

type fenceMachine struct {
	geometry      map[string]gen.Fence
	geometryBytes int
}

func (m *fenceMachine) evaluate(input *streampb.FenceEvaluation, previous *fenceMembership) (*fenceMembership, []*streampb.FenceTransition, string, error) {
	at := time.Unix(0, input.ProcessedAtUnixNano).UTC()
	if input.Action == streampb.FenceEvaluation_ACTION_DEADLINE {
		if previous == nil || !bytes.Equal(previous.observation.EventId, input.DeadlineGeneration) ||
			previous.deadlines.Next().IsZero() || !previous.deadlines.Next().Equal(at) {
			return previous, nil, "stale_deadline", nil
		}
		return nil, []*streampb.FenceTransition{previous.expire(at)}, "exit", nil
	}
	if previous != nil && (input.AssetSequence <= previous.observation.AssetSequence ||
		input.ProcessedAtUnixNano < previous.observation.ProcessedAtUnixNano ||
		input.ConfigurationRevision < previous.observation.ConfigurationRevision) {
		return nil, nil, "", fmt.Errorf("fence observation precedes active membership")
	}
	var transitions []*streampb.FenceTransition
	if previous != nil {
		if deadline := previous.deadlines.Next(); !deadline.IsZero() && !deadline.After(at) {
			transitions = append(transitions, previous.expire(deadline))
			previous = nil
		}
	}
	if input.Action == streampb.FenceEvaluation_ACTION_CANCEL {
		return nil, transitions, "cancelled", nil
	}
	area, err := m.classify(input)
	if err != nil {
		return nil, nil, "", err
	}
	if previous == nil && area != hub.FenceInside {
		return nil, transitions, "outside", nil
	}
	next := &fenceMembership{observation: input, entered: at}
	policy := hub.FenceRoutingPolicy{
		FenceTimeoutNanos: input.FenceTimeoutNanos, ToleranceTimeoutNanos: input.ToleranceTimeoutNanos, ExitDelayNanos: input.ExitDelayNanos,
	}
	outcome := "retained"
	if previous == nil {
		transitions = append(transitions, next.transition(streampb.FenceTransition_KIND_ENTRY, input.EventId, at))
		outcome = "entry"
	} else {
		next.entered = previous.entered
		next.deadlines = previous.deadlines
	}
	next.deadlines = next.deadlines.Advance(at, area, policy)
	if deadline := next.deadlines.Next(); !deadline.IsZero() && !deadline.After(at) {
		return nil, append(transitions, next.expire(deadline)), "exit", nil
	}
	return next, transitions, outcome, nil
}

func (m *fenceMachine) classify(input *streampb.FenceEvaluation) (hub.FenceArea, error) {
	var location gen.Location
	if err := json.Unmarshal(input.LocationJson, &location); err != nil {
		return hub.FenceOutside, fmt.Errorf("decode fence observation location: %w", err)
	}
	fence, ok := m.geometry[string(input.FenceJson)]
	if !ok {
		if err := json.Unmarshal(input.FenceJson, &fence); err != nil {
			return hub.FenceOutside, fmt.Errorf("decode fence observation geometry: %w", err)
		}
		if fence.Id.String() != input.FenceId {
			return hub.FenceOutside, fmt.Errorf("fence geometry ID differs from membership key")
		}
		const maxCacheBytes = 8 * 1024 * 1024
		if len(input.FenceJson) <= maxCacheBytes {
			if len(m.geometry) >= 512 || m.geometryBytes+len(input.FenceJson) > maxCacheBytes {
				clear(m.geometry)
				m.geometryBytes = 0
			}
			m.geometry[string(input.FenceJson)] = fence
			m.geometryBytes += len(input.FenceJson)
		}
	} else if fence.Id.String() != input.FenceId {
		return hub.FenceOutside, fmt.Errorf("fence geometry ID differs from membership key")
	}
	return hub.ClassifyFence(fence, location, input.TrackableRadius, input.TrackableExtrusion, input.ExitTolerance)
}

func (state *fenceMembership) expire(at time.Time) *streampb.FenceTransition {
	id := fenceDeadlineID(state.observation.EventId, at)
	return state.transition(streampb.FenceTransition_KIND_EXIT, id[:], at)
}

func (state *fenceMembership) transition(kind streampb.FenceTransition_Kind, cause []byte, at time.Time) *streampb.FenceTransition {
	id := sha256.Sum256(append(append([]byte("fence-transition:v1:"), byte(kind)), cause...))
	return &streampb.FenceTransition{
		SchemaVersion: 1, EventId: id[:], CauseId: cause, Kind: kind,
		Observation: state.observation, EnteredAtUnixNano: state.entered.UnixNano(), OccurredAtUnixNano: at.UnixNano(),
	}
}

func fenceDeadlineID(generation []byte, at time.Time) [32]byte {
	identity := append([]byte("fence-deadline:v1:"), generation...)
	return sha256.Sum256(binary.AppendVarint(identity, at.UnixNano()))
}

func fenceMembershipKey(origin, fence, trackable string) []byte {
	key := appendKeyPart(nil, origin)
	key = appendKeyPart(key, fence)
	return appendKeyPart(key, trackable)
}
