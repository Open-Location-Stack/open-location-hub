package stream

import (
	"bytes"
	"context"
	"fmt"
	"hash/crc32"

	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

type collisionCellPartition struct {
	cells    map[string]*collisionCellState
	frontier *streampb.CollisionCellFrontier
}

type collisionCellChange struct {
	cellKey string
	asset   string
	prior   *collisionCellEntry
}

type collisionCellBatch struct {
	original map[string]collisionCellChange
	frontier *streampb.CollisionCellFrontier
}

func (s *checkpointStore) restoreCollisionCells(ctx context.Context, offsets map[int32]kgo.Offset) (map[int32]*collisionCellPartition, error) {
	partitions := make(map[int32]*collisionCellPartition, len(offsets))
	retained := make(map[int32]map[string]*streampb.CollisionCellInput, len(offsets))
	for partition := range offsets {
		partitions[partition] = &collisionCellPartition{cells: make(map[string]*collisionCellState)}
		retained[partition] = make(map[string]*streampb.CollisionCellInput)
	}
	if err := s.scan(ctx, offsets, func(record *kgo.Record) error {
		if bytes.Equal(record.Key, frontierKey) {
			frontier := new(streampb.CollisionCellFrontier)
			partitions[record.Partition].frontier = frontier
			return proto.Unmarshal(record.Value, frontier)
		}
		if len(record.Key) == 0 {
			return fmt.Errorf("unkeyed collision cell checkpoint")
		}
		if record.Key[0] == 0 {
			return nil
		}
		motions := retained[record.Partition]
		if record.Value == nil {
			delete(motions, string(record.Key))
			return nil
		}
		input := new(streampb.CollisionCellInput)
		if err := proto.Unmarshal(record.Value, input); err != nil {
			return err
		}
		key, err := validateCollisionCellInput(input)
		if err != nil {
			return err
		}
		if input.Action != streampb.CollisionCellInput_ACTION_UPSERT ||
			!bytes.Equal(record.Key, appendKeyPart(key, input.Sample.TrackableId)) ||
			record.Partition != int32(crc32.ChecksumIEEE(key)%uint32(s.partitions)) {
			return fmt.Errorf("misplaced collision cell checkpoint")
		}
		motions[string(record.Key)] = input
		return nil
	}); err != nil {
		return nil, err
	}
	for partition, offset := range offsets {
		state := partitions[partition]
		committed := offset.EpochOffset().Offset
		frontier := state.frontier
		if frontier == nil && committed < 0 && len(retained[partition]) == 0 {
			continue
		}
		if frontier == nil || frontier.NextOffset != committed || !bytes.Equal(frontier.InputTopicId, s.inputID[:]) ||
			!bytes.Equal(frontier.PairTopicId, s.outputID[:]) || frontier.PairPartitions != s.partitions || frontier.TopologyVersion != collisionTopologyVersion {
			return nil, fmt.Errorf("collision checkpoint for partition %d does not match topic generations and committed offset %d", partition, committed)
		}
		for _, input := range retained[partition] {
			key := string(collisionCellKey(input.Sample.OriginHubId, cellFromEnvelope(input.Cell)))
			cell := state.cells[key]
			if cell == nil {
				cell = newCollisionCellState()
				state.cells[key] = cell
			}
			cell.set(input.Sample.TrackableId, newCollisionCellEntry(input))
		}
	}
	return partitions, nil
}

func (s *checkpointStore) collisionCellRecords(batch map[int32]*collisionCellBatch, partitions map[int32]*collisionCellPartition) ([]*kgo.Record, error) {
	var records []*kgo.Record
	for partition, updates := range batch {
		for key, change := range updates.original {
			entry := partitions[partition].cells[change.cellKey].entries[change.asset]
			var value []byte
			if entry != nil {
				var err error
				value, err = proto.Marshal(entry.input)
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
