package ws

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/hub"
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
		raw, ok := payloadBatchForSubscription(subscription{topic: topicLocationUpdates, filter: locationFilter{CRS: tc.crs}}, events, newProjectionCache(&hub.Service{}))
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
	if _, ok := payloadBatchForSubscription(subscription{topic: topicLocationUpdates, filter: locationFilter{Floor: &other}}, events, newProjectionCache(&hub.Service{})); ok {
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

type countingProjector struct {
	locationCalls  int
	motionCalls    int
	collisionCalls int
}

func (p *countingProjector) ProjectLocation(_ context.Context, location gen.Location, crs, _ string) (gen.Location, error) {
	p.locationCalls++
	location.Crs = &crs
	return location, nil
}

func (p *countingProjector) ProjectMotion(_ context.Context, motion gen.TrackableMotion, crs, _ string) (gen.TrackableMotion, error) {
	p.motionCalls++
	motion.Location.Crs = &crs
	return motion, nil
}

func (p *countingProjector) ProjectCollision(_ context.Context, event gen.CollisionEvent, _, _ string) (gen.CollisionEvent, error) {
	p.collisionCalls++
	return event, nil
}

func TestProjectionFiltersBeforeWorkAndSharesResultsAcrossSubscriptions(t *testing.T) {
	projector := &countingProjector{}
	cache := newProjectionCache(projector)
	location := testLocation(t)
	location.ProviderId = "wanted"
	other := location
	other.ProviderId = "other"
	events := []hub.Event{
		{Kind: hub.EventLocation, Scope: hub.ScopeEPSG4326, Payload: hub.LocationEnvelope{Location: location}},
		{Kind: hub.EventLocation, Scope: hub.ScopeEPSG4326, Payload: hub.LocationEnvelope{Location: other}},
		{Kind: hub.EventTrackableMotion, Scope: hub.ScopeEPSG4326, Payload: hub.TrackableMotionEnvelope{Motion: gen.TrackableMotion{Location: location}}},
		{Kind: hub.EventCollisionEvent, Scope: hub.ScopeEPSG4326, Payload: hub.CollisionEnvelope{}},
	}
	for _, crs := range []string{"", "EPSG:4326"} {
		for _, topic := range []string{topicLocationUpdates, topicLocationGeoJSON} {
			_, ok := payloadBatchForSubscription(subscription{topic: topic, filter: locationFilter{ProviderID: "wanted", CRS: crs}}, events, cache)
			if !ok {
				t.Fatal("missing matching payload")
			}
		}
	}
	if projector.locationCalls != 1 || projector.motionCalls != 0 || projector.collisionCalls != 0 {
		t.Fatalf("unnecessary projection: locations=%d motions=%d collisions=%d", projector.locationCalls, projector.motionCalls, projector.collisionCalls)
	}
	for _, filter := range []locationFilter{{ProviderID: "wanted", CRS: "local", ZoneID: "zone-a"}, {ProviderID: "wanted", CRS: "local", ZoneID: "zone-b"}} {
		if _, ok := payloadBatchForSubscription(subscription{topic: topicLocationUpdates, filter: filter}, events, cache); !ok {
			t.Fatal("missing local projection")
		}
	}
	if projector.locationCalls != 3 {
		t.Fatal("different projection targets shared an incorrect result")
	}
}

func TestNativeMotionFiltersUseOriginalObservation(t *testing.T) {
	projector := &countingProjector{}
	original := gen.TrackableMotion{Location: testLocation(t)}
	original.Location.ProviderId = "original"
	projected := original
	projected.Location.ProviderId = "projected"
	events := []hub.Event{{Kind: hub.EventTrackableMotion, Native: true, Payload: hub.TrackableMotionEnvelope{Motion: projected, Original: &original}}}
	if _, ok := payloadBatchForSubscription(subscription{topic: topicTrackableMotions, filter: motionFilter{CRS: "local", ProviderID: "original"}}, events, newProjectionCache(projector)); !ok {
		t.Fatal("native motion filter did not use the original observation")
	}
}
