package stream

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"google.golang.org/protobuf/proto"
)

func TestMotionEstimateRecoverAndCrossDateline(t *testing.T) {
	t.Parallel()
	model := motionEstimator{}
	start := time.Unix(1_700_000_000, 0)
	first := testEstimateInput(t, start, "provider", 179.999)
	result, checkpoint, outcome, err := model.estimate(first, nil)
	if err != nil || outcome != "estimated" || result.Sequence != 1 || !result.FilterReset {
		t.Fatalf("first estimate: %v %v %v", result, outcome, err)
	}
	data, err := proto.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	var restored streampb.EstimateCheckpoint
	if err := proto.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	second := testEstimateInput(t, start.Add(5*time.Second), "provider", -179.999)
	got, next, outcome, err := model.estimate(second, &restored)
	if err != nil || outcome != "estimated" || got.Sequence != 2 || got.FilterReset {
		t.Fatalf("estimate after recovery: %v %v %v", got, outcome, err)
	}
	var location gen.Location
	if err := json.Unmarshal(got.LocationJson, &location); err != nil {
		t.Fatal(err)
	}
	if location.Speed == nil || *location.Speed < 30 || *location.Speed > 65 {
		t.Fatalf("speed should reflect the dateline crossing, got %v", location.Speed)
	}
	if location.Course == nil || math.Abs(*location.Course-90) > 0.001 {
		t.Fatalf("course should point east, got %v", location.Course)
	}
	inMemory, _, _, err := model.estimate(second, checkpoint)
	if err != nil || !proto.Equal(got, inMemory) {
		t.Fatalf("recovery changed the estimate: %v", err)
	}
	if estimate, update, outcome, err := model.estimate(first, next); err != nil || outcome != "out_of_order" || estimate != nil || update != nil {
		t.Fatalf("late observation rewound the estimate: %v %v %v", estimate, outcome, err)
	}
	changed := testEstimateInput(t, start.Add(6*time.Second), "different provider", 8)
	estimate, _, _, err := model.estimate(changed, next)
	if err != nil || !estimate.GetFilterReset() || estimate.Sequence != 3 || len(estimate.GetPreviousPosition()) != 0 {
		t.Fatalf("incompatible provider frame did not reset the filter: %v %v", estimate, err)
	}
}

func testEstimateInput(t *testing.T, at time.Time, provider string, longitude float64) *streampb.AssetInput {
	t.Helper()
	input := Observation{
		Kind: Location, ProviderID: provider, Source: "airfield", ObservedAt: &at,
		ResolvedTrackableIDs: []string{"asset"},
		Payload: map[string]any{
			"provider_id": provider, "provider_type": "gps", "source": "airfield", "crs": "EPSG:4326",
			"position": map[string]any{"type": "Point", "coordinates": []float64{longitude, 0, 30}},
		},
	}
	_, value, err := input.encode("hub", "websocket", at)
	if err != nil {
		t.Fatal(err)
	}
	records, err := assetRecords(value, "asset-inputs")
	if err != nil || len(records) != 1 {
		t.Fatalf("route input: %v %v", records, err)
	}
	result := new(streampb.AssetInput)
	if err := proto.Unmarshal(records[0].Value, result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestEstimatorFrontierRejectsMissingOrUnrelatedState(t *testing.T) {
	t.Parallel()
	id := []byte("input generation")
	initial := &partitionEstimates{}
	if err := validateEstimatorFrontier(initial, id, -2); err != nil {
		t.Fatal(err)
	}
	if err := validateEstimatorFrontier(initial, id, 5); err == nil {
		t.Fatal("committed input offset has no checkpoint frontier")
	}
	saved := &partitionEstimates{frontier: &streampb.EstimatorFrontier{InputTopicId: id, NextOffset: 5}}
	if err := validateEstimatorFrontier(saved, id, 5); err != nil {
		t.Fatal(err)
	}
	if err := validateEstimatorFrontier(saved, id, 4); err == nil {
		t.Fatal("offset reset reused incompatible state")
	}
	if err := validateEstimatorFrontier(saved, []byte("other generation"), 5); err == nil {
		t.Fatal("recreated input topic reused incompatible state")
	}
}
