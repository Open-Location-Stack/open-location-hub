package hub

import (
	"context"
	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestLocationDefaultsPreserveAndInherit(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	s := &Service{now: func() time.Time { return now }}
	loc := s.applyLocationDefaults(context.Background(), gen.Location{})
	if loc.TimestampGenerated == nil || !loc.TimestampGenerated.Equal(now) || loc.Floor == nil || *loc.Floor != 0 || loc.ElevationRef == nil || *loc.ElevationRef != "floor" || loc.Crs == nil || *loc.Crs != "local" {
		t.Fatalf("missing defaults: %+v", loc)
	}
	explicit := 9.0
	loc.Floor = &explicit
	loc.TimestampGenerated = &now
	after := s.applyLocationDefaults(context.Background(), loc)
	if *after.Floor != explicit || !after.TimestampGenerated.Equal(now) {
		t.Fatal("explicit metadata overwritten")
	}
}

func TestLocationFloorInheritedThroughForeignZoneID(t *testing.T) {
	zone := testZoneWithForeignID(t, uuid.New(), "uwb", "foreign-floor", [2]float64{13, 52}, nil, nil)
	floor := 4.0
	zone.Floor = &floor
	metadata, err := NewMetadataCache(context.Background(), fakeQueries{listZonesFn: metadataZoneList(t, zone), listFencesFn: metadataFenceList(t), listTrackablesFn: metadataTrackableList(t), listProvidersFn: metadataProviderList(t)})
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{metadata: metadata}
	result := s.applyLocationDefaults(context.Background(), gen.Location{Source: "foreign-floor"})
	if result.Floor == nil || *result.Floor != 4 {
		t.Fatal("zone floor not inherited")
	}
}
