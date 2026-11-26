package stream

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/hub"
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"google.golang.org/protobuf/proto"
)

func TestFenceDeadlineQueueOrdersTiesAndUpdatesInPlace(t *testing.T) {
	t.Parallel()
	at := time.Unix(1700000000, 0)
	state := func(deadline time.Time) *fenceMembership {
		return &fenceMembership{deadlines: hub.FenceDeadlines{Fence: deadline}}
	}
	queue := fenceDeadlineQueue{byKey: make(map[string]*fenceDeadline)}
	queue.update(0, "b", state(at))
	queue.update(0, "a", state(at.Add(time.Second)))
	queue.update(0, "a", state(at))
	queue.update(0, "cancelled", state(at.Add(-time.Second)))
	queue.update(0, "cancelled", nil)
	if queue.pop().key != "a" || queue.pop().key != "b" || !queue.next().IsZero() {
		t.Fatal("queue did not keep one deadline per key with stable tie order")
	}
}

func TestFenceMachineRestoresAndFencesExitTimers(t *testing.T) {
	t.Parallel()
	fence := routingTestFence(t, 0, 65)
	fence.ExitDelay = new(gen.PositiveOrMinusOne)
	if err := fence.ExitDelay.FromPositiveNumber(8000); err != nil {
		t.Fatal(err)
	}
	fence.ToleranceTimeout = new(gen.PositiveOrMinusOne)
	if err := fence.ToleranceTimeout.FromPositiveOrMinusOne0(gen.Minus1); err != nil {
		t.Fatal(err)
	}
	config := routingTestConfig(fence)
	router := fenceRouter{topic: "fixture", partitions: 12}
	machine := fenceMachine{geometry: make(map[string]gen.Fence)}
	ctx := context.Background()
	start := time.Unix(1700000000, 0).UTC()
	first := routingTestEstimate(t, "asset", 1, 0, nil)
	first.EventId = bytes.Repeat([]byte{1}, 32)
	records, ledger, err := router.route(ctx, first, nil, config, start.UnixNano())
	if err != nil || len(records) != 1 {
		t.Fatalf("route entry: %v", err)
	}
	entry := new(streampb.FenceEvaluation)
	if err := proto.Unmarshal(records[0].Value, entry); err != nil {
		t.Fatal(err)
	}
	state, transitions, _, err := machine.evaluate(entry, nil)
	if err != nil || state == nil || len(transitions) != 1 || transitions[0].Kind != streampb.FenceTransition_KIND_ENTRY {
		t.Fatalf("expected entry, got %v and %v", transitions, err)
	}
	jump := routingTestEstimate(t, "asset", 2, 0.3, nil)
	jump.EventId = bytes.Repeat([]byte{2}, 32)
	records, _, err = router.route(ctx, jump, ledger, config, start.Add(time.Second).UnixNano())
	if err != nil || len(records) != 1 {
		t.Fatalf("route cell jump: %v", err)
	}
	record := records[0]
	outside := new(streampb.FenceEvaluation)
	if err := proto.Unmarshal(record.Value, outside); err != nil {
		t.Fatal(err)
	}
	deadline := start.Add(9 * time.Second)
	state, transitions, _, err = machine.evaluate(outside, state)
	if err != nil || state == nil || len(transitions) != 0 || !state.deadlines.Next().Equal(deadline) {
		t.Fatalf("expected pending exit, got %v and %v", transitions, err)
	}
	key := string(record.Key)
	partition := &partitionMemberships{members: map[string]*fenceMembership{key: state}, frontier: &streampb.FenceMembershipFrontier{NextOffset: 2}}
	store := checkpointStore{topic: "checkpoint"}
	checkpoints, err := store.fenceRecords(map[int32]*partitionMemberships{record.Partition: partition})
	if err != nil {
		t.Fatal(err)
	}
	restored := &partitionMemberships{members: make(map[string]*fenceMembership)}
	for _, checkpoint := range checkpoints {
		if err := applyFenceCheckpoint(restored, checkpoint, router.partitions); err != nil {
			t.Fatal(err)
		}
	}
	state = restored.members[key]
	machine = fenceMachine{geometry: make(map[string]gen.Fence)}
	timer := &streampb.FenceEvaluation{
		Action: streampb.FenceEvaluation_ACTION_DEADLINE, ProcessedAtUnixNano: deadline.UnixNano(), DeadlineGeneration: entry.EventId,
	}
	if got, transitions, _, err := machine.evaluate(timer, state); err != nil || got != state || len(transitions) != 0 {
		t.Fatalf("obsolete deadline changed membership: %v, %v", transitions, err)
	}
	timer.DeadlineGeneration = outside.EventId
	state, transitions, _, err = machine.evaluate(timer, state)
	if err != nil || state != nil || len(transitions) != 1 || transitions[0].Kind != streampb.FenceTransition_KIND_EXIT ||
		transitions[0].EnteredAtUnixNano != start.UnixNano() || transitions[0].OccurredAtUnixNano != deadline.UnixNano() {
		t.Fatalf("restored deadline did not exit once at the original time: %v, %v", transitions, err)
	}
	if _, transitions, _, err := machine.evaluate(timer, state); err != nil || len(transitions) != 0 {
		t.Fatalf("duplicate deadline repeated the exit: %v, %v", transitions, err)
	}
}

func TestFenceMachineExpiresBeforeNextObservation(t *testing.T) {
	t.Parallel()
	fence := routingTestFence(t, 0, 60)
	fence.Timeout = new(gen.PositiveOrMinusOne)
	if err := fence.Timeout.FromPositiveNumber(1000); err != nil {
		t.Fatal(err)
	}
	config := routingTestConfig(fence)
	router := fenceRouter{topic: "fixture", partitions: 12}
	machine := fenceMachine{geometry: make(map[string]gen.Fence)}
	now := time.Unix(1700000000, 0)
	var state *fenceMembership
	var ledger *streampb.FenceRoutingCheckpoint
	for sequence := uint64(1); sequence <= 2; sequence++ {
		estimate := routingTestEstimate(t, "asset", sequence, 0, nil)
		records, nextLedger, err := router.route(context.Background(), estimate, ledger, config, now.UnixNano())
		if err != nil || len(records) != 1 {
			t.Fatalf("route transition: %v", err)
		}
		var input streampb.FenceEvaluation
		if err := proto.Unmarshal(records[0].Value, &input); err != nil {
			t.Fatal(err)
		}
		next, events, _, err := machine.evaluate(&input, state)
		if err != nil || next == nil || len(events) != int(sequence) || events[len(events)-1].Kind != streampb.FenceTransition_KIND_ENTRY {
			t.Fatalf("unexpected events at sequence %d: %v, %v", sequence, events, err)
		}
		if sequence == 2 && (events[0].Kind != streampb.FenceTransition_KIND_EXIT || events[0].OccurredAtUnixNano != now.UnixNano()) {
			t.Fatalf("previous membership did not expire before re-entry: %v", events)
		}
		state, ledger, now = next, nextLedger, now.Add(time.Second)
	}
}
