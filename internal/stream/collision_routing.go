package stream

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"math"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/hub"
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

const collisionTopologyVersion = 1
const collisionCellSizeMeters = 2048
const collisionMaxReachMeters = 1000
const collisionMaxCellsPerAsset = 8

type collisionCell [3]int32

func (cell collisionCell) envelope() *streampb.CollisionCell {
	return &streampb.CollisionCell{X: cell[0], Y: cell[1], Z: cell[2]}
}

func cellFromEnvelope(cell *streampb.CollisionCell) collisionCell {
	return collisionCell{cell.X, cell.Y, cell.Z}
}

func collisionCellKey(origin string, cell collisionCell) []byte {
	key := appendKeyPart(nil, origin)
	for _, coordinate := range cell {
		key = binary.BigEndian.AppendUint32(key, uint32(coordinate))
	}
	return key
}

func collisionCells(position [3]float64, reach float64) []collisionCell {
	var low, high collisionCell
	// The margin mirrors the Earth-centered compatibility index and covers
	// conversion and floating-point error at tangencies.
	reach = 1.02*reach + 1e-6
	for axis, coordinate := range position {
		low[axis] = int32(math.Floor((coordinate - reach) / collisionCellSizeMeters))
		high[axis] = int32(math.Floor((coordinate + reach) / collisionCellSizeMeters))
	}
	cells := make([]collisionCell, 0, collisionMaxCellsPerAsset)
	for x := low[0]; x <= high[0]; x++ {
		for y := low[1]; y <= high[1]; y++ {
			for z := low[2]; z <= high[2]; z++ {
				cells = append(cells, collisionCell{x, y, z})
			}
		}
	}
	return cells
}

type collisionRouter struct {
	topic      string
	topicID    [16]byte
	partitions int32
	ttl        time.Duration
}

func (r collisionRouter) route(ctx context.Context, estimate *streampb.MotionEstimate, prior *streampb.CollisionRoutingCheckpoint, config *locatingConfiguration, observedAt int64) ([]*kgo.Record, *streampb.CollisionRoutingCheckpoint, string, error) {
	processedAt := max(observedAt, prior.GetProcessedAtUnixNano())
	if processedAt <= 0 || int64(r.ttl) > math.MaxInt64-processedAt {
		return nil, nil, "", fmt.Errorf("invalid collision routing clock")
	}
	var location gen.Location
	if err := json.Unmarshal(estimate.LocationJson, &location); err != nil {
		return nil, nil, "", fmt.Errorf("decode collision location: %w", err)
	}
	location, projected := config.geometry.catalog.Frame(ctx, location)
	trackable, found := config.shards[configurationShard(estimate.TrackableId)].trackables[estimate.TrackableId]
	provider := config.shards[configurationShard(location.ProviderId)].providers[location.ProviderId]
	policy := hub.ResolveCollisionAssetPolicy(trackable, provider)
	position, world := hub.CollisionEarthPosition(location)
	reach := policy.Radius + policy.ExitTolerance
	outcome := "routed"
	var cells []collisionCell
	switch {
	case !found:
		outcome = "unknown_trackable"
	case !world:
		outcome = "unprojectable"
	case !(reach >= 0 && reach <= collisionMaxReachMeters):
		outcome = "reach_exceeded"
	default:
		cells = collisionCells(position, reach)
	}
	state := &streampb.CollisionRoutingCheckpoint{ProcessedAtUnixNano: processedAt}
	if len(cells) == 0 && len(prior.GetCells()) == 0 {
		return nil, state, outcome, nil
	}
	locationJSON := estimate.LocationJson
	if projected {
		var err error
		locationJSON, err = json.Marshal(location)
		if err != nil {
			return nil, nil, "", fmt.Errorf("encode projected collision location: %w", err)
		}
	}
	sample := &streampb.CollisionSample{
		EstimateId: estimate.EventId, OriginHubId: estimate.OriginHubId, TrackableId: estimate.TrackableId,
		Sequence: estimate.Sequence, ConfigurationRevision: config.revision, ProcessedAtUnixNano: processedAt,
		ExpiresAtUnixNano: processedAt + int64(r.ttl), LocationJson: locationJSON,
		RadiusMeters: policy.Radius, ExtrusionMeters: trackable.Extrusion, ExitToleranceMeters: policy.ExitTolerance,
		ToleranceTimeoutNanos: policy.ToleranceTimeoutNanos, ExitDelayNanos: policy.ExitDelayNanos, Eligible: len(cells) > 0,
	}
	if world {
		sample.EcefMeters = position[:]
	}
	current := make(map[collisionCell]struct{}, len(cells))
	records := make([]*kgo.Record, 0, len(cells)+len(prior.GetCells()))
	for _, cell := range cells {
		current[cell] = struct{}{}
		state.Cells = append(state.Cells, cell.envelope())
		record, err := r.record(cell, sample, streampb.CollisionCellInput_ACTION_UPSERT)
		if err != nil {
			return nil, nil, "", err
		}
		records = append(records, record)
	}
	for _, saved := range prior.GetCells() {
		if saved == nil {
			return nil, nil, "", fmt.Errorf("invalid prior collision cell")
		}
		cell := cellFromEnvelope(saved)
		if _, retained := current[cell]; retained {
			continue
		}
		record, err := r.record(cell, sample, streampb.CollisionCellInput_ACTION_WITHDRAW)
		if err != nil {
			return nil, nil, "", err
		}
		records = append(records, record)
	}
	return records, state, outcome, nil
}

func (r collisionRouter) record(cell collisionCell, sample *streampb.CollisionSample, action streampb.CollisionCellInput_Action) (*kgo.Record, error) {
	key := collisionCellKey(sample.OriginHubId, cell)
	value, err := proto.Marshal(&streampb.CollisionCellInput{
		SchemaVersion: 1, TopologyVersion: collisionTopologyVersion,
		EventId: collisionCellInputID(key, sample.EstimateId, action), Cell: cell.envelope(), Sample: sample, Action: action,
	})
	if err != nil {
		return nil, err
	}
	return &kgo.Record{Topic: r.topic, Partition: int32(crc32.ChecksumIEEE(key) % uint32(r.partitions)), Key: key, Value: value}, nil
}

func collisionCellInputID(key, estimateID []byte, action streampb.CollisionCellInput_Action) []byte {
	cause := append(append([]byte("collision-cell:v1:"), byte(action)), estimateID...)
	id := sha256.Sum256(append(cause, key...))
	return id[:]
}
