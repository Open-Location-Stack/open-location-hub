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

// CollisionCellWorker maintains indexes for the assigned collision cells.
type CollisionCellWorker struct {
	ctx         context.Context
	stop        context.CancelCauseFunc
	session     *kgo.GroupTransactSession
	store       *checkpointStore
	inputTopic  string
	outputTopic string
	mu          sync.Mutex
	partitions  map[int32]*collisionCellPartition
	deadlines   deadlineQueue[*collisionCellEntry]
}

// OpenCollisionCellWorker opens the cell group and verifies its topic topology.
func OpenCollisionCellWorker(ctx context.Context, cfg config.KafkaShadowConfig, input, output, state, instance string) (*CollisionCellWorker, error) {
	if !cfg.Enabled() || input == "" || output == "" || state == "" || input == output || output == state || state == input || instance == "" {
		return nil, fmt.Errorf("collision cells require separate input, pair and checkpoint topics and an instance ID")
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
		return nil, fmt.Errorf("broker did not provide the collision pair topic ID")
	}
	ctx, stop := context.WithCancelCause(ctx)
	worker := &CollisionCellWorker{
		ctx: ctx, stop: stop, store: store, inputTopic: input, outputTopic: output,
		partitions: make(map[int32]*collisionCellPartition),
		deadlines:  deadlineQueue[*collisionCellEntry]{byKey: make(map[string]*deadlineItem[*collisionCellEntry])},
	}
	group := state + ".cells"
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

// Close releases group ownership and the checkpoint connection.
func (w *CollisionCellWorker) Close() {
	w.stop(nil)
	w.session.CloseAllowingRebalance()
	w.store.client.Close()
}

// Run updates cell indexes and publishes pair inputs until canceled.
func (w *CollisionCellWorker) Run() error {
	ctx := w.ctx
	for ctx.Err() == nil {
		wake, err := w.appendExpirations(ctx, time.Now())
		if err != nil && ctx.Err() == nil {
			return fmt.Errorf("append collision cell expirations: %w", err)
		}
		pollCtx, cancel := context.WithDeadline(ctx, wake)
		fetches := w.session.PollRecords(pollCtx, 256)
		cancel()
		if ctx.Err() != nil {
			break
		}
		if err := fetches.Err(); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("fetch collision cell input: %w", err)
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

func (w *CollisionCellWorker) restorePartitions(ctx context.Context, offsets map[string]map[int32]kgo.Offset) (map[string]map[int32]kgo.Offset, error) {
	if len(offsets[w.inputTopic]) == 0 {
		return offsets, nil
	}
	start := time.Now()
	partitions, err := w.store.restoreCollisionCells(ctx, offsets[w.inputTopic])
	observability.RuntimeFromContext(w.ctx).RecordProcessingDuration(w.ctx, "collision_cell_restore", "checkpoint", time.Since(start))
	if err != nil {
		if ctx.Err() == nil && w.ctx.Err() == nil {
			w.stop(fmt.Errorf("restore collision cell partitions: %w", err))
		}
		return nil, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for partition, state := range partitions {
		w.partitions[partition] = state
		for key, cell := range state.cells {
			for id, entry := range cell.entries {
				w.updateExpiry(partition, string(appendKeyPart([]byte(key), id)), entry)
			}
		}
	}
	return offsets, nil
}

func (w *CollisionCellWorker) dropPartitions(_ context.Context, _ *kgo.Client, revoked map[string][]int32) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, partition := range revoked[w.inputTopic] {
		if state := w.partitions[partition]; state != nil {
			for key, cell := range state.cells {
				for id := range cell.entries {
					w.updateExpiry(partition, string(appendKeyPart([]byte(key), id)), nil)
				}
			}
		}
		delete(w.partitions, partition)
	}
}

func (w *CollisionCellWorker) updateExpiry(partition int32, key string, entry *collisionCellEntry) {
	var at time.Time
	if entry != nil {
		at = time.Unix(0, entry.input.Sample.ExpiresAtUnixNano)
	}
	w.deadlines.update(partition, key, at, entry)
}

func (w *CollisionCellWorker) appendExpirations(ctx context.Context, now time.Time) (time.Time, error) {
	wake := now.Add(100 * time.Millisecond)
	router := collisionRouter{topic: w.inputTopic, partitions: w.store.partitions}
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
		input := w.deadlines.pop().value.input
		record, err := router.record(cellFromEnvelope(input.Cell), input.Sample, streampb.CollisionCellInput_ACTION_EXPIRE)
		if err != nil {
			w.mu.Unlock()
			return wake, err
		}
		records = append(records, record)
	}
	w.mu.Unlock()
	if len(records) == 0 {
		return wake, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return wake, w.store.client.ProduceSync(ctx, records...).FirstErr()
}

func (w *CollisionCellWorker) process(ctx context.Context, fetches kgo.Fetches) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	batch := make(map[int32]*collisionCellBatch)
	var outputs []*kgo.Record
	outcomes := make([]string, 0, fetches.NumRecords())
	telemetry := observability.RuntimeFromContext(ctx)
	for record := range fetches.RecordsAll() {
		state := w.partitions[record.Partition]
		if state == nil {
			return fmt.Errorf("collision state was not restored for partition %d", record.Partition)
		}
		updates := batch[record.Partition]
		if updates == nil {
			updates = &collisionCellBatch{original: make(map[string]collisionCellChange), frontier: state.frontier}
			batch[record.Partition] = updates
		}
		if updates.frontier != nil && record.Offset < updates.frontier.NextOffset {
			return fmt.Errorf("collision cell partition %d offset %d precedes checkpoint %d", record.Partition, record.Offset, updates.frontier.NextOffset)
		}
		input := new(streampb.CollisionCellInput)
		if err := proto.Unmarshal(record.Value, input); err != nil {
			return fmt.Errorf("decode collision cell input: %w", err)
		}
		key, err := validateCollisionCellInput(input)
		if err != nil {
			return fmt.Errorf("collision partition %d offset %d: %w", record.Partition, record.Offset, err)
		}
		if !bytes.Equal(record.Key, key) || record.Partition != int32(crc32.ChecksumIEEE(key)%uint32(w.store.partitions)) {
			return fmt.Errorf("collision cell input arrived in the wrong partition")
		}
		start := time.Now()
		telemetry.RecordQueueWait(ctx, "collision_cell", max(0, start.Sub(record.Timestamp)))
		cellKey, asset := string(key), input.Sample.TrackableId
		cell := state.cells[cellKey]
		outcome := "obsolete"
		if cell != nil || input.Action == streampb.CollisionCellInput_ACTION_UPSERT {
			if cell == nil {
				cell = newCollisionCellState()
				state.cells[cellKey] = cell
			}
			prior := cell.entries[asset]
			next, matches, result := cell.evaluate(input)
			outcome = result
			if next != prior {
				stateKey := string(appendKeyPart(key, asset))
				if _, seen := updates.original[stateKey]; !seen {
					updates.original[stateKey] = collisionCellChange{cellKey: cellKey, asset: asset, prior: prior}
				}
				cell.set(asset, next)
			}
			for _, other := range matches {
				out, err := collisionPairRecord(w.outputTopic, w.store.partitions, input, other)
				if err != nil {
					return fmt.Errorf("encode collision pair input: %w", err)
				}
				outputs = append(outputs, out)
			}
		}
		telemetry.RecordStreamServiceTime(ctx, "collision_cell", "index", "collision_pair", "shadow", time.Since(start))
		outcomes = append(outcomes, outcome)
		updates.frontier = &streampb.CollisionCellFrontier{
			InputTopicId: w.store.inputID[:], NextOffset: record.Offset + 1, PairTopicId: w.store.outputID[:],
			PairPartitions: w.store.partitions, TopologyVersion: collisionTopologyVersion,
		}
	}
	checkpoints, err := w.store.collisionCellRecords(batch, w.partitions)
	if err != nil {
		return fmt.Errorf("encode collision cell checkpoints: %w", err)
	}
	start := time.Now()
	committed, err := commitStreamBatch(ctx, w.session, append(outputs, checkpoints...))
	telemetry.RecordProcessingDuration(ctx, "collision_cell_commit", "checkpoint", time.Since(start))
	if err != nil {
		return fmt.Errorf("commit collision cell batch: %w", err)
	}
	for partition, updates := range batch {
		state := w.partitions[partition]
		if committed {
			state.frontier = updates.frontier
		}
		emptied := make(map[string]struct{})
		for key, change := range updates.original {
			cell := state.cells[change.cellKey]
			if committed {
				w.updateExpiry(partition, key, cell.entries[change.asset])
			} else {
				cell.set(change.asset, change.prior)
			}
			if len(cell.entries) == 0 {
				emptied[change.cellKey] = struct{}{}
			}
		}
		for key := range emptied {
			if len(state.cells[key].entries) == 0 {
				delete(state.cells, key)
			}
		}
	}
	for _, outcome := range outcomes {
		if !committed {
			outcome = "rebalanced"
		}
		telemetry.RecordStreamHandled(ctx, "collision_cell", "index", "collision_pair", "shadow", outcome)
		if outcome == "candidate_limit" {
			telemetry.RecordRuntimeDrop(ctx, "collision_cell", outcome)
		}
	}
	if committed {
		for range outputs {
			telemetry.RecordStreamHandled(ctx, "collision_cell", "pair_candidate", "collision_pair", "shadow", "committed")
		}
	}
	return nil
}
