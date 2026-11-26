package stream

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"hash/crc32"
	"maps"
	"math"
	"slices"

	"github.com/dhconnelly/rtreego"
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

const maxCollisionCandidates = 256

type collisionCellEntry struct {
	input *streampb.CollisionCellInput
	rect  rtreego.Rect
}

func (e *collisionCellEntry) Bounds() rtreego.Rect { return e.rect }

type collisionCellState struct {
	tree    *rtreego.Rtree
	entries map[string]*collisionCellEntry
}

func newCollisionCellState() *collisionCellState {
	return &collisionCellState{tree: rtreego.NewTree(3, 8, 32), entries: make(map[string]*collisionCellEntry)}
}

func (c *collisionCellState) set(asset string, entry *collisionCellEntry) {
	if prior := c.entries[asset]; prior != nil {
		c.tree.Delete(prior)
	}
	if entry == nil {
		delete(c.entries, asset)
		return
	}
	c.entries[asset] = entry
	c.tree.Insert(entry)
}

func (c *collisionCellState) evaluate(input *streampb.CollisionCellInput) (*collisionCellEntry, []*streampb.CollisionSample, string) {
	sample := input.Sample
	prior := c.entries[sample.TrackableId]
	if input.Action == streampb.CollisionCellInput_ACTION_EXPIRE {
		if prior != nil && bytes.Equal(prior.input.Sample.EstimateId, sample.EstimateId) &&
			prior.input.Sample.ExpiresAtUnixNano == sample.ExpiresAtUnixNano {
			return nil, nil, "expired"
		}
		return prior, nil, "obsolete"
	}
	if prior != nil && sample.Sequence <= prior.input.Sample.Sequence {
		return prior, nil, "obsolete"
	}
	var next *collisionCellEntry
	if input.Action == streampb.CollisionCellInput_ACTION_UPSERT {
		next = newCollisionCellEntry(input)
	}
	candidates := make(map[string]*streampb.CollisionSample)
	for _, boundary := range []*collisionCellEntry{prior, next} {
		if boundary == nil {
			continue
		}
		filter := func(results []rtreego.Spatial, spatial rtreego.Spatial) (bool, bool) {
			other := spatial.(*collisionCellEntry).input.Sample
			at := max(other.ProcessedAtUnixNano, sample.ProcessedAtUnixNano)
			return other.TrackableId == sample.TrackableId ||
				other.ExpiresAtUnixNano <= at || sample.ExpiresAtUnixNano <= at, false
		}
		matches := c.tree.SearchIntersect(boundary.rect, filter, rtreego.LimitFilter(maxCollisionCandidates+1))
		for _, match := range matches {
			other := match.(*collisionCellEntry).input.Sample
			candidates[other.TrackableId] = other
		}
		if len(candidates) > maxCollisionCandidates {
			return next, nil, "candidate_limit"
		}
	}
	// Check the old and new geometry against the same cached counterpart.
	// A jump can therefore report departure even when the assets no longer
	// share a cell. Pair owners merge the facts by the two asset sequences.
	out := make([]*streampb.CollisionSample, 0, len(candidates))
	for _, id := range slices.Sorted(maps.Keys(candidates)) {
		out = append(out, candidates[id])
	}
	return next, out, "evaluated"
}

func newCollisionCellEntry(input *streampb.CollisionCellInput) *collisionCellEntry {
	sample := input.Sample
	extent := 1.02*(sample.RadiusMeters+sample.ExitToleranceMeters) + 1e-6
	return &collisionCellEntry{input: input, rect: rtreego.Point(sample.EcefMeters).ToRect(extent)}
}

func validateCollisionCellInput(input *streampb.CollisionCellInput) ([]byte, error) {
	sample := input.Sample
	if input.SchemaVersion != 1 || input.TopologyVersion != collisionTopologyVersion || input.Cell == nil || sample == nil ||
		len(sample.EstimateId) != sha256.Size || sample.OriginHubId == "" || sample.TrackableId == "" || sample.Sequence == 0 ||
		sample.ConfigurationRevision < 0 || sample.ProcessedAtUnixNano <= 0 || sample.ExpiresAtUnixNano <= sample.ProcessedAtUnixNano {
		return nil, fmt.Errorf("invalid collision cell input identity")
	}
	cell := cellFromEnvelope(input.Cell)
	switch input.Action {
	case streampb.CollisionCellInput_ACTION_UPSERT, streampb.CollisionCellInput_ACTION_WITHDRAW, streampb.CollisionCellInput_ACTION_EXPIRE:
	default:
		return nil, fmt.Errorf("unknown collision cell action %v", input.Action)
	}
	if input.Action != streampb.CollisionCellInput_ACTION_WITHDRAW && !sample.Eligible {
		return nil, fmt.Errorf("ineligible sample cannot enter or expire from the cell index")
	}
	if sample.Eligible {
		if len(sample.EcefMeters) != 3 ||
			!(sample.RadiusMeters >= 0 && sample.ExitToleranceMeters >= 0 && sample.RadiusMeters+sample.ExitToleranceMeters <= collisionMaxReachMeters) {
			return nil, fmt.Errorf("invalid collision cell extent")
		}
		for _, axis := range sample.EcefMeters {
			if math.IsNaN(axis) || math.Abs(axis) > 6371010 {
				return nil, fmt.Errorf("invalid collision Earth position")
			}
		}
		covered := slices.Contains(collisionCells([3]float64(sample.EcefMeters), sample.RadiusMeters+sample.ExitToleranceMeters), cell)
		expectsCoverage := input.Action != streampb.CollisionCellInput_ACTION_WITHDRAW
		if covered != expectsCoverage {
			return nil, fmt.Errorf("collision action conflicts with the sample coverage")
		}
	}
	key := collisionCellKey(sample.OriginHubId, cell)
	if !bytes.Equal(input.EventId, collisionCellInputID(key, sample.EstimateId, input.Action)) {
		return nil, fmt.Errorf("collision cell input ID does not match the sample")
	}
	return key, nil
}

func collisionPairRecord(output string, partitions int32, input *streampb.CollisionCellInput, counterpart *streampb.CollisionSample) (*kgo.Record, error) {
	left, right := input.Sample, counterpart
	if left.TrackableId > right.TrackableId {
		left, right = right, left
	}
	key := collisionPairKey(left, right)
	value, err := proto.Marshal(&streampb.CollisionPairInput{
		SchemaVersion: 1, TopologyVersion: collisionTopologyVersion, EventId: collisionPairVersionID(left, right), TriggerCellInputId: input.EventId,
		Left: left, Right: right, EvaluatedAtUnixNano: max(left.ProcessedAtUnixNano, right.ProcessedAtUnixNano), UpdatedTrackableId: input.Sample.TrackableId,
	})
	if err != nil {
		return nil, err
	}
	return &kgo.Record{Topic: output, Partition: int32(crc32.ChecksumIEEE(key) % uint32(partitions)), Key: key, Value: value}, nil
}

func collisionPairKey(left, right *streampb.CollisionSample) []byte {
	key := appendKeyPart(nil, left.OriginHubId)
	key = appendKeyPart(key, left.TrackableId)
	return appendKeyPart(key, right.TrackableId)
}

func collisionPairVersionID(left, right *streampb.CollisionSample) []byte {
	id := sha256.Sum256(append(append([]byte("collision-pair-input:v1:"), left.EstimateId...), right.EstimateId...))
	return id[:]
}
