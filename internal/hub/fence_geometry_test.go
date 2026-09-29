package hub

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/google/uuid"
)

func TestGeographicFenceUsesMeterRadiusAndTrackableExtent(t *testing.T) {
	fence := testPointFence(t, uuid.New(), [2]float64{13, 52}, 5)
	crs := "EPSG:4326"
	fence.Crs = &crs
	for _, tc := range []struct {
		distance, radius float64
		inside           bool
		separation       float64
	}{
		{4, 0, true, 0}, {6, 0, false, 1}, {6, 1, true, 0}, {7, 1, false, 1},
	} {
		location := testLocationWithCoordinates(t, &crs, "gps", [2]float64{13, 52 + tc.distance/metersPerLatitudeDegree})
		got, err := fenceContainmentForLocation(fence, location, tc.radius)
		if err != nil || got.Inside != tc.inside || math.Abs(got.OutsideDistance-tc.separation) > 1e-7 {
			t.Fatalf("distance=%g radius=%g: %+v, %v", tc.distance, tc.radius, got, err)
		}
	}
}

func TestFencePolygonHoleAndTouchingBoundary(t *testing.T) {
	polygon := gen.Polygon{Type: "Polygon"}
	for _, coords := range [][][2]float64{
		{{0, 0}, {10, 0}, {10, 10}, {0, 10}, {0, 0}},
		{{3, 3}, {7, 3}, {7, 7}, {3, 7}, {3, 3}},
	} {
		ring := []gen.GeoJsonPosition{}
		for _, xy := range coords {
			p := gen.GeoJsonPosition{}
			_ = p.FromGeoJsonPosition2D([]float64{xy[0], xy[1]})
			ring = append(ring, p)
		}
		polygon.Coordinates = append(polygon.Coordinates, ring)
	}
	fence := gen.Fence{Crs: stringPtrValueRef("local")}
	_ = fence.Region.FromPolygon(polygon)
	for _, tc := range []struct {
		point  [2]float64
		radius float64
		inside bool
	}{
		{[2]float64{5, 5}, 0, false}, {[2]float64{5, 5}, 2, true}, {[2]float64{3, 5}, 0, true},
		{[2]float64{-1, 5}, 1, true}, {[2]float64{1, 1}, 0, true},
	} {
		location := testLocationWithCoordinates(t, fence.Crs, "zone", tc.point)
		got, err := fenceContainmentForLocation(fence, location, tc.radius)
		if err != nil || got.Inside != tc.inside {
			t.Fatalf("point=%v radius=%g: %+v %v", tc.point, tc.radius, got, err)
		}
	}
}

func TestTrackableRadiusExpandsFenceCandidateSearch(t *testing.T) {
	s, fence, location := timerFenceService(t, time.Now)
	id := uuid.New()
	trackable := gen.Trackable{Id: id, Type: gen.TrackableTypeVirtual, Radius: float64Ptr(2)}
	s.metadata.UpsertTrackable(trackable, "trackable")
	location.Trackables = &[]string{id.String()}
	_ = location.Position.Coordinates.FromGeoJsonPosition2D([]float64{7, 0})
	fences, err := s.fenceCandidatesForLocation(context.Background(), location)
	if err != nil || len(fences) != 1 || fences[0].Id != fence.Id {
		t.Fatalf("touching extent lost in spatial index: %v %v", fences, err)
	}
	if err = s.publishFenceEvents(context.Background(), location); err != nil {
		t.Fatal(err)
	}
	if !s.state.IsInsideFence(id.String(), fence.Id.String()) {
		t.Fatal("touching trackable failed to enter")
	}
	location.Trackables = nil
	fences, err = s.fenceCandidatesForLocation(context.Background(), location)
	if err != nil || len(fences) != 0 {
		t.Fatal("a point provider outside must not acquire trackable radius")
	}
}

func TestCircularMotionGeometryOverridesMetadataPolygon(t *testing.T) {
	s, _, location := timerFenceService(t, time.Now)
	id := uuid.New()
	s.metadata.UpsertTrackable(gen.Trackable{Id: id, Type: gen.TrackableTypeVirtual, Radius: float64Ptr(2)}, "trackable")
	location.Trackables = &[]string{id.String()}
	motions, err := s.buildTrackableMotionsForLocation(context.Background(), location)
	if err != nil || len(motions) != 1 || motions[0].Geometry == nil {
		t.Fatalf("missing radius geometry: %v %v", motions, err)
	}
	ring := motions[0].Geometry.Coordinates[0]
	if len(ring) != 65 {
		t.Fatalf("unexpected approximation size: %d", len(ring))
	}
	for _, p := range ring {
		xy, e := geoPoint(p)
		if e != nil || math.Abs(math.Hypot(xy[0], xy[1])-2) > 1e-10 {
			t.Fatalf("noncircular geometry: %v", xy)
		}
	}
}

func TestFenceMembershipCrossesLocalZoneBoundaries(t *testing.T) {
	first := georeferencedZoneFixture(t, 52, 13)
	second := georeferencedZoneFixture(t, 53, 14)
	fence := testPointFence(t, uuid.New(), [2]float64{5, 5}, 2)
	fence.Crs = stringPtrValueRef("local")
	fence.ZoneId = stringPtrValueRef(first.Id.String())
	s := &Service{state: NewProcessingState(time.Now), bus: NewEventBus(), metadata: &MetadataCache{snapshot: newMetadataSnapshot([]zoneRecord{{Zone: first}, {Zone: second}}, []fenceRecord{{Fence: fence}}, nil, nil)}}
	ch, stop := s.bus.Subscribe(8)
	defer stop()
	location := testLocationWithCoordinates(t, fence.Crs, first.Id.String(), [2]float64{5, 5})
	if err := s.publishFenceEvents(context.Background(), location); err != nil {
		t.Fatal(err)
	}
	location.Source = second.Id.String()
	if err := s.publishFenceEvents(context.Background(), location); err != nil {
		t.Fatal(err)
	}
	events := collectEvents(ch, 2)
	if len(events) != 2 {
		t.Fatalf("expected entry and cross-zone exit, got %d", len(events))
	}
	for i, kind := range []gen.FenceEventEventType{gen.RegionEntry, gen.RegionExit} {
		event, err := Decode[FenceEventEnvelope](events[i])
		if err != nil {
			t.Fatal(err)
		}
		if event.Event.EventType != kind {
			t.Fatalf("event %d: %s", i, event.Event.EventType)
		}
		if event.Fence.Crs == nil || *event.Fence.Crs != "EPSG:4326" {
			t.Fatal("fence event geometry must be geographic")
		}
	}
}
