package ws

import (
	"encoding/json"
	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/hub"
	"testing"
)

func TestSubscriptionsSelectOneVariantAndProject(t *testing.T) {
	wgs := "EPSG:4326"
	local := "local"
	floor := 2.0
	var coords gen.GeoJsonPosition
	_ = coords.FromGeoJsonPosition2D(gen.GeoJsonPosition2D{13, 52})
	loc := gen.Location{Crs: &wgs, Source: "source", ProviderId: "provider", ProviderType: "gps", Floor: &floor, Position: gen.Point{Type: "Point", Coordinates: coords}}
	native := loc
	native.Crs = &local
	_ = native.Position.Coordinates.FromGeoJsonPosition2D(gen.GeoJsonPosition2D{4, 5})
	events := []hub.Event{{Kind: hub.EventLocation, Scope: hub.ScopeLocal, Native: true, Payload: hub.LocationEnvelope{Location: native}}, {Kind: hub.EventLocation, Scope: hub.ScopeEPSG4326, Payload: hub.LocationEnvelope{Location: loc}}}
	for _, tc := range []struct {
		crs string
		x   float64
	}{{"", 13}, {"local", 4}, {"EPSG:32633", 362705.6}} {
		raw, ok := payloadBatchForSubscription(subscription{topic: topicLocationUpdates, filter: locationFilter{CRS: tc.crs}}, events, &hub.Service{})
		if !ok {
			t.Fatalf("missing %s", tc.crs)
		}
		var out []gen.Location
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 {
			t.Fatalf("duplicate variants: %s", raw)
		}
		xy, _ := out[0].Position.Coordinates.AsGeoJsonPosition2D()
		d := xy[0] - tc.x
		if d < -1 || d > 1 {
			t.Fatalf("wrong projection: %s", raw)
		}
	}
	other := 3.0
	if _, ok := payloadBatchForSubscription(subscription{topic: topicLocationUpdates, filter: locationFilter{Floor: &other}}, events, &hub.Service{}); ok {
		t.Fatal("floor filter ignored")
	}
}
func TestFenceObjectTypeFilter(t *testing.T) {
	id := "t"
	event := gen.FenceEvent{TrackableId: &id}
	if !matchFence(fenceFilter{ObjectType: "trackable"}, event) || matchFence(fenceFilter{ObjectType: "location_provider"}, event) {
		t.Fatal("incorrect object type filtering")
	}
}
