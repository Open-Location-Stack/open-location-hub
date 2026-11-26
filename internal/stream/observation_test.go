package stream

import (
	"bytes"
	"testing"
	"time"

	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"google.golang.org/protobuf/proto"
)

func TestObservationEncodingPreservesIdentityAcrossRetries(t *testing.T) {
	t.Parallel()

	observed := time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC)
	input := Observation{
		Kind: Location, ProviderID: "provider-stable-id", Source: "receiver",
		ObservedAt: &observed, Payload: map[string]any{"provider_id": "provider-stable-id"},
	}
	var decoded [2]streampb.Observation
	var keys [2][]byte
	for i, transport := range []string{"rest", "websocket"} {
		at := observed.Add(time.Duration(i+1) * time.Second)
		key, encoded, err := encodeObservation(input, "hub-a", transport, at)
		if err != nil {
			t.Fatal(err)
		}
		keys[i] = key
		record := &decoded[i]
		if err := proto.Unmarshal(encoded, record); err != nil {
			t.Fatal(err)
		}
		if record.GetSchemaVersion() != 1 || record.GetKind() != Location ||
			record.GetObservedAtUnixNano() != observed.UnixNano() ||
			record.GetReceivedAtUnixNano() != at.UnixNano() ||
			record.GetOriginHubId() != "hub-a" || record.GetTransport() != transport {
			t.Fatalf("incorrect envelope: %v", record)
		}
	}
	if !bytes.Equal(keys[0], keys[1]) || len(decoded[0].GetEventId()) != 32 ||
		!bytes.Equal(decoded[0].GetEventId(), decoded[1].GetEventId()) {
		t.Fatal("retry changed the partition key or event identity")
	}
	otherKey, _, err := encodeObservation(input, "hub-b", "rest", observed)
	if err != nil || bytes.Equal(otherKey, keys[0]) {
		t.Fatalf("origin hubs do not have distinct partition keys: %v", err)
	}
}
