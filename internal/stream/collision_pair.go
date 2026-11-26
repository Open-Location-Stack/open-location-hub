package stream

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/hub"
	"github.com/formation-res/open-location-hub/internal/ids"
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"github.com/google/uuid"
)

type collisionPose struct {
	sample    *streampb.CollisionSample
	motion    gen.TrackableMotion
	trackable gen.Trackable
	policy    hub.CollisionAssetPolicy
}

type collisionPair struct {
	left, right *collisionPose
	evaluatedAt time.Time
	startedAt   time.Time
	deadlines   hub.FenceDeadlines
	distance    float64
	leading     string
}

func evaluateCollisionPair(input *streampb.CollisionPairInput, previous *collisionPair, appended time.Time) (*collisionPair, []*streampb.CollisionTransition, string, error) {
	if input.DeadlineAtUnixNano != 0 {
		at := timestampOrZero(input.DeadlineAtUnixNano)
		if previous == nil || !previous.nextDeadline().Equal(at) ||
			!bytes.Equal(previous.generation(), input.DeadlineGeneration) {
			return previous, nil, "obsolete_deadline", nil
		}
		return previous.expire(at)
	}
	var lastLeft, lastRight *collisionPose
	if previous != nil {
		lastLeft, lastRight = previous.left, previous.right
	}
	left, err := mergeCollisionPose(lastLeft, input.Left)
	if err != nil {
		return nil, nil, "", err
	}
	right, err := mergeCollisionPose(lastRight, input.Right)
	if err != nil {
		return nil, nil, "", err
	}
	if left == lastLeft && right == lastRight {
		return previous, nil, "duplicate", nil
	}
	at := timestampOrZero(max(left.sample.ProcessedAtUnixNano, right.sample.ProcessedAtUnixNano))
	if previous != nil && previous.evaluatedAt.After(at) {
		at = previous.evaluatedAt
	}
	// Broker append time rejects a candidate that arrived after its source
	// samples expired, including duplicates arriving after checkpoint cleanup.
	if min(left.sample.ExpiresAtUnixNano, right.sample.ExpiresAtUnixNano) <= max(appended.UnixNano(), at.UnixNano()) {
		return previous, nil, "stale_samples", nil
	}
	leading := left.sample.TrackableId
	if left == lastLeft || right != lastRight &&
		(input.UpdatedTrackableId == right.sample.TrackableId ||
			input.UpdatedTrackableId == "" && right.sample.ProcessedAtUnixNano > left.sample.ProcessedAtUnixNano) {
		leading = right.sample.TrackableId
	}
	state := &collisionPair{left: left, right: right, evaluatedAt: at, leading: leading}
	var events []*streampb.CollisionTransition
	if previous != nil && !previous.startedAt.IsZero() {
		if deadline := previous.deadlines.Next(); !deadline.IsZero() && !deadline.After(at) {
			_, expired, _, err := previous.expire(deadline)
			if err != nil {
				return nil, nil, "", err
			}
			events = append(events, expired...)
		} else {
			state.startedAt, state.deadlines, state.distance = previous.startedAt, previous.deadlines, previous.distance
		}
	}
	if !left.sample.Eligible || !right.sample.Eligible {
		// An unsupported pose cannot drive an exact contact or an exit. Retain
		// the versions and suspend deadlines until an eligible pair arrives.
		state.deadlines = hub.FenceDeadlines{}
		return state, events, "ineligible", nil
	}
	contact, err := hub.CollisionContactForPair(left.motion, left.trackable, right.motion, right.trackable)
	if err != nil {
		return nil, nil, "", err
	}
	state.distance = contact.Distance
	if state.startedAt.IsZero() && !contact.Inside {
		return state, events, "separate", nil
	}
	kind, outcome := gen.Colliding, "retained"
	if state.startedAt.IsZero() {
		state.startedAt, kind, outcome = at, gen.CollisionStart, "start"
	}
	policy := hub.CollisionPolicyForPair(left.policy, right.policy)
	state.deadlines = state.deadlines.Advance(at, contact.ExitArea(policy.ExitTolerance), policy)
	if deadline := state.deadlines.Next(); !deadline.IsZero() && !deadline.After(at) {
		next, expired, result, err := state.expire(deadline)
		return next, append(events, expired...), result, err
	}
	if contact.Inside {
		event, err := state.event(kind, state.generation(), at, contact.Area)
		if err != nil {
			return nil, nil, "", err
		}
		events = append(events, event)
		if kind == gen.Colliding {
			outcome = "colliding"
		}
	}
	return state, events, outcome, nil
}

func mergeCollisionPose(prior *collisionPose, input *streampb.CollisionSample) (*collisionPose, error) {
	if prior != nil {
		if input.Sequence < prior.sample.Sequence {
			return prior, nil
		}
		if input.Sequence == prior.sample.Sequence {
			if !bytes.Equal(input.EstimateId, prior.sample.EstimateId) {
				return nil, fmt.Errorf("collision sample sequence %d has conflicting identities", input.Sequence)
			}
			return prior, nil
		}
		if input.ProcessedAtUnixNano < prior.sample.ProcessedAtUnixNano || input.ConfigurationRevision < prior.sample.ConfigurationRevision {
			return nil, fmt.Errorf("collision sample sequence %d regresses time or configuration", input.Sequence)
		}
	}
	result := &collisionPose{sample: input}
	if !input.Eligible {
		return result, nil
	}
	id, err := uuid.Parse(input.TrackableId)
	if err != nil {
		return nil, fmt.Errorf("invalid collision trackable ID: %w", err)
	}
	result.motion.Id = id.String()
	if err := json.Unmarshal(input.LocationJson, &result.motion.Location); err != nil {
		return nil, fmt.Errorf("decode pair location: %w", err)
	}
	if _, ok := hub.CollisionEarthPosition(result.motion.Location); !ok {
		return nil, fmt.Errorf("eligible collision sample has no WGS84 location")
	}
	result.trackable = gen.Trackable{Id: id, Radius: &input.RadiusMeters, Extrusion: input.ExtrusionMeters}
	if len(input.GeometryJson) > 0 {
		shape := new(gen.Polygon)
		if err := json.Unmarshal(input.GeometryJson, shape); err != nil {
			return nil, fmt.Errorf("decode trackable shape: %w", err)
		}
		result.motion.Geometry = shape
	}
	result.policy = hub.CollisionAssetPolicy{
		Radius: input.RadiusMeters, ExitTolerance: input.ExitToleranceMeters,
		ToleranceTimeoutNanos: input.ToleranceTimeoutNanos, ExitDelayNanos: input.ExitDelayNanos,
	}
	return result, nil
}

func (p *collisionPair) generation() []byte {
	return collisionPairVersionID(p.left.sample, p.right.sample)
}

func (p *collisionPair) nextDeadline() time.Time {
	if !p.startedAt.IsZero() {
		return p.deadlines.Next()
	}
	return timestampOrZero(max(p.left.sample.ExpiresAtUnixNano, p.right.sample.ExpiresAtUnixNano))
}

func (p *collisionPair) expire(at time.Time) (*collisionPair, []*streampb.CollisionTransition, string, error) {
	if p.startedAt.IsZero() {
		return nil, nil, "expired_versions", nil
	}
	id := collisionPairDeadlineID(p.generation(), at)
	event, err := p.event(gen.CollisionEnd, id[:], at, nil)
	if err != nil {
		return nil, nil, "", err
	}
	next := *p
	next.startedAt, next.evaluatedAt, next.deadlines = time.Time{}, at, hub.FenceDeadlines{}
	if !next.nextDeadline().After(at) {
		return nil, []*streampb.CollisionTransition{event}, "end", nil
	}
	return &next, []*streampb.CollisionTransition{event}, "end", nil
}

func (p *collisionPair) event(kind gen.CollisionEventCollisionType, cause []byte, at time.Time, area *gen.CollisionEvent_CollisionArea) (*streampb.CollisionTransition, error) {
	id := sha256.Sum256(append(appendKeyPart([]byte("collision-transition:v1:"), string(kind)), cause...))
	left, right := p.left, p.right
	if p.leading == right.sample.TrackableId {
		left, right = right, left
	}
	event := hub.CollisionEventForPair(ids.EventUUID(at, id), kind, at, p.startedAt, left.motion, left.trackable, right.motion, right.trackable, area, p.distance)
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("encode collision event: %w", err)
	}
	return &streampb.CollisionTransition{
		SchemaVersion: 1, EventId: id[:], CauseId: cause, Left: p.left.sample, Right: p.right.sample,
		OccurredAtUnixNano: at.UnixNano(), CollisionEventJson: payload,
	}, nil
}

func collisionPairDeadlineID(generation []byte, at time.Time) [32]byte {
	identity := append([]byte("collision-pair-deadline:v1:"), generation...)
	return sha256.Sum256(binary.AppendVarint(identity, at.UnixNano()))
}

func validatePairSamples(left, right *streampb.CollisionSample) error {
	if left == nil || right == nil || left.OriginHubId == "" || left.OriginHubId != right.OriginHubId ||
		left.TrackableId == "" || left.TrackableId >= right.TrackableId {
		return fmt.Errorf("invalid collision pair scope")
	}
	for _, sample := range []*streampb.CollisionSample{left, right} {
		if len(sample.EstimateId) != sha256.Size || sample.Sequence == 0 || sample.ProcessedAtUnixNano <= 0 ||
			sample.ExpiresAtUnixNano <= sample.ProcessedAtUnixNano || sample.ConfigurationRevision < 0 {
			return fmt.Errorf("invalid collision sample identity")
		}
		if sample.Eligible && (!(sample.RadiusMeters >= 0 && sample.ExitToleranceMeters >= 0 &&
			sample.RadiusMeters+sample.ExitToleranceMeters <= collisionMaxReachMeters) ||
			sample.ToleranceTimeoutNanos < -1 || sample.ExitDelayNanos < -1 ||
			(sample.ExtrusionMeters != nil && (math.IsNaN(*sample.ExtrusionMeters) || math.IsInf(*sample.ExtrusionMeters, 0) || *sample.ExtrusionMeters < 0))) {
			return fmt.Errorf("invalid eligible collision extent or timer")
		}
	}
	return nil
}
