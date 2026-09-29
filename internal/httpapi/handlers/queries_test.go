package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/google/uuid"
)

type spatialQueryStub struct {
	*fakeService
	called  string
	spatial bool
}

func (s *spatialQueryStub) ListTrackableFencesQuery(_ context.Context, _ gen.TrackableId, spatial bool) ([]gen.Fence, error) {
	s.called = "trackable-fences"
	s.spatial = spatial
	return []gen.Fence{}, nil
}
func (s *spatialQueryStub) ListProviderFencesQuery(_ context.Context, _ gen.ProviderId, spatial bool) ([]gen.Fence, error) {
	s.called = "provider-fences"
	s.spatial = spatial
	return []gen.Fence{}, nil
}
func (s *spatialQueryStub) ListFenceLocationsQuery(_ context.Context, _ gen.FenceId, spatial bool) ([]gen.Location, error) {
	s.called = "fence-locations"
	s.spatial = spatial
	return []gen.Location{}, nil
}
func (s *spatialQueryStub) ListFenceProvidersQuery(_ context.Context, _ gen.FenceId, spatial bool) ([]gen.LocationProvider, error) {
	s.called = "fence-providers"
	s.spatial = spatial
	return []gen.LocationProvider{}, nil
}

func TestSpatialQueryParametersReachService(t *testing.T) {
	for _, spatial := range []bool{false, true} {
		for _, name := range []string{"trackable-fences", "provider-fences", "fence-locations", "fence-providers"} {
			svc := &spatialQueryStub{fakeService: &fakeService{}}
			h := New(Dependencies{Service: svc})
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			id := uuid.New()
			switch name {
			case "trackable-fences":
				h.GetTrackableFences(w, r, id, gen.GetTrackableFencesParams{SpatialQuery: &spatial})
			case "provider-fences":
				h.GetProviderFences(w, r, "provider", gen.GetProviderFencesParams{SpatialQuery: &spatial})
			case "fence-locations":
				h.GetFenceLocations(w, r, id, gen.GetFenceLocationsParams{SpatialQuery: &spatial})
			case "fence-providers":
				h.GetFenceProviders(w, r, id, gen.GetFenceProvidersParams{SpatialQuery: &spatial})
			}
			if w.Code != http.StatusOK || svc.called != name || svc.spatial != spatial {
				t.Fatalf("%s spatial=%t: status=%d called=%s got=%t", name, spatial, w.Code, svc.called, svc.spatial)
			}
		}
	}
}
