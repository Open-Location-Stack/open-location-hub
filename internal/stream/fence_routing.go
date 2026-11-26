package stream

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"maps"
	"math"
	"slices"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/hub"
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

const fenceTopologyVersion = 1
const maxFenceInterests = 2048

type fenceRouter struct {
	topic      string
	topicID    [16]byte
	partitions int32
}

func (r fenceRouter) route(ctx context.Context, estimate *streampb.MotionEstimate, prior *streampb.FenceRoutingCheckpoint, config *locatingConfiguration, observedAt int64) ([]*kgo.Record, *streampb.FenceRoutingCheckpoint, error) {
	var location gen.Location
	if err := json.Unmarshal(estimate.LocationJson, &location); err != nil {
		return nil, nil, fmt.Errorf("decode fence routing location: %w", err)
	}
	catalog := config.geometry.catalog
	location, projected := catalog.Frame(ctx, location)
	trackable := config.shards[configurationShard(estimate.TrackableId)].trackables[estimate.TrackableId]
	provider := config.shards[configurationShard(location.ProviderId)].providers[location.ProviderId]
	candidates, err := catalog.Candidates(location, trackable)
	if err != nil {
		return nil, nil, err
	}
	processedAt := max(observedAt, prior.GetProcessedAtUnixNano())
	if processedAt <= 0 {
		return nil, nil, fmt.Errorf("missing fence routing processing time")
	}

	current := make(map[string]*streampb.FenceRoutingCheckpoint_Interest, len(candidates)+len(prior.GetInterests()))
	for _, candidate := range candidates {
		id := candidate.Id.String()
		current[id] = &streampb.FenceRoutingCheckpoint_Interest{FenceId: id}
	}
	for _, interest := range prior.GetInterests() {
		id := interest.GetFenceId()
		if _, ok := current[id]; ok {
			continue
		}
		if prior.GetConfigurationRevision() == config.revision {
			if interest.CertainlyExpiredAtUnixNano != nil && interest.GetCertainlyExpiredAtUnixNano() <= processedAt {
				continue
			}
			current[id] = interest
		} else {
			current[id] = &streampb.FenceRoutingCheckpoint_Interest{FenceId: id}
		}
	}
	if len(current) > maxFenceInterests {
		return nil, nil, fmt.Errorf("asset exceeds the limit of %d possible fence memberships", maxFenceInterests)
	}

	locationJSON := estimate.LocationJson
	if projected {
		locationJSON = nil
	}
	records := make([]*kgo.Record, 0, len(current))
	state := &streampb.FenceRoutingCheckpoint{
		ProcessedAtUnixNano: processedAt, ConfigurationRevision: config.revision,
		Interests: make([]*streampb.FenceRoutingCheckpoint_Interest, 0, len(current)),
	}
	for _, id := range slices.Sorted(maps.Keys(current)) {
		interest := current[id]
		fence, exists, comparable := catalog.Resolve(id, location)
		if exists && !comparable {
			state.Interests = append(state.Interests, interest)
			continue
		}
		input := &streampb.FenceEvaluation{
			SchemaVersion: 1, EstimateId: estimate.EventId, OriginHubId: estimate.OriginHubId,
			FenceId: id, TrackableId: estimate.TrackableId, AssetSequence: estimate.Sequence,
			ConfigurationRevision: config.revision, TopologyVersion: fenceTopologyVersion,
			ProcessedAtUnixNano: processedAt,
		}
		if !exists || !hub.FenceFloorMatches(fence, location) {
			input.Action = streampb.FenceEvaluation_ACTION_CANCEL
		} else {
			policy := hub.ResolveFenceRoutingPolicy(fence, trackable, provider)
			input.Action = streampb.FenceEvaluation_ACTION_EVALUATE
			if locationJSON == nil {
				locationJSON, err = json.Marshal(location)
				if err != nil {
					return nil, nil, fmt.Errorf("encode projected location: %w", err)
				}
			}
			input.LocationJson = locationJSON
			input.FenceJson = catalog.Encoded(id, location)
			if input.FenceJson == nil {
				return nil, nil, fmt.Errorf("missing encoded spatial fence %s", id)
			}
			input.TrackableRadius = policy.Radius
			input.TrackableExtrusion = trackable.Extrusion
			input.ExitTolerance = policy.ExitTolerance
			input.FenceTimeoutNanos = policy.FenceTimeoutNanos
			input.ToleranceTimeoutNanos = policy.ToleranceTimeoutNanos
			input.ExitDelayNanos = policy.ExitDelayNanos

			// This upper bound is valid only while observations remain certainly
			// outside and the configuration is unchanged. Other timers can clear
			// membership sooner, but cannot prolong this outside deadline.
			retained := &streampb.FenceRoutingCheckpoint_Interest{FenceId: id}
			if policy.ExitDelayNanos >= 0 && policy.ExitDelayNanos <= math.MaxInt64-processedAt &&
				catalog.CertainlyOutside(fence, location, policy.Radius+policy.ExitTolerance) {
				if interest.CertainlyExpiredAtUnixNano != nil {
					retained.CertainlyExpiredAtUnixNano = interest.CertainlyExpiredAtUnixNano
				} else {
					retained.CertainlyExpiredAtUnixNano = proto.Int64(processedAt + policy.ExitDelayNanos)
				}
			}
			if retained.CertainlyExpiredAtUnixNano == nil || retained.GetCertainlyExpiredAtUnixNano() > processedAt {
				state.Interests = append(state.Interests, retained)
			}
		}
		record, err := r.record(input)
		if err != nil {
			return nil, nil, err
		}
		records = append(records, record)
	}
	return records, state, nil
}

func (r fenceRouter) record(input *streampb.FenceEvaluation) (*kgo.Record, error) {
	key := fenceMembershipKey(input.OriginHubId, input.FenceId, input.TrackableId)
	cause := append([]byte("fence-evaluation:v1:"), input.EstimateId...)
	id := sha256.Sum256(append(cause, key...))
	input.EventId = id[:]
	value, err := proto.Marshal(input)
	if err != nil {
		return nil, err
	}
	return &kgo.Record{Topic: r.topic, Partition: int32(crc32.ChecksumIEEE(key) % uint32(r.partitions)), Key: key, Value: value}, nil
}
