package hub

import (
	"context"
	"testing"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
)

func TestDeletingFenceCancelsPendingExit(t *testing.T) {
	now := time.Now()
	s, fence, location := timerFenceService(t, func() time.Time { return now })
	ch, stop := s.bus.Subscribe(8)
	defer stop()
	if err := s.publishFenceEvents(context.Background(), location); err != nil {
		t.Fatal(err)
	}
	collectEvents(ch, 1)
	s.metadata.DeleteFence(fence.Id)
	s.clearFenceRuntime(fence.Id.String())
	now = now.Add(time.Second)
	s.eventScheduler().runDue()
	expectNoEvent(t, ch, 10*time.Millisecond)
	if s.state.IsInsideFence(providerMembershipKey(location.ProviderId), fence.Id.String()) {
		t.Fatal("deleted fence retained membership")
	}
	if err := s.transitionFenceMembership(context.Background(), providerMembershipKey(location.ProviderId), fence, location, exitInside, resolveFenceExitPolicy(fence, gen.Trackable{}, false, gen.LocationProvider{}, false)); err != nil {
		t.Fatal(err)
	}
	if s.state.IsInsideFence(providerMembershipKey(location.ProviderId), fence.Id.String()) {
		t.Fatal("queued old observation resurrected deleted fence")
	}
}

func TestClearingTrackableCancelsCollisionAndCachedSelection(t *testing.T) {
	now := time.Now()
	s, left, right := collisionTestService(t, func() time.Time { return now })
	lt, _ := s.trackableByID(context.Background(), left.Id)
	lt.ExitDelay = positiveOrMinusOneDuration(t, 1000)
	lt.ToleranceTimeout = disabledPositiveOrMinusOne(t)
	s.metadata.UpsertTrackable(lt, "left")
	ch, stop := s.bus.Subscribe(8)
	defer stop()
	if err := s.publishCollisionEvents(context.Background(), []gen.TrackableMotion{left, right}); err != nil {
		t.Fatal(err)
	}
	collectEvents(ch, 1)
	_ = left.Location.Position.Coordinates.FromGeoJsonPosition2D([]float64{1000, 0})
	if err := s.publishCollisionEvents(context.Background(), []gen.TrackableMotion{left}); err != nil {
		t.Fatal(err)
	}
	s.state.SetMotion(left.Id, left, time.Hour)
	s.state.SetTrackableLocation(latestTrackableLocationKey(left.Id), left.Location, time.Hour)
	s.clearTrackableRuntime(left.Id)
	now = now.Add(2 * time.Second)
	s.eventScheduler().runDue()
	expectNoEvent(t, ch, 10*time.Millisecond)
	if _, ok := s.state.GetMotion(left.Id); ok {
		t.Fatal("deleted trackable retained motion")
	}
	if _, ok := s.state.GetTrackableLocation(latestTrackableLocationKey(left.Id)); ok {
		t.Fatal("deleted trackable retained location")
	}
	if len(s.state.collisionsForTrackable(right.Id)) != 0 {
		t.Fatal("counterpart retained deleted collision")
	}
}
