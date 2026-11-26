package stream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"sync"
	"time"

	"github.com/formation-res/open-location-hub/internal/config"
	"github.com/formation-res/open-location-hub/internal/observability"
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

// CollisionPairWorker owns exact contact state for its assigned pairs.
type CollisionPairWorker struct {
	ctx         context.Context
	stop        context.CancelCauseFunc
	session     *kgo.GroupTransactSession
	store       *checkpointStore
	inputTopic  string
	outputTopic string
	mu          sync.Mutex
	partitions  map[int32]*collisionPairPartition
	deadlines   deadlineQueue[*collisionPair]
}

// OpenCollisionPairWorker joins the pair consumer group for a dedicated checkpoint topic.
func OpenCollisionPairWorker(ctx context.Context, cfg config.KafkaShadowConfig, input, output, state, instance string) (*CollisionPairWorker, error) {
	if !cfg.Enabled() || input == "" || output == "" || state == "" || input == output || output == state || state == input || instance == "" {
		return nil, fmt.Errorf("collision pairs require separate input, transition and checkpoint topics and an instance ID")
	}
	options, err := kafkaClientOptions(cfg)
	if err != nil {
		return nil, err
	}
	options = append(options, kgo.RecordPartitioner(kgo.ManualPartitioner()))
	store, err := openCheckpointStore(ctx, options, input, output, state)
	if err != nil {
		return nil, err
	}
	checkCtx, finish := context.WithTimeout(ctx, 5*time.Second)
	timestampMode, err := topicSetting(checkCtx, store.client, input, "message.timestamp.type")
	finish()
	if err != nil {
		store.client.Close()
		return nil, err
	}
	if timestampMode != "LogAppendTime" || store.outputID == [16]byte{} {
		store.client.Close()
		return nil, fmt.Errorf("collision pairs require broker append time on input and a transition topic ID")
	}
	ctx, stop := context.WithCancelCause(ctx)
	w := &CollisionPairWorker{
		ctx: ctx, stop: stop, store: store, inputTopic: input, outputTopic: output,
		partitions: make(map[int32]*collisionPairPartition),
		deadlines:  deadlineQueue[*collisionPair]{byKey: make(map[string]*deadlineItem[*collisionPair])},
	}
	group := state + ".pairs"
	w.session, err = kgo.NewGroupTransactSession(append(options,
		kgo.ConsumeTopics(input),
		kgo.ConsumerGroup(group),
		kgo.ConsumeStartOffset(kgo.NewOffset().AtStart()),
		kgo.ConsumeResetOffset(kgo.NoResetOffset()),
		kgo.FetchIsolationLevel(kgo.ReadCommitted()),
		kgo.TransactionalID(group+"."+instance),
		kgo.TransactionTimeout(20*time.Second),
		kgo.BlockRebalanceOnPoll(),
		kgo.Balancers(kgo.RangeBalancer()),
		kgo.OnPartitionsRevoked(w.dropPartitions),
		kgo.OnPartitionsLost(w.dropPartitions),
		kgo.AdjustFetchOffsetsFn(w.restorePartitions),
	)...)
	if err != nil {
		stop(err)
		store.client.Close()
		return nil, err
	}
	return w, nil
}

// Close releases group ownership and the checkpoint connection.
func (w *CollisionPairWorker) Close() {
	w.stop(nil)
	w.session.CloseAllowingRebalance()
	w.store.client.Close()
}

// Run evaluates contacts and durable deadline hints until canceled.
func (w *CollisionPairWorker) Run() error {
	ctx := w.ctx
	for ctx.Err() == nil {
		wake, err := w.appendDeadlines(ctx, time.Now())
		if err != nil && ctx.Err() == nil {
			return fmt.Errorf("append collision pair deadlines: %w", err)
		}
		pollCtx, cancel := context.WithDeadline(ctx, wake)
		fetches := w.session.PollRecords(pollCtx, 256)
		cancel()
		if ctx.Err() != nil {
			break
		}
		if err := fetches.Err(); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("fetch collision pair input: %w", err)
		}
		if fetches.NumRecords() > 0 {
			if err := w.process(ctx, fetches); err != nil && ctx.Err() == nil {
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

func (w *CollisionPairWorker) restorePartitions(ctx context.Context, offsets map[string]map[int32]kgo.Offset) (map[string]map[int32]kgo.Offset, error) {
	if len(offsets[w.inputTopic]) == 0 {
		return offsets, nil
	}
	start := time.Now()
	partitions, err := w.store.restoreCollisionPairs(ctx, offsets[w.inputTopic])
	observability.RuntimeFromContext(w.ctx).RecordProcessingDuration(w.ctx, "collision_pair_restore", "checkpoint", time.Since(start))
	if err != nil {
		if ctx.Err() == nil && w.ctx.Err() == nil {
			w.stop(fmt.Errorf("restore collision pair partitions: %w", err))
		}
		return nil, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for partition, state := range partitions {
		w.partitions[partition] = state
		for key, pair := range state.pairs {
			w.updateDeadline(partition, key, pair)
		}
	}
	return offsets, nil
}

func (w *CollisionPairWorker) dropPartitions(_ context.Context, _ *kgo.Client, revoked map[string][]int32) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, partition := range revoked[w.inputTopic] {
		if state := w.partitions[partition]; state != nil {
			for key := range state.pairs {
				w.updateDeadline(partition, key, nil)
			}
			delete(w.partitions, partition)
		}
	}
}

func (w *CollisionPairWorker) updateDeadline(partition int32, key string, pair *collisionPair) {
	var at time.Time
	if pair != nil {
		at = pair.nextDeadline()
	}
	w.deadlines.update(partition, key, at, pair)
}

func (w *CollisionPairWorker) appendDeadlines(ctx context.Context, now time.Time) (time.Time, error) {
	wake := now.Add(100 * time.Millisecond)
	var records []*kgo.Record
	w.mu.Lock()
	for len(records) < 256 {
		at := w.deadlines.next()
		if at.IsZero() || at.After(now) {
			if !at.IsZero() && at.Before(wake) {
				wake = at
			}
			break
		}
		timer := w.deadlines.pop()
		pair := timer.value
		generation := pair.generation()
		id := collisionPairDeadlineID(generation, at)
		value, err := proto.Marshal(&streampb.CollisionPairInput{
			SchemaVersion: 1, TopologyVersion: collisionTopologyVersion, EventId: id[:],
			Left: pair.left.sample, Right: pair.right.sample, DeadlineGeneration: generation, DeadlineAtUnixNano: at.UnixNano(),
		})
		if err != nil {
			w.mu.Unlock()
			return wake, err
		}
		records = append(records, &kgo.Record{Topic: w.inputTopic, Partition: timer.partition, Key: []byte(timer.key), Value: value})
	}
	w.mu.Unlock()
	if len(records) == 0 {
		return wake, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	start := time.Now()
	err := w.store.client.ProduceSync(ctx, records...).FirstErr()
	observability.RuntimeFromContext(ctx).RecordProcessingDuration(ctx, "collision_pair_deadline_append", "timer", time.Since(start))
	return wake, err
}

type collisionPairOutcome struct{ trigger, outcome string }

func (w *CollisionPairWorker) process(ctx context.Context, fetches kgo.Fetches) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	batch := make(map[int32]*collisionPairPartition)
	var outputs []*kgo.Record
	outcomes := make([]collisionPairOutcome, 0, fetches.NumRecords())
	telemetry := observability.RuntimeFromContext(ctx)
	for record := range fetches.RecordsAll() {
		previous := w.partitions[record.Partition]
		if previous == nil {
			return fmt.Errorf("collision pair state was not restored for partition %d", record.Partition)
		}
		updates := batch[record.Partition]
		if updates == nil {
			updates = &collisionPairPartition{pairs: make(map[string]*collisionPair), frontier: previous.frontier}
			batch[record.Partition] = updates
		}
		if updates.frontier != nil && record.Offset < updates.frontier.NextOffset {
			return fmt.Errorf("collision pair partition %d offset %d precedes checkpoint %d", record.Partition, record.Offset, updates.frontier.NextOffset)
		}
		input := new(streampb.CollisionPairInput)
		if err := proto.Unmarshal(record.Value, input); err != nil {
			return fmt.Errorf("decode collision pair input: %w", err)
		}
		if err := validateCollisionPairInput(record, input, w.store.partitions); err != nil {
			return fmt.Errorf("validate collision pair partition %d offset %d: %w", record.Partition, record.Offset, err)
		}
		key := string(record.Key)
		pair, changed := updates.pairs[key]
		if !changed {
			pair = previous.pairs[key]
		}
		start := time.Now()
		telemetry.RecordQueueWait(ctx, "collision_pair", max(0, start.Sub(record.Timestamp)))
		trigger := "observation"
		if input.DeadlineAtUnixNano != 0 {
			trigger = "timer"
			telemetry.RecordQueueWait(ctx, "collision_pair_timer", max(0, start.Sub(timestampOrZero(input.DeadlineAtUnixNano))))
		}
		next, transitions, outcome, err := evaluateCollisionPair(input, pair, record.Timestamp)
		duration := time.Since(start)
		telemetry.RecordMachineEvaluation(ctx, "collision_pair", trigger, "shadow", outcome, duration, err)
		telemetry.RecordStreamServiceTime(ctx, "collision_pair", trigger, "collision_pair", "shadow", duration)
		if err != nil {
			return fmt.Errorf("evaluate collision pair partition %d offset %d: %w", record.Partition, record.Offset, err)
		}
		if next != pair {
			updates.pairs[key] = next
		}
		for _, transition := range transitions {
			value, err := proto.Marshal(transition)
			if err != nil {
				return fmt.Errorf("encode collision transition: %w", err)
			}
			outputs = append(outputs, &kgo.Record{Topic: w.outputTopic, Partition: record.Partition, Key: record.Key, Value: value})
		}
		updates.frontier = &streampb.CollisionPairFrontier{
			InputTopicId: w.store.inputID[:], NextOffset: record.Offset + 1,
			TransitionTopicId: w.store.outputID[:], TopologyVersion: collisionTopologyVersion,
		}
		outcomes = append(outcomes, collisionPairOutcome{trigger: trigger, outcome: outcome})
	}
	checkpoints, err := w.store.collisionPairRecords(batch)
	if err != nil {
		return fmt.Errorf("encode collision pair checkpoints: %w", err)
	}
	start := time.Now()
	committed, err := commitStreamBatch(ctx, w.session, append(outputs, checkpoints...))
	telemetry.RecordProcessingDuration(ctx, "collision_pair_commit", "checkpoint", time.Since(start))
	if err != nil {
		return fmt.Errorf("commit collision pair batch: %w", err)
	}
	if committed {
		for partition, updates := range batch {
			state := w.partitions[partition]
			for key, pair := range updates.pairs {
				if pair == nil {
					delete(state.pairs, key)
				} else {
					state.pairs[key] = pair
				}
				w.updateDeadline(partition, key, pair)
			}
			state.frontier = updates.frontier
		}
	}
	for _, result := range outcomes {
		if !committed {
			result.outcome = "rebalanced"
		}
		telemetry.RecordStreamHandled(ctx, "collision_pair", result.trigger, "collision_pair", "shadow", result.outcome)
	}
	if committed {
		for range outputs {
			telemetry.RecordStreamHandled(ctx, "collision_pair_transition", "publish", "collision_pair", "shadow", "committed")
		}
	}
	return nil
}

func validateCollisionPairInput(record *kgo.Record, input *streampb.CollisionPairInput, partitions int32) error {
	if input.SchemaVersion != 1 || input.TopologyVersion != collisionTopologyVersion || record.Timestamp.UnixNano() <= 0 {
		return fmt.Errorf("invalid collision pair schema or append clock")
	}
	if err := validatePairSamples(input.Left, input.Right); err != nil {
		return err
	}
	key := collisionPairKey(input.Left, input.Right)
	if !bytes.Equal(record.Key, key) || record.Partition != int32(crc32.ChecksumIEEE(key)%uint32(partitions)) {
		return fmt.Errorf("collision pair key does not match partition")
	}
	generation := collisionPairVersionID(input.Left, input.Right)
	if input.DeadlineAtUnixNano != 0 {
		at := timestampOrZero(input.DeadlineAtUnixNano)
		id := collisionPairDeadlineID(generation, at)
		if input.DeadlineAtUnixNano < 0 || !bytes.Equal(input.DeadlineGeneration, generation) ||
			!bytes.Equal(input.EventId, id[:]) || len(input.TriggerCellInputId) != 0 {
			return fmt.Errorf("invalid collision pair deadline")
		}
		return nil
	}
	if !bytes.Equal(input.EventId, generation) || len(input.DeadlineGeneration) != 0 || len(input.TriggerCellInputId) != 32 ||
		input.EvaluatedAtUnixNano != max(input.Left.ProcessedAtUnixNano, input.Right.ProcessedAtUnixNano) ||
		(input.UpdatedTrackableId != "" && input.UpdatedTrackableId != input.Left.TrackableId && input.UpdatedTrackableId != input.Right.TrackableId) {
		return fmt.Errorf("invalid collision pair candidate")
	}
	return nil
}
