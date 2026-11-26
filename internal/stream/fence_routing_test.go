package stream

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

func TestFenceRouterRetainsDepartedCoverageAcrossRestart(t *testing.T) {
	t.Parallel()
	const asset = "test-asset"
	var delay gen.PositiveOrMinusOne
	if err := delay.FromPositiveNumber(8000); err != nil {
		t.Fatal(err)
	}
	fence := routingTestFence(t, 0, 50)
	fence.ExitDelay = &delay
	config := routingTestConfig(fence)
	router := fenceRouter{topic: "fixture", partitions: 12}
	origin := time.Unix(1700000000, 0)
	ctx := context.Background()
	first := routingTestEstimate(t, asset, 1, 0, nil)
	entry, state, err := router.route(ctx, first, nil, config, origin.UnixNano())
	if err != nil || len(entry) != 1 || len(state.GetInterests()) != 1 {
		t.Fatalf("initial route did not retain a candidate: %d, %v", len(entry), err)
	}
	encoded, err := proto.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	state = new(streampb.FenceRoutingCheckpoint)
	if err := proto.Unmarshal(encoded, state); err != nil {
		t.Fatal(err)
	}

	jump := routingTestEstimate(t, asset, 2, 0.2, nil)
	departure, state, err := router.route(ctx, jump, state, config, origin.Add(time.Second).UnixNano())
	if err != nil || len(departure) != 1 || len(state.GetInterests()) != 1 {
		t.Fatalf("cell jump missed the departed fence: %d, %v", len(departure), err)
	}
	if !bytes.Equal(entry[0].Key, departure[0].Key) || entry[0].Partition != departure[0].Partition {
		t.Fatal("movement changed membership partition ownership")
	}
	var evaluation streampb.FenceEvaluation
	if err := proto.Unmarshal(departure[0].Value, &evaluation); err != nil {
		t.Fatal(err)
	}
	if evaluation.FenceId != fence.Id.String() || evaluation.AssetSequence != 2 ||
		evaluation.ConfigurationRevision != config.revision || evaluation.ExitDelayNanos != int64(8*time.Second) {
		t.Fatalf("unexpected departure input: %v", &evaluation)
	}
	if state.Interests[0].GetCertainlyExpiredAtUnixNano() != origin.Add(9*time.Second).UnixNano() {
		t.Fatal("outside delay did not constrain the retained membership")
	}
	later := routingTestEstimate(t, asset, 3, 0.3, nil)
	records, state, err := router.route(ctx, later, state, config, origin.Add(10*time.Second).UnixNano())
	if err != nil || len(records) != 0 || len(state.GetInterests()) != 0 {
		t.Fatalf("expired interest kept receiving inputs: %d, %v", len(records), err)
	}
}

func TestFenceRouterCancelsOnFloorChangeOrDeletion(t *testing.T) {
	t.Parallel()
	fence := routingTestFence(t, 0, 50)
	floor := 1.0
	fence.Floor = &floor
	config := routingTestConfig(fence)
	router := fenceRouter{topic: "fixture", partitions: 12}
	at := time.Unix(1700000000, 0)
	estimate := routingTestEstimate(t, "asset", 1, 0, &floor)
	_, state, err := router.route(context.Background(), estimate, nil, config, at.UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	newFloor := 2.0
	cases := []struct {
		name   string
		floor  *float64
		config *locatingConfiguration
	}{
		{"floor", &newFloor, config},
		{"removed", &floor, config.withChanges(config.revision+1, []locatingChange{{kind: "fence", id: fence.Id.String(), remove: true}})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			location := routingTestEstimate(t, "asset", 2, 0, tc.floor)
			records, next, err := router.route(context.Background(), location, state, tc.config, at.Add(time.Second).UnixNano())
			if err != nil || len(records) != 1 || len(next.GetInterests()) != 0 {
				t.Fatalf("cancellation did not release membership: %d, %v", len(records), err)
			}
			var evaluation streampb.FenceEvaluation
			if err := proto.Unmarshal(records[0].Value, &evaluation); err != nil || evaluation.Action != streampb.FenceEvaluation_ACTION_CANCEL {
				t.Fatalf("unexpected routing action: %v %v", &evaluation, err)
			}
		})
	}
}

func TestFenceRouterWrapsTrackableExtentAtAntimeridian(t *testing.T) {
	t.Parallel()
	fence := routingTestFence(t, 179.999, 100)
	config := routingTestConfig(fence)
	config.shards[configurationShard("asset")].trackables = map[string]gen.Trackable{"asset": {Radius: proto.Float64(200)}}
	estimate := routingTestEstimate(t, "asset", 1, -179.999, nil)
	records, _, err := (fenceRouter{topic: "fixture", partitions: 12}).route(context.Background(), estimate, nil, config, time.Unix(1700000000, 0).UnixNano())
	if err != nil || len(records) != 1 {
		t.Fatalf("wrapped trackable extent did not reach the nearby fence: %d, %v", len(records), err)
	}
}

func routingTestFence(t *testing.T, longitude, radiusMetres float64) gen.Fence {
	t.Helper()
	fence := gen.Fence{Id: uuid.New(), Radius: (*gen.PositiveNumber)(&radiusMetres)}
	var point gen.Point
	point.Type = "Point"
	if err := point.Coordinates.FromGeoJsonPosition2D([]float64{longitude, 0}); err != nil {
		t.Fatal(err)
	}
	if err := fence.Region.FromPoint(point); err != nil {
		t.Fatal(err)
	}
	return fence
}

func routingTestConfig(fence gen.Fence) *locatingConfiguration {
	config := newLocatingConfiguration(7)
	config.geometry = &fenceConfiguration{fences: map[string]gen.Fence{fence.Id.String(): fence}}
	config.geometry.rebuild()
	return config
}

func routingTestEstimate(t *testing.T, asset string, sequence uint64, longitude float64, floor *float64) *streampb.MotionEstimate {
	t.Helper()
	loc := gen.Location{ProviderId: "gps", Source: "source", Floor: floor, Crs: proto.String("EPSG:4326")}
	loc.Position.Type = "Point"
	if err := loc.Position.Coordinates.FromGeoJsonPosition2D([]float64{longitude, 0}); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(loc)
	if err != nil {
		t.Fatal(err)
	}
	return &streampb.MotionEstimate{TrackableId: asset, Sequence: sequence, OriginHubId: "hub", EventId: []byte{byte(sequence)}, LocationJson: payload}
}
