package hub

import (
	"bytes"
	"testing"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
)

func TestNewEventPreparesCachedLocationJSON(t *testing.T) {
	t.Parallel()

	crs := "local"
	location := gen.Location{
		ProviderId: "provider-a",
		Source:     "source-a",
		Crs:        &crs,
	}
	feature := GeoJSONFeatureCollection{Type: "FeatureCollection"}

	event, err := newEvent(
		EventLocation,
		ScopeLocal,
		time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC),
		location.ProviderId,
		"",
		"",
		"hub-a",
		LocationEnvelope{Location: location, GeoJSON: feature},
	)
	if err != nil {
		t.Fatalf("newEvent failed: %v", err)
	}

	envelope, ok := event.Payload.(LocationEnvelope)
	if !ok {
		t.Fatalf("unexpected payload type: %T", event.Payload)
	}
	if len(envelope.locationJSON) == 0 {
		t.Fatal("expected cached location json")
	}
	if len(envelope.geoJSONJSON) == 0 {
		t.Fatal("expected cached geojson json")
	}
	if !bytes.Equal(envelope.LocationItemJSON(), envelope.locationJSON) {
		t.Fatal("expected location accessor to reuse cached json")
	}
	if !bytes.Equal(envelope.GeoJSONItemJSON(), envelope.geoJSONJSON) {
		t.Fatal("expected geojson accessor to reuse cached json")
	}
}

func TestNewEventPreparesCachedMetadataJSON(t *testing.T) {
	t.Parallel()

	change := MetadataChange{
		ID:        "zone-1",
		Type:      "zone",
		Operation: "update",
		Timestamp: time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC),
	}

	event, err := newEvent(EventMetadataChange, ScopeMetadata, change.Timestamp, "", "", "", "hub-a", change)
	if err != nil {
		t.Fatalf("newEvent failed: %v", err)
	}

	out, ok := event.Payload.(MetadataChange)
	if !ok {
		t.Fatalf("unexpected payload type: %T", event.Payload)
	}
	if len(out.changeJSON) == 0 {
		t.Fatal("expected cached metadata json")
	}
	if !bytes.Equal(out.ItemJSON(), out.changeJSON) {
		t.Fatal("expected metadata accessor to reuse cached json")
	}
}
