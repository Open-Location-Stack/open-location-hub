package stream

import (
	"bytes"
	"context"
	"fmt"
	"hash/crc32"
	"math"

	"github.com/formation-res/open-location-hub/internal/hub"
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

type collisionPairPartition struct {
	pairs    map[string]*collisionPair
	frontier *streampb.CollisionPairFrontier
}

func (s *checkpointStore) restoreCollisionPairs(ctx context.Context, offsets map[int32]kgo.Offset) (map[int32]*collisionPairPartition, error) {
	partitions := make(map[int32]*collisionPairPartition, len(offsets))
	for partition := range offsets {
		partitions[partition] = &collisionPairPartition{pairs: make(map[string]*collisionPair)}
	}
	if err := s.scan(ctx, offsets, func(record *kgo.Record) error {
		return applyCollisionPairCheckpoint(partitions[record.Partition], record, s.partitions)
	}); err != nil {
		return nil, err
	}
	for partition, offset := range offsets {
		state := partitions[partition]
		committed := offset.EpochOffset().Offset
		frontier := state.frontier
		if frontier == nil && committed < 0 && len(state.pairs) == 0 {
			continue
		}
		if frontier == nil || frontier.NextOffset != committed || !bytes.Equal(frontier.InputTopicId, s.inputID[:]) ||
			!bytes.Equal(frontier.TransitionTopicId, s.outputID[:]) || frontier.TopologyVersion != collisionTopologyVersion {
			return nil, fmt.Errorf("collision pair checkpoint for partition %d does not match topic generations and committed offset %d", partition, committed)
		}
	}
	return partitions, nil
}

func applyCollisionPairCheckpoint(state *collisionPairPartition, record *kgo.Record, partitions int32) error {
	if bytes.Equal(record.Key, frontierKey) {
		state.frontier = new(streampb.CollisionPairFrontier)
		return proto.Unmarshal(record.Value, state.frontier)
	}
	if len(record.Key) == 0 {
		return fmt.Errorf("unkeyed collision pair checkpoint")
	}
	if record.Key[0] == 0 {
		return nil
	}
	if record.Value == nil {
		delete(state.pairs, string(record.Key))
		return nil
	}
	var value streampb.CollisionPairCheckpoint
	if err := proto.Unmarshal(record.Value, &value); err != nil {
		return err
	}
	if err := validatePairSamples(value.Left, value.Right); err != nil {
		return err
	}
	if !bytes.Equal(record.Key, collisionPairKey(value.Left, value.Right)) ||
		record.Partition != int32(crc32.ChecksumIEEE(record.Key)%uint32(partitions)) {
		return fmt.Errorf("misplaced collision pair checkpoint")
	}
	if value.PhysicalArea > uint32(hub.FenceOutside) ||
		value.EvaluatedAtUnixNano < max(value.Left.ProcessedAtUnixNano, value.Right.ProcessedAtUnixNano) ||
		value.StartedAtUnixNano < 0 || value.StartedAtUnixNano > value.EvaluatedAtUnixNano ||
		math.IsNaN(value.CenterDistanceMeters) || math.IsInf(value.CenterDistanceMeters, 0) || value.CenterDistanceMeters < 0 ||
		(value.LeadingTrackableId != value.Left.TrackableId && value.LeadingTrackableId != value.Right.TrackableId) {
		return fmt.Errorf("invalid collision pair checkpoint")
	}
	left, err := mergeCollisionPose(nil, value.Left)
	if err != nil {
		return err
	}
	right, err := mergeCollisionPose(nil, value.Right)
	if err != nil {
		return err
	}
	pair := &collisionPair{
		left: left, right: right, startedAt: timestampOrZero(value.StartedAtUnixNano), evaluatedAt: timestampOrZero(value.EvaluatedAtUnixNano),
		distance: value.CenterDistanceMeters, leading: value.LeadingTrackableId,
		deadlines: hub.FenceDeadlines{Area: hub.FenceArea(value.PhysicalArea),
			Tolerance: timestampOrZero(value.ToleranceDeadlineUnixNano), Outside: timestampOrZero(value.OutsideDeadlineUnixNano)},
	}
	if next := pair.nextDeadline(); !next.IsZero() && !next.After(pair.evaluatedAt) {
		return fmt.Errorf("collision pair checkpoint contains an already expired deadline")
	}
	if expiry := pair.deadlines.Next(); !expiry.IsZero() && (pair.startedAt.IsZero() || !value.Left.Eligible || !value.Right.Eligible) {
		return fmt.Errorf("collision deadline has no eligible active pair")
	}
	state.pairs[string(record.Key)] = pair
	return nil
}

func (s *checkpointStore) collisionPairRecords(batch map[int32]*collisionPairPartition) ([]*kgo.Record, error) {
	var records []*kgo.Record
	for partition, updates := range batch {
		for key, state := range updates.pairs {
			var value []byte
			if state != nil {
				var err error
				value, err = proto.Marshal(&streampb.CollisionPairCheckpoint{
					Left: state.left.sample, Right: state.right.sample, EvaluatedAtUnixNano: nanosOrZero(state.evaluatedAt),
					StartedAtUnixNano: nanosOrZero(state.startedAt), PhysicalArea: uint32(state.deadlines.Area),
					ToleranceDeadlineUnixNano: nanosOrZero(state.deadlines.Tolerance), OutsideDeadlineUnixNano: nanosOrZero(state.deadlines.Outside),
					CenterDistanceMeters: state.distance, LeadingTrackableId: state.leading,
				})
				if err != nil {
					return nil, err
				}
			}
			records = append(records, &kgo.Record{Topic: s.topic, Partition: partition, Key: []byte(key), Value: value})
		}
		frontier, err := proto.Marshal(updates.frontier)
		if err != nil {
			return nil, err
		}
		records = append(records, &kgo.Record{Topic: s.topic, Partition: partition, Key: frontierKey, Value: frontier})
	}
	return records, nil
}
