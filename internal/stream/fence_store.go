package stream

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/formation-res/open-location-hub/internal/hub"
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

type partitionMemberships struct {
	members  map[string]*fenceMembership
	frontier *streampb.FenceMembershipFrontier
}

func (s *checkpointStore) restoreFences(ctx context.Context, offsets map[int32]kgo.Offset) (map[int32]*partitionMemberships, error) {
	partitions := make(map[int32]*partitionMemberships, len(offsets))
	for partition := range offsets {
		partitions[partition] = &partitionMemberships{members: make(map[string]*fenceMembership)}
	}
	if err := s.scan(ctx, offsets, func(record *kgo.Record) error {
		return applyFenceCheckpoint(partitions[record.Partition], record, s.partitions)
	}); err != nil {
		return nil, err
	}
	for partition, offset := range offsets {
		state := partitions[partition]
		committed := offset.EpochOffset().Offset
		if state.frontier == nil && committed < 0 && len(state.members) == 0 {
			continue
		}
		if state.frontier == nil || state.frontier.NextOffset != committed ||
			!bytes.Equal(state.frontier.InputTopicId, s.inputID[:]) || !bytes.Equal(state.frontier.TransitionTopicId, s.outputID[:]) ||
			state.frontier.TopologyVersion != fenceTopologyVersion {
			return nil, fmt.Errorf("fence checkpoint for partition %d does not match the topic generations and committed offset %d", partition, committed)
		}
	}
	return partitions, nil
}

func applyFenceCheckpoint(partition *partitionMemberships, record *kgo.Record, partitionCount int32) error {
	if bytes.Equal(record.Key, frontierKey) {
		partition.frontier = new(streampb.FenceMembershipFrontier)
		return proto.Unmarshal(record.Value, partition.frontier)
	}
	if len(record.Key) == 0 {
		return fmt.Errorf("unkeyed fence checkpoint")
	}
	if record.Key[0] == 0 {
		return nil
	}
	if record.Value == nil {
		delete(partition.members, string(record.Key))
		return nil
	}
	var checkpoint streampb.FenceMembershipCheckpoint
	if err := proto.Unmarshal(record.Value, &checkpoint); err != nil {
		return err
	}
	input := checkpoint.Observation
	if input == nil || input.Action != streampb.FenceEvaluation_ACTION_EVALUATE || checkpoint.PhysicalArea > uint32(hub.FenceOutside) ||
		checkpoint.EnteredAtUnixNano <= 0 || checkpoint.EnteredAtUnixNano > input.ProcessedAtUnixNano {
		return fmt.Errorf("invalid fence membership checkpoint")
	}
	if err := validateFenceInput(record, input, partitionCount); err != nil {
		return err
	}
	state := &fenceMembership{
		observation: input, entered: timestampOrZero(checkpoint.EnteredAtUnixNano),
		deadlines: hub.FenceDeadlines{
			Area: hub.FenceArea(checkpoint.PhysicalArea), Fence: timestampOrZero(checkpoint.FenceDeadlineUnixNano),
			Tolerance: timestampOrZero(checkpoint.ToleranceDeadlineUnixNano), Outside: timestampOrZero(checkpoint.OutsideDeadlineUnixNano),
		},
	}
	if at := state.deadlines.Next(); !at.IsZero() && at.UnixNano() <= input.ProcessedAtUnixNano {
		return fmt.Errorf("fence checkpoint contains an already expired membership")
	}
	partition.members[string(record.Key)] = state
	return nil
}

func (s *checkpointStore) fenceRecords(batch map[int32]*partitionMemberships) ([]*kgo.Record, error) {
	var records []*kgo.Record
	for partition, updates := range batch {
		for key, state := range updates.members {
			var value []byte
			if state != nil {
				var err error
				value, err = proto.Marshal(&streampb.FenceMembershipCheckpoint{
					Observation: state.observation, EnteredAtUnixNano: state.entered.UnixNano(), PhysicalArea: uint32(state.deadlines.Area),
					FenceDeadlineUnixNano: nanosOrZero(state.deadlines.Fence), ToleranceDeadlineUnixNano: nanosOrZero(state.deadlines.Tolerance),
					OutsideDeadlineUnixNano: nanosOrZero(state.deadlines.Outside),
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

func timestampOrZero(nanos int64) time.Time {
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos).UTC()
}

func nanosOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}
