package hub

import (
	"context"
	"testing"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/google/uuid"
)

func TestSpatialQueriesDistinguishGeometryFromTimedMembership(t *testing.T) {
	s, fence, location := timerFenceService(t, time.Now)
	fence.Timeout = nil
	fence.ExitTolerance = float64Ptr(1)
	fence.ToleranceTimeout = disabledPositiveOrMinusOne(t)
	s.metadata.UpsertFence(fence, "fence")
	provider := gen.LocationProvider{Id: location.ProviderId, Type: "uwb"}
	s.metadata.UpsertProvider(provider, "provider")
	id := uuid.New()
	trackable := gen.Trackable{Id: id, Type: gen.TrackableTypeVirtual}
	s.metadata.UpsertTrackable(trackable, "trackable")
	ctx := context.Background()
	update := func(x float64) {
		t.Helper()
		_ = location.Position.Coordinates.FromGeoJsonPosition2D([]float64{x, 0})
		s.state.SetLatestLocation(latestLocationKey(location.ProviderId, location.Source), location, time.Minute)
		if err := s.publishFenceObservation(ctx, location, false); err != nil {
			t.Fatal(err)
		}
		selected := location
		selected.Trackables = &[]string{id.String()}
		s.state.SetTrackableLocation(latestTrackableLocationKey(id.String()), selected, time.Minute)
		if err := s.publishFenceEvents(ctx, selected); err != nil {
			t.Fatal(err)
		}
	}
	update(0)
	update(5.5)
	for _, spatial := range []bool{false, true} {
		want := 1
		if spatial {
			want = 0
		}
		locations, err := s.ListFenceLocationsQuery(ctx, fence.Id, spatial)
		if err != nil || len(locations) != want {
			t.Fatalf("locations spatial=%t: %d %v", spatial, len(locations), err)
		}
		providers, err := s.ListFenceProvidersQuery(ctx, fence.Id, spatial)
		if err != nil || len(providers) != want {
			t.Fatalf("providers spatial=%t: %d %v", spatial, len(providers), err)
		}
		trackables, err := s.ListFenceTrackables(ctx, fence.Id, spatial)
		if err != nil || len(trackables) != want {
			t.Fatalf("trackables spatial=%t: %d %v", spatial, len(trackables), err)
		}
		fences, err := s.ListTrackableFencesQuery(ctx, id, spatial)
		if err != nil || len(fences) != want {
			t.Fatalf("trackable fences spatial=%t: %d %v", spatial, len(fences), err)
		}
		fences, err = s.ListProviderFencesQuery(ctx, provider.Id, spatial)
		if err != nil || len(fences) != want {
			t.Fatalf("provider fences spatial=%t: %d %v", spatial, len(fences), err)
		}
	}
}
