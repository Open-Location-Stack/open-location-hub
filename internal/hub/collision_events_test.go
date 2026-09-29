package hub

import (
	"context"
	"testing"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/google/uuid"
)

func collisionTestService(t *testing.T, now func() time.Time) (*Service, gen.TrackableMotion, gen.TrackableMotion) {
	t.Helper()
	left, right := gen.Trackable{Id: uuid.New(), Type: gen.TrackableTypeVirtual, Radius: float64Ptr(1)}, gen.Trackable{Id: uuid.New(), Type: gen.TrackableTypeVirtual, Radius: float64Ptr(1)}
	s := &Service{state: NewProcessingState(now), bus: NewEventBus(), cfg: Config{CollisionsEnabled: true, CollisionStateTTL: time.Minute}, metadata: &MetadataCache{snapshot: newMetadataSnapshot(nil, nil, []trackableRecord{{Trackable: left}, {Trackable: right}}, nil)}}
	crs := "local"
	lm := gen.TrackableMotion{Id: left.Id.String(), Location: testLocationWithCoordinates(t, &crs, "same-zone", [2]float64{0, 0})}
	rm := gen.TrackableMotion{Id: right.Id.String(), Location: testLocationWithCoordinates(t, &crs, "same-zone", [2]float64{1, 0})}
	lm.Location.ProviderId = "left-provider"
	rm.Location.ProviderId = "right-provider"
	return s, lm, rm
}

func TestCollisionLargeJumpEmitsEndAndEveryIntersectingUpdateEmitsColliding(t *testing.T) {
	s, left, right := collisionTestService(t, time.Now)
	ch, stop := s.bus.Subscribe(16)
	defer stop()
	if err := s.publishCollisionEvents(context.Background(), []gen.TrackableMotion{left, right}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.publishCollisionEvents(context.Background(), []gen.TrackableMotion{left}); err != nil {
			t.Fatal(err)
		}
	}
	_ = right.Location.Position.Coordinates.FromGeoJsonPosition2D([]float64{10000, 0})
	if err := s.publishCollisionEvents(context.Background(), []gen.TrackableMotion{right}); err != nil {
		t.Fatal(err)
	}
	events := collectEvents(ch, 4)
	if len(events) != 4 {
		t.Fatalf("expected four collision events, got %d", len(events))
	}
	for i, kind := range []gen.CollisionEventCollisionType{gen.CollisionStart, gen.Colliding, gen.Colliding, gen.CollisionEnd} {
		envelope, err := Decode[CollisionEnvelope](events[i])
		if err != nil {
			t.Fatal(err)
		}
		if envelope.Event.CollisionType != kind {
			t.Fatalf("event %d: got %s want %s", i, envelope.Event.CollisionType, kind)
		}
		leading := left.Id
		if i == 0 || i == 3 {
			leading = right.Id
		}
		if envelope.Event.Collisions[0].Id.String() != leading {
			t.Fatalf("wrong first mover in event %d", i)
		}
	}
	if pairs := s.state.collisionsForTrackable(left.Id); len(pairs) != 0 {
		t.Fatal("ended collision retained in adjacency index")
	}
}

func TestCollisionToleranceUsesMaximumAfterProviderOverrideAndExpiresWithoutMotion(t *testing.T) {
	now := time.Now()
	s, left, right := collisionTestService(t, func() time.Time { return now })
	lt, _ := s.trackableByID(context.Background(), left.Id)
	rt, _ := s.trackableByID(context.Background(), right.Id)
	lt.ExitTolerance = float64Ptr(1)
	rt.ExitTolerance = float64Ptr(1)
	lt.ToleranceTimeout = positiveOrMinusOneDuration(t, 2000)
	rt.ToleranceTimeout = positiveOrMinusOneDuration(t, 4000)
	s.metadata.UpsertTrackable(lt, "left")
	s.metadata.UpsertTrackable(rt, "right")
	s.metadata.UpsertProvider(gen.LocationProvider{Id: "left-provider", Type: "uwb", ToleranceTimeout: positiveOrMinusOneDuration(t, 1000)}, "provider")
	ch, stop := s.bus.Subscribe(8)
	defer stop()
	if err := s.publishCollisionEvents(context.Background(), []gen.TrackableMotion{left, right}); err != nil {
		t.Fatal(err)
	}
	collectEvents(ch, 1)
	_ = left.Location.Position.Coordinates.FromGeoJsonPosition2D([]float64{-2, 0})
	if err := s.publishCollisionEvents(context.Background(), []gen.TrackableMotion{left}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Second)
	s.eventScheduler().runDue()
	expectNoEvent(t, ch, 10*time.Millisecond)
	now = now.Add(time.Second)
	s.eventScheduler().runDue()
	events := collectEvents(ch, 1)
	if len(events) != 1 {
		t.Fatal("collision timeout requires another motion")
	}
	envelope, err := Decode[CollisionEnvelope](events[0])
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Event.CollisionType != gen.CollisionEnd || envelope.Event.EndTime == nil || !envelope.Event.EndTime.Equal(now) {
		t.Fatalf("invalid timeout end: %+v", envelope.Event)
	}
}

func TestCollisionReturnToIntersectionCancelsTimeout(t *testing.T) {
	now := time.Now()
	s, left, right := collisionTestService(t, func() time.Time { return now })
	lt, _ := s.trackableByID(context.Background(), left.Id)
	lt.ToleranceTimeout = disabledPositiveOrMinusOne(t)
	lt.ExitDelay = positiveOrMinusOneDuration(t, 1000)
	s.metadata.UpsertTrackable(lt, "left")
	ch, stop := s.bus.Subscribe(8)
	defer stop()
	update := func(motions ...gen.TrackableMotion) {
		t.Helper()
		if err := s.publishCollisionEvents(context.Background(), motions); err != nil {
			t.Fatal(err)
		}
	}
	update(left, right)
	collectEvents(ch, 1)
	_ = left.Location.Position.Coordinates.FromGeoJsonPosition2D([]float64{-10, 0})
	update(left)
	now = now.Add(500 * time.Millisecond)
	_ = left.Location.Position.Coordinates.FromGeoJsonPosition2D([]float64{0, 0})
	update(left)
	collectEvents(ch, 1)
	now = now.Add(2 * time.Hour)
	s.state.SweepExpired()
	s.eventScheduler().runDue()
	expectNoEvent(t, ch, 10*time.Millisecond)
	if len(s.state.collisionsForTrackable(left.Id)) != 1 {
		t.Fatal("cache TTL removed ongoing collision without a separation")
	}
}

func TestCollisionConsidersFloorHeightAndCircularExtent(t *testing.T) {
	for _, kind := range []string{"floor", "height", "square-only"} {
		t.Run(kind, func(t *testing.T) {
			s, left, right := collisionTestService(t, time.Now)
			lt, _ := s.trackableByID(context.Background(), left.Id)
			rt, _ := s.trackableByID(context.Background(), right.Id)
			switch kind {
			case "floor":
				left.Location.Floor = float64Ptr(1)
				right.Location.Floor = float64Ptr(2)
			case "height":
				lt.Extrusion = float64Ptr(1)
				rt.Extrusion = float64Ptr(1)
				_ = left.Location.Position.Coordinates.FromGeoJsonPosition3D([]float64{0, 0, 0})
				_ = right.Location.Position.Coordinates.FromGeoJsonPosition3D([]float64{0, 0, 2})
			case "square-only":
				_ = right.Location.Position.Coordinates.FromGeoJsonPosition2D([]float64{1.5, 1.5})
				left.Geometry = pointSquarePolygon(left.Location, 1)
				right.Geometry = pointSquarePolygon(right.Location, 1)
			}
			s.metadata.UpsertTrackable(lt, "left")
			s.metadata.UpsertTrackable(rt, "right")
			ch, stop := s.bus.Subscribe(4)
			defer stop()
			if err := s.publishCollisionEvents(context.Background(), []gen.TrackableMotion{left, right}); err != nil {
				t.Fatal(err)
			}
			expectNoEvent(t, ch, 10*time.Millisecond)
		})
	}
}
