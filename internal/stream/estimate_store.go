package stream

import (
	"bytes"
	"context"
	"fmt"

	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

func (s *checkpointStore) estimateRecords(batch map[int32]*partitionEstimates) ([]*kgo.Record, error) {
	var records []*kgo.Record
	for partition, updates := range batch {
		for key, state := range updates.assets {
			encoded, err := proto.Marshal(state.checkpoint)
			if err != nil {
				return nil, fmt.Errorf("marshal estimator checkpoint: %w", err)
			}
			records = append(records, &kgo.Record{Topic: s.topic, Partition: partition, Key: []byte(key), Value: encoded})
		}
		frontier, err := proto.Marshal(updates.frontier)
		if err != nil {
			return nil, fmt.Errorf("marshal estimator frontier: %w", err)
		}
		records = append(records, &kgo.Record{Topic: s.topic, Partition: partition, Key: frontierKey, Value: frontier})
	}
	return records, nil
}

func (s *checkpointStore) restoreEstimates(ctx context.Context, offsets map[int32]kgo.Offset) (map[int32]*partitionEstimates, error) {
	checkpoints := make(map[int32]*partitionEstimates, len(offsets))
	for partition := range offsets {
		checkpoints[partition] = &partitionEstimates{assets: make(map[string]*assetEstimateState)}
	}
	if err := s.scan(ctx, offsets, func(record *kgo.Record) error {
		return applyEstimateCheckpoint(checkpoints[record.Partition], record)
	}); err != nil {
		return nil, err
	}
	for partition, offset := range offsets {
		if err := validateEstimatorFrontier(checkpoints[partition], s.inputID[:], offset.EpochOffset().Offset); err != nil {
			return nil, fmt.Errorf("partition %d: %w", partition, err)
		}
	}
	return checkpoints, nil
}

func applyEstimateCheckpoint(checkpoints *partitionEstimates, record *kgo.Record) error {
	if bytes.Equal(record.Key, frontierKey) {
		frontier := new(streampb.EstimatorFrontier)
		if err := proto.Unmarshal(record.Value, frontier); err != nil {
			return err
		}
		checkpoints.frontier = frontier
		return nil
	}
	if len(record.Key) == 0 {
		return fmt.Errorf("unkeyed record on estimator checkpoint topic")
	}
	if record.Key[0] == 0 {
		// Restore barriers and their tombstones are not estimator state.
		return nil
	}
	if record.Value == nil {
		delete(checkpoints.assets, string(record.Key))
		return nil
	}
	state := new(streampb.EstimateCheckpoint)
	if err := proto.Unmarshal(record.Value, state); err != nil {
		return err
	}
	if state.SchemaVersion != 1 || state.Sequence == 0 || len(state.Frame) == 0 ||
		len(state.Axes) < 2 || len(state.Axes) > 3 || len(state.Position) < 2 || len(state.Position) > 3 {
		return fmt.Errorf("invalid estimator checkpoint")
	}
	restored, err := restoreCandidates(state)
	if err != nil {
		return err
	}
	checkpoints.assets[string(record.Key)] = restored
	return nil
}

func restoreCandidates(checkpoint *streampb.EstimateCheckpoint) (*assetEstimateState, error) {
	if len(checkpoint.Candidates) > maxEstimateCandidates {
		return nil, fmt.Errorf("estimator checkpoint exceeds the candidate limit")
	}
	state := &assetEstimateState{checkpoint: checkpoint}
	totalBytes := 0
	for _, candidate := range checkpoint.Candidates {
		if candidate.GetObservation() == nil || candidate.GetObservation().GetReceivedAtUnixNano() == 0 {
			return nil, fmt.Errorf("invalid retained candidate in estimator checkpoint")
		}
		cached, err := cacheLocationCandidate(candidate)
		if err != nil {
			return nil, fmt.Errorf("restore retained candidate: %w", err)
		}
		totalBytes += cached.size
		if totalBytes > maxEstimateCandidateBytes {
			return nil, fmt.Errorf("estimator checkpoint exceeds the candidate byte limit")
		}
		state.candidates = append(state.candidates, cached)
	}
	return state, nil
}

func validateEstimatorFrontier(checkpoints *partitionEstimates, inputID []byte, offset int64) error {
	frontier := checkpoints.frontier
	if frontier == nil {
		if offset < 0 && len(checkpoints.assets) == 0 {
			return nil
		}
		return fmt.Errorf("checkpoint frontier is missing for input offset %d", offset)
	}
	if !bytes.Equal(frontier.InputTopicId, inputID) {
		return fmt.Errorf("checkpoint frontier belongs to a different input topic generation")
	}
	if frontier.NextOffset != offset {
		return fmt.Errorf("checkpoint frontier offset %d differs from the consumer's committed offset %d", frontier.NextOffset, offset)
	}
	return nil
}
