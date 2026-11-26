package stream

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"time"

	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"google.golang.org/protobuf/proto"
)

var frontierKey = []byte{0}

type estimateStore struct {
	client     *kgo.Client
	options    []kgo.Opt
	topic      string
	inputID    [16]byte
	partitions int32
}

func openEstimateStore(ctx context.Context, options []kgo.Opt, input, output, topic string) (*estimateStore, error) {
	client, err := kgo.NewClient(options...)
	if err != nil {
		return nil, err
	}
	store, err := describeEstimateStore(ctx, client, options, input, output, topic)
	if err != nil {
		client.Close()
		return nil, err
	}
	return store, nil
}

func describeEstimateStore(ctx context.Context, client *kgo.Client, options []kgo.Opt, input, output, topic string) (*estimateStore, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	inputMetadata, err := topicMetadata(ctx, client, input)
	if err != nil {
		return nil, err
	}
	if inputMetadata.TopicID == [16]byte{} {
		return nil, fmt.Errorf("broker did not provide the estimator input topic ID")
	}
	for _, name := range []string{topic, output} {
		metadata, err := topicMetadata(ctx, client, name)
		if err != nil {
			return nil, err
		}
		if len(metadata.Partitions) != len(inputMetadata.Partitions) {
			return nil, fmt.Errorf("topic %q has %d partitions, input has %d", name, len(metadata.Partitions), len(inputMetadata.Partitions))
		}
	}
	req := kmsg.NewPtrDescribeConfigsRequest()
	req.Resources = []kmsg.DescribeConfigsRequestResource{{
		ResourceType: kmsg.ConfigResourceTypeTopic, ResourceName: topic,
		ConfigNames: []string{"cleanup.policy"},
	}}
	configs, err := req.RequestWith(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("describe estimator checkpoint topic: %w", err)
	}
	if len(configs.Resources) != 1 || len(configs.Resources[0].Configs) != 1 {
		return nil, fmt.Errorf("checkpoint topic %q did not return its cleanup policy", topic)
	}
	if err := kerr.ErrorForCode(configs.Resources[0].ErrorCode); err != nil {
		return nil, fmt.Errorf("describe checkpoint topic %q: %w", topic, err)
	}
	policy := configs.Resources[0].Configs[0].Value
	if policy == nil || *policy != "compact" {
		return nil, fmt.Errorf("checkpoint topic %q requires cleanup.policy=compact", topic)
	}
	return &estimateStore{
		client: client, options: options, topic: topic, inputID: inputMetadata.TopicID,
		partitions: int32(len(inputMetadata.Partitions)),
	}, nil
}

func (s *estimateStore) records(batch map[int32]*partitionEstimates) ([]*kgo.Record, error) {
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

func (s *estimateStore) restore(ctx context.Context, offsets map[int32]kgo.Offset) (map[int32]*partitionEstimates, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// A unique key prevents compaction from erasing this read barrier before
	// the scan observes it. Reading committed data through the barrier also
	// waits for any earlier open transaction on that partition.
	barrier := make([]byte, 17)
	rand.Read(barrier[1:])
	checkpoints := make(map[int32]*partitionEstimates, len(offsets))
	assigned := make(map[int32]kgo.Offset, len(offsets))
	barriers := make([]*kgo.Record, 0, len(offsets))
	for partition := range offsets {
		if partition < 0 || partition >= s.partitions {
			return nil, fmt.Errorf("partition %d exceeds the configured estimator topology", partition)
		}
		assigned[partition] = kgo.NewOffset().AtStart()
		checkpoints[partition] = &partitionEstimates{assets: make(map[string]*assetEstimateState)}
		barriers = append(barriers, &kgo.Record{Topic: s.topic, Partition: partition, Key: barrier, Value: []byte{1}})
	}
	if err := s.client.ProduceSync(ctx, barriers...).FirstErr(); err != nil {
		return nil, fmt.Errorf("write checkpoint restore barriers: %w", err)
	}
	options := append([]kgo.Opt(nil), s.options...)
	reader, err := kgo.NewClient(append(options,
		kgo.FetchIsolationLevel(kgo.ReadCommitted()),
		kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{s.topic: assigned}),
	)...)
	if err != nil {
		return nil, fmt.Errorf("open checkpoint reader: %w", err)
	}
	defer reader.Close()
	done := make(map[int32]bool, len(offsets))
	for len(done) < len(offsets) {
		fetches := reader.PollRecords(ctx, 2048)
		if err := fetches.Err(); err != nil {
			return nil, fmt.Errorf("read committed estimator state: %w", err)
		}
		for record := range fetches.RecordsAll() {
			if done[record.Partition] {
				continue
			}
			if bytes.Equal(record.Key, barrier) {
				done[record.Partition] = true
				reader.PauseFetchPartitions(map[string][]int32{s.topic: {record.Partition}})
				continue
			}
			if err := applyEstimateCheckpoint(checkpoints[record.Partition], record); err != nil {
				return nil, fmt.Errorf("restore estimator state partition %d offset %d: %w", record.Partition, record.Offset, err)
			}
		}
	}
	for partition, offset := range offsets {
		if err := validateEstimatorFrontier(checkpoints[partition], s.inputID[:], offset.EpochOffset().Offset); err != nil {
			return nil, fmt.Errorf("partition %d: %w", partition, err)
		}
		s.client.TryProduce(context.WithoutCancel(ctx), &kgo.Record{Topic: s.topic, Partition: partition, Key: barrier}, nil)
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
