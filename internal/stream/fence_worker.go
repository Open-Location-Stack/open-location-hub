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
	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/observability"
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

// FenceWorker owns active fence memberships for the assigned Kafka partitions.
type FenceWorker struct {
	ctx         context.Context
	stop        context.CancelCauseFunc
	session     *kgo.GroupTransactSession
	store       *checkpointStore
	inputTopic  string
	outputTopic string
	machine     fenceMachine
	mu          sync.Mutex
	partitions  map[int32]*partitionMemberships
	deadlines   fenceDeadlineQueue
}

// OpenFenceWorker joins the membership consumer group for a dedicated state topic.
func OpenFenceWorker(ctx context.Context, cfg config.KafkaShadowConfig, input, output, state, instance string) (*FenceWorker, error) {
	if !cfg.Enabled() || input == "" || output == "" || state == "" || input == output || output == state || state == input || instance == "" {
		return nil, fmt.Errorf("fence membership requires separate input, transition and checkpoint topics and an instance ID")
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
	if store.outputID == [16]byte{} {
		store.client.Close()
		return nil, fmt.Errorf("broker did not provide the fence transition topic ID")
	}
	ctx, stop := context.WithCancelCause(ctx)
	worker := &FenceWorker{
		ctx: ctx, stop: stop, store: store, inputTopic: input, outputTopic: output,
		machine: fenceMachine{geometry: make(map[string]gen.Fence)}, partitions: make(map[int32]*partitionMemberships),
		deadlines: fenceDeadlineQueue{byKey: make(map[string]*fenceDeadline)},
	}
	group := state + ".membership"
	worker.session, err = kgo.NewGroupTransactSession(append(options,
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
	return worker, nil
}

// Close leaves the group and closes the checkpoint and deadline producer.
func (w *FenceWorker) Close() {
	w.stop(nil)
	w.session.CloseAllowingRebalance()
	w.store.client.Close()
}

// Run processes evaluations and schedules due deadlines until canceled.
func (w *FenceWorker) Run() error {
	ctx := w.ctx
	for ctx.Err() == nil {
		pollDeadline, err := w.publishDue(ctx, time.Now())
		if err != nil && ctx.Err() == nil {
			return fmt.Errorf("append fence deadline: %w", err)
		}
		pollCtx, cancel := context.WithDeadline(ctx, pollDeadline)
		fetches := w.session.PollRecords(pollCtx, 256)
		cancel()
		if ctx.Err() != nil {
			break
		}
		if err := fetches.Err(); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("fetch fence membership input: %w", err)
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

func (w *FenceWorker) restorePartitions(ctx context.Context, offsets map[string]map[int32]kgo.Offset) (map[string]map[int32]kgo.Offset, error) {
	if len(offsets[w.inputTopic]) == 0 {
		return offsets, nil
	}
	started := time.Now()
	partitions, err := w.store.restoreFences(ctx, offsets[w.inputTopic])
	observability.RuntimeFromContext(w.ctx).RecordProcessingDuration(w.ctx, "fence_membership_restore", "checkpoint", time.Since(started))
	if err != nil {
		if ctx.Err() == nil && w.ctx.Err() == nil {
			w.stop(fmt.Errorf("restore fence partitions: %w", err))
		}
		return nil, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for partition, materialized := range partitions {
		w.partitions[partition] = materialized
		for key, state := range materialized.members {
			w.deadlines.update(partition, key, state)
		}
	}
	return offsets, nil
}

func (w *FenceWorker) dropPartitions(_ context.Context, _ *kgo.Client, revoked map[string][]int32) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, partition := range revoked[w.inputTopic] {
		if state := w.partitions[partition]; state != nil {
			for key := range state.members {
				w.deadlines.update(partition, key, nil)
			}
			delete(w.partitions, partition)
		}
	}
}

func (w *FenceWorker) publishDue(ctx context.Context, now time.Time) (time.Time, error) {
	// Periodically wake even without a timer so a concurrent assignment can
	// make restored deadlines visible without any incoming evaluations.
	wake := now.Add(100 * time.Millisecond)
	w.mu.Lock()
	var records []*kgo.Record
	for len(records) < 256 {
		at := w.deadlines.next()
		if at.IsZero() {
			break
		}
		if at.After(now) {
			if at.Before(wake) {
				wake = at
			}
			break
		}
		timer := w.deadlines.pop()
		last := timer.membership.observation
		eventID := fenceDeadlineID(last.EventId, at)
		value, err := proto.Marshal(&streampb.FenceEvaluation{
			SchemaVersion: 1, EventId: eventID[:], OriginHubId: last.OriginHubId,
			FenceId: last.FenceId, TrackableId: last.TrackableId, TopologyVersion: fenceTopologyVersion,
			ProcessedAtUnixNano: at.UnixNano(), Action: streampb.FenceEvaluation_ACTION_DEADLINE, DeadlineGeneration: last.EventId,
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
	// A former owner may append a hint during reassignment. The committed
	// membership generation fences stale and duplicate hints.
	produceCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	started := time.Now()
	err := w.store.client.ProduceSync(produceCtx, records...).FirstErr()
	telemetry := observability.RuntimeFromContext(ctx)
	telemetry.RecordProcessingDuration(ctx, "fence_membership_deadline_append", "timer", time.Since(started))
	return wake, err
}

type fenceOutcome struct{ trigger, outcome string }

func (w *FenceWorker) process(ctx context.Context, fetches kgo.Fetches) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	batch := make(map[int32]*partitionMemberships)
	var results []*kgo.Record
	outcomes := make([]fenceOutcome, 0, fetches.NumRecords())
	telemetry := observability.RuntimeFromContext(ctx)
	for record := range fetches.RecordsAll() {
		previous := w.partitions[record.Partition]
		if previous == nil {
			return fmt.Errorf("fence state was not restored for partition %d", record.Partition)
		}
		updates := batch[record.Partition]
		if updates == nil {
			updates = &partitionMemberships{members: make(map[string]*fenceMembership), frontier: previous.frontier}
			batch[record.Partition] = updates
		}
		if updates.frontier != nil && record.Offset < updates.frontier.NextOffset {
			return fmt.Errorf("fence input partition %d offset %d precedes checkpoint %d", record.Partition, record.Offset, updates.frontier.NextOffset)
		}
		var input streampb.FenceEvaluation
		if err := proto.Unmarshal(record.Value, &input); err != nil {
			return fmt.Errorf("decode fence input: %w", err)
		}
		if err := validateFenceInput(record, &input, w.store.partitions); err != nil {
			return fmt.Errorf("fence partition %d offset %d: %w", record.Partition, record.Offset, err)
		}
		key := string(record.Key)
		membership, changed := updates.members[key]
		if !changed {
			membership = previous.members[key]
		}
		trigger := "observation"
		started := time.Now()
		telemetry.RecordQueueWait(ctx, "fence_membership", max(0, started.Sub(record.Timestamp)))
		if input.Action == streampb.FenceEvaluation_ACTION_DEADLINE {
			trigger = "timer"
			telemetry.RecordQueueWait(ctx, "fence_membership_timer", max(0, started.Sub(time.Unix(0, input.ProcessedAtUnixNano))))
		}
		next, transitions, outcome, err := w.machine.evaluate(&input, membership)
		duration := time.Since(started)
		telemetry.RecordMachineEvaluation(ctx, "fence_membership", trigger, "shadow", outcome, duration, err)
		telemetry.RecordStreamServiceTime(ctx, "fence_membership", trigger, "fence_membership", "shadow", duration)
		if err != nil {
			return fmt.Errorf("evaluate fence partition %d offset %d: %w", record.Partition, record.Offset, err)
		}
		if next != membership {
			updates.members[key] = next
		}
		for _, transition := range transitions {
			value, err := proto.Marshal(transition)
			if err != nil {
				return fmt.Errorf("encode fence transition: %w", err)
			}
			results = append(results, &kgo.Record{Topic: w.outputTopic, Partition: record.Partition, Key: record.Key, Value: value})
		}
		updates.frontier = &streampb.FenceMembershipFrontier{
			InputTopicId: w.store.inputID[:], NextOffset: record.Offset + 1,
			TransitionTopicId: w.store.outputID[:], TopologyVersion: fenceTopologyVersion,
		}
		outcomes = append(outcomes, fenceOutcome{trigger: trigger, outcome: outcome})
	}
	checkpoints, err := w.store.fenceRecords(batch)
	if err != nil {
		return fmt.Errorf("encode fence checkpoints: %w", err)
	}
	started := time.Now()
	committed, err := commitStreamBatch(ctx, w.session, append(results, checkpoints...))
	telemetry.RecordProcessingDuration(ctx, "fence_membership_commit", "checkpoint", time.Since(started))
	if err != nil {
		return fmt.Errorf("commit fence membership batch: %w", err)
	}
	if committed {
		for partition, updates := range batch {
			current := w.partitions[partition]
			for key, state := range updates.members {
				if state == nil {
					delete(current.members, key)
				} else {
					current.members[key] = state
				}
				w.deadlines.update(partition, key, state)
			}
			current.frontier = updates.frontier
		}
	}
	for _, result := range outcomes {
		if !committed {
			result.outcome = "rebalanced"
		}
		telemetry.RecordStreamHandled(ctx, "fence_membership", result.trigger, "fence_membership", "shadow", result.outcome)
	}
	return nil
}

func validateFenceInput(record *kgo.Record, input *streampb.FenceEvaluation, partitions int32) error {
	if input.SchemaVersion != 1 || input.TopologyVersion != fenceTopologyVersion || len(input.EventId) != 32 ||
		input.OriginHubId == "" || input.FenceId == "" || input.TrackableId == "" || input.ProcessedAtUnixNano <= 0 {
		return fmt.Errorf("invalid fence input identity")
	}
	key := fenceMembershipKey(input.OriginHubId, input.FenceId, input.TrackableId)
	if !bytes.Equal(key, record.Key) || record.Partition != int32(crc32.ChecksumIEEE(key)%uint32(partitions)) {
		return fmt.Errorf("fence key does not match its membership partition")
	}
	switch input.Action {
	case streampb.FenceEvaluation_ACTION_DEADLINE:
		id := fenceDeadlineID(input.DeadlineGeneration, time.Unix(0, input.ProcessedAtUnixNano))
		if !bytes.Equal(input.EventId, id[:]) || len(input.DeadlineGeneration) != 32 {
			return fmt.Errorf("invalid fence deadline generation")
		}
	case streampb.FenceEvaluation_ACTION_EVALUATE, streampb.FenceEvaluation_ACTION_CANCEL:
		if input.AssetSequence == 0 || input.ConfigurationRevision < 0 || len(input.EstimateId) != 32 || len(input.DeadlineGeneration) != 0 {
			return fmt.Errorf("invalid fence observation")
		}
	default:
		return fmt.Errorf("unknown fence input action %v", input.Action)
	}
	return nil
}
