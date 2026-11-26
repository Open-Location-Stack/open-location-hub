package stream

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/formation-res/open-location-hub/internal/config"
	"github.com/formation-res/open-location-hub/internal/observability"
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"github.com/formation-res/open-location-hub/internal/transform"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

// EstimateWorker consumes asset-keyed inputs and transactionally commits motion
// estimates, materialized filter state and input offsets.
type EstimateWorker struct {
	ctx           context.Context
	stop          context.CancelCauseFunc
	session       *kgo.GroupTransactSession
	store         *estimateStore
	model         motionEstimator
	inputTopic    string
	outputTopic   string
	partitions    map[int32]*partitionEstimates
	configuration *ConfigurationWatch
	retention     candidateRetention
	fences        *fenceRouter
}

// EstimatorConfiguration enables locating rules and defines candidate lifetimes.
type EstimatorConfiguration struct {
	Watch           *ConfigurationWatch
	LocationTTL     time.Duration
	ProximityTTL    time.Duration
	FenceInputTopic string
}

type partitionEstimates struct {
	assets   map[string]*assetEstimateState
	frontier *streampb.EstimatorFrontier
}

// OpenEstimateWorker joins the consumer group for one dedicated checkpoint topic.
func OpenEstimateWorker(ctx context.Context, cfg config.KafkaShadowConfig, input, output, state, instance string, configuration *EstimatorConfiguration) (*EstimateWorker, error) {
	if !cfg.Enabled() || input == "" || output == "" || state == "" || input == output || output == state || state == input || instance == "" {
		return nil, fmt.Errorf("estimation requires separate input, output and checkpoint topics and an instance ID")
	}
	options, err := kafkaClientOptions(cfg)
	if err != nil {
		return nil, err
	}
	options = append(options, kgo.RecordPartitioner(kgo.ManualPartitioner()))
	store, err := openEstimateStore(ctx, options, input, output, state)
	if err != nil {
		return nil, err
	}
	workerCtx, stop := context.WithCancelCause(ctx)
	worker := &EstimateWorker{
		ctx: workerCtx, stop: stop,
		store: store, model: motionEstimator{projection: transform.NewCRSTransformer()},
		inputTopic: input, outputTopic: output, partitions: make(map[int32]*partitionEstimates),
	}
	if configuration != nil {
		if configuration.Watch == nil || configuration.LocationTTL <= 0 || configuration.ProximityTTL <= 0 {
			stop(nil)
			store.client.Close()
			return nil, fmt.Errorf("estimator requires a configuration watch and positive candidate lifetimes")
		}
		worker.configuration = configuration.Watch
		worker.retention = candidateRetention{location: configuration.LocationTTL, proximity: configuration.ProximityTTL}
		if topic := configuration.FenceInputTopic; topic != "" {
			if topic == cfg.Topic || topic == input || topic == output || topic == state || topic == configuration.Watch.topic ||
				configuration.Watch.cache.Load().geometry == nil {
				stop(nil)
				store.client.Close()
				return nil, fmt.Errorf("fence routing requires a separate input topic and geometry snapshot")
			}
			checkCtx, finish := context.WithTimeout(ctx, 5*time.Second)
			metadata, err := topicMetadata(checkCtx, store.client, topic)
			finish()
			if err != nil || metadata.TopicID == [16]byte{} || len(metadata.Partitions) == 0 {
				stop(nil)
				store.client.Close()
				return nil, fmt.Errorf("fence input topic topology is unavailable: %v", err)
			}
			worker.fences = &fenceRouter{topic: topic, topicID: metadata.TopicID, partitions: int32(len(metadata.Partitions))}
		}
	}
	group := state + ".estimator"
	session, err := kgo.NewGroupTransactSession(append(options,
		kgo.ConsumeTopics(input),
		kgo.ConsumerGroup(group),
		kgo.ConsumeStartOffset(kgo.NewOffset().AtStart()),
		kgo.ConsumeResetOffset(kgo.NoResetOffset()),
		kgo.FetchIsolationLevel(kgo.ReadCommitted()),
		kgo.TransactionalID(group+"."+instance),
		kgo.TransactionTimeout(20*time.Second),
		kgo.BlockRebalanceOnPoll(),
		kgo.Balancers(kgo.RangeBalancer()),
		kgo.OnPartitionsRevoked(worker.dropPartitions),
		kgo.OnPartitionsLost(worker.dropPartitions),
		kgo.AdjustFetchOffsetsFn(worker.restorePartitions),
	)...)
	if err != nil {
		stop(err)
		store.client.Close()
		return nil, err
	}
	worker.session = session
	return worker, nil
}

// Close leaves the consumer group and releases the checkpoint connection.
func (w *EstimateWorker) Close() {
	w.stop(nil)
	w.session.CloseAllowingRebalance()
	w.store.client.Close()
}

// Run estimates motion until the context is canceled or processing cannot safely continue.
func (w *EstimateWorker) Run() error {
	ctx := w.ctx
	for ctx.Err() == nil {
		fetches := w.session.PollRecords(ctx, 256)
		if ctx.Err() != nil {
			break
		}
		if err := fetches.Err(); err != nil {
			return fmt.Errorf("fetch estimator input: %w", err)
		}
		if fetches.NumRecords() > 0 {
			if err := w.process(ctx, fetches); err != nil {
				if ctx.Err() != nil {
					break
				}
				return err
			}
		}
		w.session.AllowRebalance()
	}
	cause := context.Cause(ctx)
	if errors.Is(cause, context.Canceled) {
		return nil
	}
	return cause
}

func (w *EstimateWorker) restorePartitions(ctx context.Context, offsets map[string]map[int32]kgo.Offset) (map[string]map[int32]kgo.Offset, error) {
	if len(offsets[w.inputTopic]) == 0 {
		return offsets, nil
	}
	started := time.Now()
	partitions, err := w.store.restore(ctx, offsets[w.inputTopic])
	observability.RuntimeFromContext(w.ctx).RecordProcessingDuration(w.ctx, "asset_estimator_restore", "checkpoint", time.Since(started))
	if err != nil {
		if ctx.Err() == nil && w.ctx.Err() == nil {
			w.stop(fmt.Errorf("restore estimator partitions: %w", err))
		}
		return nil, err
	}
	for partition, materialized := range partitions {
		if materialized.frontier != nil && materialized.frontier.ConfigurationAware != (w.configuration != nil) {
			err := fmt.Errorf("partition %d checkpoint belongs to another estimator configuration mode", partition)
			w.stop(err)
			return nil, err
		}
		if frontier := materialized.frontier; frontier != nil {
			if frontier.FenceRouting != (w.fences != nil) || w.fences != nil &&
				(!bytes.Equal(frontier.FenceInputTopicId, w.fences.topicID[:]) || frontier.FenceInputPartitions != w.fences.partitions) {
				err := fmt.Errorf("partition %d checkpoint belongs to another fence routing topology", partition)
				w.stop(err)
				return nil, err
			}
		}
		if w.configuration != nil && materialized.frontier != nil &&
			materialized.frontier.ConfigurationRevision > w.configuration.cache.Load().revision {
			err := fmt.Errorf("partition %d checkpoint requires a newer configuration revision", partition)
			w.stop(err)
			return nil, err
		}
		w.partitions[partition] = materialized
	}
	return offsets, nil
}

func (w *EstimateWorker) dropPartitions(_ context.Context, _ *kgo.Client, revoked map[string][]int32) {
	for _, partition := range revoked[w.inputTopic] {
		delete(w.partitions, partition)
	}
}

func (w *EstimateWorker) process(ctx context.Context, fetches kgo.Fetches) error {
	telemetry := observability.RuntimeFromContext(ctx)
	batch, results, outcomes, err := w.prepare(ctx, fetches)
	if err != nil {
		return err
	}
	checkpoints, err := w.store.records(batch)
	if err != nil {
		return err
	}
	started := time.Now()
	committed, err := commitStreamBatch(ctx, w.session, append(results, checkpoints...))
	telemetry.RecordProcessingDuration(ctx, "asset_estimator_commit", "motion", time.Since(started))
	if err != nil {
		return fmt.Errorf("commit estimator batch: %w", err)
	}
	if committed {
		for partition, updates := range batch {
			current := w.partitions[partition]
			for key, checkpoint := range updates.assets {
				current.assets[key] = checkpoint
			}
			current.frontier = updates.frontier
		}
		if w.fences != nil {
			for _, record := range results {
				if record.Topic == w.fences.topic {
					telemetry.RecordStreamHandled(ctx, "spatial_router", "candidate", "fence_membership", "shadow", "committed")
				}
			}
		}
	}
	for _, outcome := range outcomes {
		if !committed {
			outcome = "rebalanced"
		}
		telemetry.RecordStreamHandled(ctx, "asset_estimator", "estimate", "motion", "shadow", outcome)
	}
	return nil
}

func (w *EstimateWorker) prepare(ctx context.Context, fetches kgo.Fetches) (map[int32]*partitionEstimates, []*kgo.Record, []string, error) {
	var configuration *locatingConfiguration
	if w.configuration != nil {
		configuration = w.configuration.cache.Load()
	}
	telemetry := observability.RuntimeFromContext(ctx)
	batch := make(map[int32]*partitionEstimates)
	results := make([]*kgo.Record, 0, fetches.NumRecords())
	outcomes := make([]string, 0, fetches.NumRecords())
	for record := range fetches.RecordsAll() {
		previous := w.partitions[record.Partition]
		if previous == nil {
			return nil, nil, nil, fmt.Errorf("state was not restored for partition %d", record.Partition)
		}
		updates := batch[record.Partition]
		if updates == nil {
			updates = &partitionEstimates{assets: make(map[string]*assetEstimateState), frontier: previous.frontier}
			batch[record.Partition] = updates
		}
		if updates.frontier != nil && record.Offset < updates.frontier.NextOffset {
			return nil, nil, nil, fmt.Errorf("partition %d offset %d precedes estimator frontier %d", record.Partition, record.Offset, updates.frontier.NextOffset)
		}
		start := time.Now()
		telemetry.RecordQueueWait(ctx, "asset_estimator", max(0, start.Sub(record.Timestamp)))
		outputs, outcome, err := w.estimateRecord(ctx, record, previous, updates, configuration)
		telemetry.RecordStreamServiceTime(ctx, "asset_estimator", "estimate", "motion", "shadow", time.Since(start))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("estimate partition %d offset %d: %w", record.Partition, record.Offset, err)
		}
		results = append(results, outputs...)
		outcomes = append(outcomes, outcome)
		updates.frontier = &streampb.EstimatorFrontier{
			InputTopicId: w.store.inputID[:], NextOffset: record.Offset + 1,
			ConfigurationAware: w.configuration != nil,
		}
		if configuration != nil {
			updates.frontier.ConfigurationRevision = configuration.revision
		}
		if w.fences != nil {
			updates.frontier.FenceRouting = true
			updates.frontier.FenceInputTopicId = w.fences.topicID[:]
			updates.frontier.FenceInputPartitions = w.fences.partitions
		}
	}
	return batch, results, outcomes, nil
}

func (w *EstimateWorker) estimateRecord(ctx context.Context, record *kgo.Record, previous, updates *partitionEstimates, configuration *locatingConfiguration) ([]*kgo.Record, string, error) {
	var input streampb.AssetInput
	if err := proto.Unmarshal(record.Value, &input); err != nil {
		return nil, "", fmt.Errorf("decode asset input: %w", err)
	}
	origin := input.GetObservation().GetOriginHubId()
	key := appendKeyPart(appendKeyPart(nil, origin), input.GetTrackableId())
	if input.SchemaVersion != 1 || input.GetObservation().GetSchemaVersion() != 1 || origin == "" ||
		input.GetTrackableId() == "" || len(input.EventId) != sha256.Size || !bytes.Equal(record.Key, key) {
		return nil, "", fmt.Errorf("invalid asset input version or identity")
	}
	checkpoint := updates.assets[string(key)]
	if checkpoint == nil {
		checkpoint = previous.assets[string(key)]
	}
	var result *streampb.MotionEstimate
	var next *assetEstimateState
	var outcome string
	var err error
	if configuration != nil {
		result, next, outcome, err = w.estimateConfigured(&input, checkpoint, configuration)
	} else {
		var filter *streampb.EstimateCheckpoint
		result, filter, outcome, err = w.model.estimate(&input, checkpoint.filter())
		if filter != nil {
			next = &assetEstimateState{checkpoint: filter}
		}
	}
	if err != nil {
		return nil, outcome, err
	}
	if result == nil {
		if next != nil {
			updates.assets[string(key)] = next
		}
		return nil, outcome, nil
	}
	encoded, err := proto.Marshal(result)
	if err != nil {
		return nil, "", fmt.Errorf("marshal motion estimate: %w", err)
	}
	outputs := []*kgo.Record{{Topic: w.outputTopic, Partition: record.Partition, Key: key, Value: encoded}}
	if w.fences != nil {
		started := time.Now()
		evaluations, routing, err := w.fences.route(ctx, result, checkpoint.filter().GetFenceRouting(), configuration, record.Timestamp.UnixNano())
		if err != nil {
			return nil, "", err
		}
		telemetry := observability.RuntimeFromContext(ctx)
		telemetry.RecordStreamServiceTime(ctx, "spatial_router", "route", "fence_membership", "shadow", time.Since(started))
		telemetry.RecordBatchProcessing(ctx, "spatial_router", len(evaluations), time.Since(started))
		next.checkpoint.FenceRouting = routing
		outputs = append(outputs, evaluations...)
	}
	updates.assets[string(key)] = next
	return outputs, outcome, nil
}
