package stream

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

func TestCollisionCellsMeetAcrossAntimeridian(t *testing.T) {
	t.Parallel()
	config, left, right := collisionTestConfiguration(50)
	route := collisionRouter{topic: "cells", partitions: 12, ttl: time.Minute}
	at := time.Unix(1700000000, 0).UnixNano()
	leftInputs, leftState, _, err := route.route(context.Background(), collisionTestEstimate(t, left, 1, 179.99985), nil, config, at)
	if err != nil || len(leftInputs) > collisionMaxCellsPerAsset {
		t.Fatalf("left coverage: %d, %v", len(leftInputs), err)
	}
	rightInputs, rightState, _, err := route.route(context.Background(), collisionTestEstimate(t, right, 2, -179.99985), nil, config, at)
	if err != nil || len(rightInputs) > collisionMaxCellsPerAsset {
		t.Fatalf("right coverage: %d, %v", len(rightInputs), err)
	}
	leftCells := make([]collisionCell, 0, len(leftState.Cells))
	for _, cell := range leftState.Cells {
		leftCells = append(leftCells, cellFromEnvelope(cell))
	}
	for _, cell := range rightState.Cells {
		if slices.Contains(leftCells, cellFromEnvelope(cell)) {
			return
		}
	}
	t.Fatal("overlapping extents across the antimeridian had no common owner")
}

func TestCollisionCellReconsidersDepartedCoverage(t *testing.T) {
	t.Parallel()
	config, leftID, rightID := collisionTestConfiguration(50)
	route := collisionRouter{topic: "cells", partitions: 12, ttl: time.Minute}
	at := time.Unix(1700000000, 0)
	left, prior, _, err := route.route(context.Background(), collisionTestEstimate(t, leftID, 1, 0), nil, config, at.UnixNano())
	if err != nil || len(left) == 0 {
		t.Fatalf("left route: %v", err)
	}
	checkpoint, err := proto.Marshal(prior)
	if err != nil {
		t.Fatal(err)
	}
	prior = new(streampb.CollisionRoutingCheckpoint)
	if err := proto.Unmarshal(checkpoint, prior); err != nil {
		t.Fatal(err)
	}
	right, _, _, err := route.route(context.Background(), collisionTestEstimate(t, rightID, 1, 0.0002), nil, config, at.UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	var leftInput, rightInput *streampb.CollisionCellInput
	for _, record := range right {
		if bytes.Equal(record.Key, left[0].Key) {
			leftInput, rightInput = decodeCellTestInput(t, left[0]), decodeCellTestInput(t, record)
			break
		}
	}
	if leftInput == nil {
		t.Fatal("adjacent tracks did not share a cell")
	}
	cell := newCollisionCellState()
	leftEntry, _, _ := cell.evaluate(leftInput)
	cell.set(leftID, leftEntry)
	rightEntry, matches, _ := cell.evaluate(rightInput)
	cell.set(rightID, rightEntry)
	if len(matches) != 1 || matches[0].TrackableId != leftID {
		t.Fatalf("shared cell missed the potential contact: %v", matches)
	}
	entry, err := collisionPairRecord("pairs", 12, rightInput, matches[0])
	if err != nil {
		t.Fatal(err)
	}
	movement, _, _, err := route.route(context.Background(), collisionTestEstimate(t, leftID, 2, 0.3), prior, config, at.Add(time.Second).UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range movement {
		if !bytes.Equal(record.Key, left[0].Key) {
			continue
		}
		withdrawal := decodeCellTestInput(t, record)
		next, matches, _ := cell.evaluate(withdrawal)
		if withdrawal.Action != streampb.CollisionCellInput_ACTION_WITHDRAW || next != nil || len(matches) != 1 || matches[0].TrackableId != rightID {
			t.Fatalf("withdrawal lost the old neighbor: %v, %v", withdrawal, matches)
		}
		departure, err := collisionPairRecord("pairs", 12, withdrawal, matches[0])
		if err != nil || !bytes.Equal(entry.Key, departure.Key) || entry.Partition != departure.Partition {
			t.Fatalf("pair changed owner on jump: %v", err)
		}
		return
	}
	t.Fatal("cell jump failed to withdraw old coverage")
}

func TestCollisionCellBoundsCandidateOutputAndExpiresByGeneration(t *testing.T) {
	t.Parallel()
	cell := newCollisionCellState()
	input := &streampb.CollisionCellInput{
		Action: streampb.CollisionCellInput_ACTION_UPSERT,
		Sample: &streampb.CollisionSample{TrackableId: "anchor", Sequence: 1, ProcessedAtUnixNano: 1, ExpiresAtUnixNano: 2,
			EcefMeters: []float64{6371008, 0, 0}, RadiusMeters: 50, EstimateId: []byte{1}},
	}
	entry := newCollisionCellEntry(input)
	cell.set("anchor", entry)
	for index := range maxCollisionCandidates + 1 {
		other := proto.Clone(input).(*streampb.CollisionCellInput)
		other.Sample.TrackableId = fmt.Sprintf("neighbor-%d", index)
		cell.set(other.Sample.TrackableId, newCollisionCellEntry(other))
	}
	newer := proto.Clone(input).(*streampb.CollisionCellInput)
	newer.Sample.Sequence = 2
	newer.Sample.EstimateId = []byte{2}
	next, pairs, outcome := cell.evaluate(newer)
	if next == nil || len(pairs) != 0 || outcome != "candidate_limit" {
		t.Fatalf("dense index emitted an incomplete candidate set: %d, %s", len(pairs), outcome)
	}
	cell.set("anchor", next)
	expired := proto.Clone(input).(*streampb.CollisionCellInput)
	expired.Action = streampb.CollisionCellInput_ACTION_EXPIRE
	if next, _, outcome := cell.evaluate(expired); next == nil || outcome != "obsolete" {
		t.Fatal("the earlier sample expiry removed a newer motion")
	}
	expired.Sample.EstimateId = newer.Sample.EstimateId
	if next, _, outcome := cell.evaluate(expired); next != nil || outcome != "expired" {
		t.Fatal("the active sample expiry did not remove its motion")
	}
}

func collisionTestConfiguration(radius float64) (*locatingConfiguration, string, string) {
	config := newLocatingConfiguration(8)
	config.geometry = &fenceConfiguration{}
	config.geometry.rebuild()
	left, right := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{left, right} {
		shard := &config.shards[configurationShard(id.String())]
		if shard.trackables == nil {
			shard.trackables = make(map[string]gen.Trackable)
		}
		shard.trackables[id.String()] = gen.Trackable{Id: id, Radius: &radius}
	}
	return config, left.String(), right.String()
}

func collisionTestEstimate(t *testing.T, id string, sequence uint64, longitude float64) *streampb.MotionEstimate {
	t.Helper()
	result := routingTestEstimate(t, id, sequence, longitude, nil)
	event := sha256.Sum256(fmt.Appendf(nil, "%s:%d", id, sequence))
	result.EventId = event[:]
	return result
}

func decodeCellTestInput(t *testing.T, record *kgo.Record) *streampb.CollisionCellInput {
	t.Helper()
	input := new(streampb.CollisionCellInput)
	if err := proto.Unmarshal(record.Value, input); err != nil {
		t.Fatal(err)
	}
	if key, err := validateCollisionCellInput(input); err != nil || !bytes.Equal(key, record.Key) {
		t.Fatalf("invalid route output key: %v", err)
	}
	return input
}
