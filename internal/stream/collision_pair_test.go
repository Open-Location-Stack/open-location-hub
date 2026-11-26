package stream

import (
	"bytes"
	"encoding/json"
	"hash/crc32"
	"testing"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"github.com/google/uuid"
)

func TestCollisionPairMergesCellVersionsAndRestoresExit(t *testing.T) {
	t.Parallel()
	start := time.Unix(1700000000, 0).UTC()
	leftID := "1c97b198-172a-4154-899a-e3502d24d605"
	rightID := "27cf029a-2888-42c2-9126-647ba9e6cd11"
	left1 := pairSample(t, leftID, 1, 0, start)
	right1 := pairSample(t, rightID, 1, 0.0002, start)
	pair, events, _, err := evaluateCollisionPair(pairInput(left1, right1, rightID), nil, start)
	requireCollisionTransition(t, events, err, gen.CollisionStart, start, rightID)

	nextAt := start.Add(time.Second)
	left2 := pairSample(t, leftID, 2, 0.0003, nextAt)
	pair, events, _, err = evaluateCollisionPair(pairInput(left2, right1, leftID), pair, nextAt)
	requireCollisionTransition(t, events, err, gen.Colliding, nextAt, leftID)
	dupe, events, _, err := evaluateCollisionPair(pairInput(left2, right1, rightID), pair, nextAt)
	if err != nil || dupe != pair || len(events) != 0 {
		t.Fatalf("duplicate replica facts changed the state: %v, %v", events, err)
	}

	departureAt := start.Add(2 * time.Second)
	right2 := pairSample(t, rightID, 2, 0.3, departureAt)
	pair, events, _, err = evaluateCollisionPair(pairInput(left1, right2, rightID), pair, departureAt)
	deadline := departureAt.Add(8 * time.Second)
	if err != nil || len(events) != 0 || pair.left.sample != left2 || pair.right.sample != right2 || !pair.nextDeadline().Equal(deadline) {
		t.Fatalf("cross-cell update did not merge the latest two samples and start the exit timer: %v, %v", events, err)
	}

	key := collisionPairKey(left2, right2)
	partition := int32(crc32.ChecksumIEEE(key) % 12)
	store := checkpointStore{topic: "pair-state"}
	checkpoints, err := store.collisionPairRecords(map[int32]*collisionPairPartition{
		partition: {pairs: map[string]*collisionPair{string(key): pair}, frontier: &streampb.CollisionPairFrontier{NextOffset: 12}},
	})
	if err != nil {
		t.Fatal(err)
	}
	restored := &collisionPairPartition{pairs: make(map[string]*collisionPair)}
	for _, checkpoint := range checkpoints {
		if err := applyCollisionPairCheckpoint(restored, checkpoint, 12); err != nil {
			t.Fatal(err)
		}
	}
	generation := collisionPairVersionID(left2, right2)
	pair = restored.pairs[string(key)]
	stale := &streampb.CollisionPairInput{DeadlineAtUnixNano: deadline.UnixNano(), DeadlineGeneration: collisionPairVersionID(left2, right1)}
	if same, got, _, err := evaluateCollisionPair(stale, pair, deadline); err != nil || same != pair || len(got) != 0 {
		t.Fatalf("obsolete timer changed checkpoint: %v, %v", got, err)
	}
	live := &streampb.CollisionPairInput{DeadlineAtUnixNano: deadline.UnixNano(), DeadlineGeneration: generation}
	pair, events, _, err = evaluateCollisionPair(live, pair, deadline.Add(time.Second))
	end := requireCollisionTransition(t, events, err, gen.CollisionEnd, deadline, rightID)
	if end.EndTime == nil || !end.EndTime.Equal(deadline) || end.StartTime == nil || !end.StartTime.Equal(start) {
		t.Fatalf("exit timestamps did not survive recovery: %+v", end)
	}
	if _, got, _, err := evaluateCollisionPair(live, pair, deadline); err != nil || len(got) != 0 {
		t.Fatalf("deadline retry repeated the exit: %v, %v", got, err)
	}
	_, late, _, err := evaluateCollisionPair(pairInput(left1, right1, rightID), nil, start.Add(2*time.Minute))
	if err != nil || len(late) != 0 {
		t.Fatalf("expired records recreated a contact after cleanup: %v, %v", late, err)
	}
}

func pairSample(t *testing.T, id string, seq uint64, longitude float64, at time.Time) *streampb.CollisionSample {
	t.Helper()
	estimate := collisionTestEstimate(t, id, seq, longitude)
	return &streampb.CollisionSample{
		EstimateId: estimate.EventId, OriginHubId: "hub", TrackableId: id, Sequence: seq, ConfigurationRevision: 9,
		ProcessedAtUnixNano: at.UnixNano(), ExpiresAtUnixNano: at.Add(time.Minute).UnixNano(),
		LocationJson: estimate.LocationJson, RadiusMeters: 50, ToleranceTimeoutNanos: -1,
		ExitDelayNanos: int64(8 * time.Second), Eligible: true,
	}
}

func pairInput(left, right *streampb.CollisionSample, updated string) *streampb.CollisionPairInput {
	return &streampb.CollisionPairInput{Left: left, Right: right, UpdatedTrackableId: updated}
}

func requireCollisionTransition(t *testing.T, events []*streampb.CollisionTransition, err error, kind gen.CollisionEventCollisionType, at time.Time, leading string) gen.CollisionEvent {
	t.Helper()
	if err != nil || len(events) != 1 {
		t.Fatalf("expected one %s transition: got %v, %v", kind, events, err)
	}
	var event gen.CollisionEvent
	if err := json.Unmarshal(events[0].CollisionEventJson, &event); err != nil {
		t.Fatal(err)
	}
	if event.CollisionType != kind || event.CollisionTime == nil || !event.CollisionTime.Equal(at) ||
		len(event.Collisions) != 2 || event.Collisions[0].Id.String() != leading ||
		event.Id.Version() != uuid.Version(7) || bytes.Equal(events[0].EventId, events[0].CauseId) {
		t.Fatalf("unexpected %s event: %+v", kind, event)
	}
	return event
}
