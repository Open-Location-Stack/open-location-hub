package hub

import (
	"context"
	"testing"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/google/uuid"
)

func timerFenceService(t *testing.T, now func() time.Time) (*Service, gen.Fence, gen.Location) {
	t.Helper()
	zone := testZone(t, uuid.New(), "uwb", [2]float64{0, 0}, nil, nil)
	crs := "local"
	fence := testPointFence(t, uuid.New(), [2]float64{0, 0}, 5)
	fence.Crs, fence.ZoneId = &crs, stringPtrValueRef(zone.Id.String())
	fence.Timeout = positiveOrMinusOneDuration(t, 40)
	service := &Service{
		state: NewProcessingState(now), bus: NewEventBus(),
		metadata: &MetadataCache{snapshot: newMetadataSnapshot([]zoneRecord{{Zone: zone}}, []fenceRecord{{Fence: fence}}, nil, nil)},
	}
	location := testLocationWithCoordinates(t, &crs, zone.Id.String(), [2]float64{0, 0})
	return service, fence, location
}

func TestFenceTimeoutPublishesProviderExitWithoutNewObservation(t *testing.T) {
	s, fence, location := timerFenceService(t, time.Now)
	ch, unsubscribe := s.bus.Subscribe(8)
	defer unsubscribe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.eventScheduler().run(ctx)
	if err := s.publishFenceEvents(ctx, location); err != nil {
		t.Fatal(err)
	}
	events := collectEvents(ch, 2)
	if len(events) != 2 {
		t.Fatalf("expected entry and autonomous exit, got %d", len(events))
	}
	entry, err := Decode[FenceEventEnvelope](events[0])
	if err != nil {
		t.Fatal(err)
	}
	exit, err := Decode[FenceEventEnvelope](events[1])
	if err != nil {
		t.Fatal(err)
	}
	if entry.Event.EventType != gen.RegionEntry || exit.Event.EventType != gen.RegionExit || exit.Event.TrackableId != nil || exit.Event.ProviderId == nil || *exit.Event.ProviderId != location.ProviderId {
		t.Fatalf("invalid provider event sequence: %+v / %+v", entry.Event, exit.Event)
	}
	if exit.Event.EntryTime == nil || exit.Event.ExitTime == nil || exit.Event.ExitTime.Sub(*exit.Event.EntryTime) != 40*time.Millisecond {
		t.Fatalf("exit must retain entry and scheduled expiry times: %+v", exit.Event)
	}
	if !fenceEventTime(exit.Event).Equal(*exit.Event.ExitTime) {
		t.Fatal("exit envelope uses wrong event time")
	}
	if s.state.IsInsideFence(providerMembershipKey(location.ProviderId), fence.Id.String()) {
		t.Fatal("expired membership retained")
	}
}

func TestFenceReturnInsideCancelsExitAndRefreshesFenceTimeout(t *testing.T) {
	now := time.Now()
	s, fence, location := timerFenceService(t, func() time.Time { return now })
	fence.Timeout = positiveOrMinusOneDuration(t, 10000)
	fence.ExitDelay = positiveOrMinusOneDuration(t, 1000)
	fence.ToleranceTimeout = disabledPositiveOrMinusOne(t)
	location.Trackables = &[]string{"trackable-a"}
	ch, unsubscribe := s.bus.Subscribe(8)
	defer unsubscribe()
	policy := resolveFenceExitPolicy(fence, gen.Trackable{}, false, gen.LocationProvider{}, false)
	update := func(area exitArea) {
		t.Helper()
		if err := s.transitionFenceMembership(context.Background(), "trackable-a", fence, location, area, policy); err != nil {
			t.Fatal(err)
		}
	}
	update(exitInside)
	collectEvents(ch, 1)
	now = now.Add(time.Second)
	update(exitOutside)
	now = now.Add(500 * time.Millisecond)
	update(exitInside)
	now = now.Add(time.Second)
	s.eventScheduler().runDue()
	expectNoEvent(t, ch, 10*time.Millisecond)
	if !s.state.IsInsideFence("trackable-a", fence.Id.String()) {
		t.Fatal("return inside failed to cancel imminent exit")
	}
	now = now.Add(9 * time.Second)
	s.eventScheduler().runDue()
	events := collectEvents(ch, 1)
	if len(events) != 1 {
		t.Fatal("refreshed fence timeout did not expire")
	}
	exit, _ := Decode[FenceEventEnvelope](events[0])
	if exit.Event.EventType != gen.RegionExit || exit.Event.TrackableId == nil {
		t.Fatalf("invalid exit: %+v", exit.Event)
	}
}

func TestFenceToleranceDefaultInheritsFenceTimeout(t *testing.T) {
	for _, timeout := range []*gen.PositiveOrMinusOne{nil, positiveOrMinusOneDuration(t, 0)} {
		fence := gen.Fence{ExitTolerance: float64Ptr(1), ToleranceTimeout: timeout, Timeout: positiveOrMinusOneDuration(t, 3000)}
		policy := resolveFenceExitPolicy(fence, gen.Trackable{}, false, gen.LocationProvider{}, false)
		if policy.ToleranceTimeout != 3*time.Second {
			t.Fatalf("expected fence timeout fallback: %+v", policy)
		}
	}
}
