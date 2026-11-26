package stream

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

var frontierKey = []byte{0}

type checkpointStore struct {
	client     *kgo.Client
	options    []kgo.Opt
	topic      string
	inputID    [16]byte
	outputID   [16]byte
	partitions int32
}

func openCheckpointStore(ctx context.Context, options []kgo.Opt, input, output, topic string) (*checkpointStore, error) {
	client, err := kgo.NewClient(options...)
	if err != nil {
		return nil, err
	}
	store, err := describeCheckpointStore(ctx, client, options, input, output, topic)
	if err != nil {
		client.Close()
		return nil, err
	}
	return store, nil
}

func describeCheckpointStore(ctx context.Context, client *kgo.Client, options []kgo.Opt, input, output, topic string) (*checkpointStore, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	inputMetadata, err := topicMetadata(ctx, client, input)
	if err != nil {
		return nil, err
	}
	if inputMetadata.TopicID == [16]byte{} {
		return nil, fmt.Errorf("broker did not provide the input topic ID")
	}
	var outputID [16]byte
	for _, name := range []string{topic, output} {
		metadata, err := topicMetadata(ctx, client, name)
		if err != nil {
			return nil, err
		}
		if name == output {
			outputID = metadata.TopicID
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
		return nil, fmt.Errorf("describe checkpoint topic: %w", err)
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
	return &checkpointStore{
		client: client, options: options, topic: topic, inputID: inputMetadata.TopicID, outputID: outputID,
		partitions: int32(len(inputMetadata.Partitions)),
	}, nil
}

func (s *checkpointStore) scan(ctx context.Context, offsets map[int32]kgo.Offset, apply func(*kgo.Record) error) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// A unique key prevents compaction from erasing this read barrier before
	// the scan observes it. Reading committed data through the barrier also
	// waits for any earlier open transaction on that partition.
	barrier := make([]byte, 17)
	rand.Read(barrier[1:])
	assigned := make(map[int32]kgo.Offset, len(offsets))
	barriers := make([]*kgo.Record, 0, len(offsets))
	for partition := range offsets {
		if partition < 0 || partition >= s.partitions {
			return fmt.Errorf("partition %d exceeds the configured checkpoint topology", partition)
		}
		assigned[partition] = kgo.NewOffset().AtStart()
		barriers = append(barriers, &kgo.Record{Topic: s.topic, Partition: partition, Key: barrier, Value: []byte{1}})
	}
	if err := s.client.ProduceSync(ctx, barriers...).FirstErr(); err != nil {
		return fmt.Errorf("write checkpoint restore barriers: %w", err)
	}
	options := append([]kgo.Opt(nil), s.options...)
	reader, err := kgo.NewClient(append(options,
		kgo.FetchIsolationLevel(kgo.ReadCommitted()),
		kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{s.topic: assigned}),
	)...)
	if err != nil {
		return fmt.Errorf("open checkpoint reader: %w", err)
	}
	defer reader.Close()
	done := make(map[int32]bool, len(offsets))
	for len(done) < len(offsets) {
		fetches := reader.PollRecords(ctx, 2048)
		if err := fetches.Err(); err != nil {
			return fmt.Errorf("read committed checkpoint: %w", err)
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
			if err := apply(record); err != nil {
				return fmt.Errorf("restore checkpoint partition %d offset %d: %w", record.Partition, record.Offset, err)
			}
		}
	}
	for partition := range offsets {
		s.client.TryProduce(context.WithoutCancel(ctx), &kgo.Record{Topic: s.topic, Partition: partition, Key: barrier}, nil)
	}
	return nil
}
