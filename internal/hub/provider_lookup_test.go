package hub

import (
	"context"
	"testing"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/google/uuid"
)

func TestProviderLocationLookupTracksSourcesExpiryAndReplacement(t *testing.T) {
	now := time.Now()
	state := NewProcessingState(func() time.Time { return now })
	generated := now
	older := now.Add(-time.Second)
	first := gen.Location{ProviderId: "provider", Source: "first", TimestampGenerated: &older}
	second := gen.Location{ProviderId: "provider", Source: "second", TimestampGenerated: &generated}
	state.SetLatestLocation("first", first, time.Hour)
	state.SetLatestLocation("second", second, time.Second)
	if got, ok := state.GetLatestProviderLocation("provider"); !ok || got.Source != "second" {
		t.Fatal("did not select latest source")
	}
	stale := second
	stale.TimestampGenerated = &older
	if state.SetLatestLocation("second", stale, time.Hour) {
		t.Fatal("accepted an out-of-order observation")
	}
	now = now.Add(2 * time.Second)
	if got, ok := state.GetLatestProviderLocation("provider"); !ok || got.Source != "first" {
		t.Fatal("did not fall back after newest source expired")
	}
	state.SweepExpired()
	if len(state.providerLocationKeys["provider"]) != 1 {
		t.Fatal("expired source retained in provider index")
	}
	first.ProviderId = "replacement"
	state.SetLatestLocation("first", first, time.Hour)
	if _, ok := state.GetLatestProviderLocation("provider"); ok {
		t.Fatal("replaced provider retained a location")
	}
	if _, ok := state.GetLatestProviderLocation("replacement"); !ok {
		t.Fatal("replacement provider missing")
	}
	state.DeleteLatestLocation("first")
	if len(state.providerLocationKeys) != 0 {
		t.Fatal("deleted source retained in provider index")
	}
}

func TestProviderIndexCleanupPaths(t *testing.T) {
	for _, cleanup := range []string{"get", "list", "sweep", "delete_provider"} {
		t.Run(cleanup, func(t *testing.T) {
			now := time.Now()
			state := NewProcessingState(func() time.Time { return now })
			state.SetLatestLocation("key", gen.Location{ProviderId: "provider"}, time.Second)
			now = now.Add(2 * time.Second)
			switch cleanup {
			case "get":
				state.GetLatestLocation("key")
			case "list":
				state.ListLatestLocations()
			case "sweep":
				state.SweepExpired()
			case "delete_provider":
				service := &Service{state: state}
				service.deleteProviderState(context.Background(), "provider")
			}
			if len(state.providerLocationKeys) != 0 || len(state.latestLocations) != 0 {
				t.Fatal("cleanup left indexed location state behind")
			}
		})
	}
}

func TestListProviderTrackablesUsesCurrentAssignments(t *testing.T) {
	cache := &MetadataCache{snapshot: newMetadataSnapshot(nil, nil, nil, nil)}
	cache.UpsertProvider(gen.LocationProvider{Id: "provider"}, "provider")
	providers := gen.StringIdList{"provider", "provider"}
	trackable := gen.Trackable{Id: uuid.New(), LocationProviders: &providers}
	cache.UpsertTrackable(trackable, "assigned")
	service := &Service{metadata: cache}
	items, err := service.ListProviderTrackables(context.Background(), "provider")
	if err != nil || len(items) != 1 || items[0].Id != trackable.Id {
		t.Fatalf("assigned trackable missing: %v %v", items, err)
	}
	trackable.LocationProviders = nil
	cache.UpsertTrackable(trackable, "unassigned")
	items, err = service.ListProviderTrackables(context.Background(), "provider")
	if err != nil || items == nil || len(items) != 0 {
		t.Fatalf("expected an empty list after unassignment: %v %v", items, err)
	}
	cache.DeleteProvider("provider")
	if _, err := service.ListProviderTrackables(context.Background(), "provider"); err == nil {
		t.Fatal("missing provider must still return an error")
	}
}
