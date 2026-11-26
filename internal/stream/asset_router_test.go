package stream

import (
	"bytes"
	"testing"
	"time"

	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"google.golang.org/protobuf/proto"
)

func TestAssetRoutingKeepsOneKeyAcrossProviders(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_788_000_000, 0)
	first := Observation{
		Kind: Location, ProviderID: "gps", Source: "satellite",
		Payload: map[string]any{"trackables": []string{"forklift", "forklift", "trailer"}},
	}
	second := Observation{
		Kind: Proximity, ProviderID: "rfid", Source: "gate",
		Payload: map[string]any{"source": "gate"}, ResolvedTrackableIDs: []string{"forklift"},
		ResolvedProximityLocation: map[string]any{"source": "zone"},
	}
	_, original, err := encodeObservation(first, "hub", "rest", now)
	if err != nil {
		t.Fatal(err)
	}
	_, later, err := encodeObservation(second, "hub", "mqtt", now)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := assetRecords(original, "assets")
	if err != nil || len(inputs) != 2 {
		t.Fatalf("expand location: %d inputs, error %v", len(inputs), err)
	}
	additional, err := assetRecords(later, "assets")
	if err != nil || len(additional) != 1 {
		t.Fatalf("expand proximity: %d inputs, error %v", len(additional), err)
	}
	if !bytes.Equal(inputs[0].Key, additional[0].Key) || bytes.Equal(inputs[0].Key, inputs[1].Key) {
		t.Fatal("partition keys do not identify the asset independently of source")
	}
	var event streampb.AssetInput
	if err := proto.Unmarshal(additional[0].Value, &event); err != nil {
		t.Fatal(err)
	}
	if event.GetTrackableId() != "forklift" || event.GetObservation().GetProviderId() != "rfid" ||
		len(event.GetObservation().GetResolvedProximityLocationJson()) == 0 {
		t.Fatalf("proximity input lost its source or association: %s", &event)
	}
	retry, err := assetRecords(later, "assets")
	if err != nil || !bytes.Equal(retry[0].Value, additional[0].Value) {
		t.Fatalf("transaction retry produced different output: %v", err)
	}
}
