package hub

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/formation-res/open-rtls-hub/internal/storage/postgres/sqlcgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestNewMetadataCacheBuildsIndexes(t *testing.T) {
	t.Parallel()

	zone := testZoneWithForeignID(t, uuid.New(), "uwb", "foreign-zone", [2]float32{1, 2}, nil, nil)
	trackable := gen.Trackable{Id: uuidAsOpenAPI(uuid.New()), Type: gen.TrackableTypeOmlox}
	provider := gen.LocationProvider{Id: "provider-a", Type: "uwb"}
	fence := testPointFence(t, uuid.New(), [2]float32{1, 2}, 5)

	cache, err := NewMetadataCache(context.Background(), fakeQueries{
		listZonesFn:      metadataZoneList(t, zone),
		listTrackablesFn: metadataTrackableList(t, trackable),
		listProvidersFn:  metadataProviderList(t, provider),
		listFencesFn:     metadataFenceList(t, fence),
	})
	if err != nil {
		t.Fatalf("NewMetadataCache failed: %v", err)
	}

	if _, ok := cache.ZoneBySource(zone.Id.String()); !ok {
		t.Fatal("expected zone lookup by id")
	}
	if _, ok := cache.ZoneBySource("foreign-zone"); !ok {
		t.Fatal("expected zone lookup by foreign id")
	}
	if _, ok := cache.TrackableByID(trackable.Id.String()); !ok {
		t.Fatal("expected trackable lookup by id")
	}
	if _, ok := cache.ProviderByID(provider.Id); !ok {
		t.Fatal("expected provider lookup by id")
	}
	if _, ok := cache.FenceByID(fence.Id.String()); !ok {
		t.Fatal("expected fence lookup by id")
	}
}

func TestMetadataCacheReconcileDiffsCreateUpdateDelete(t *testing.T) {
	t.Parallel()

	zoneA := testZone(t, uuid.New(), "uwb", [2]float32{1, 2}, nil, nil)
	zoneB := testZone(t, uuid.New(), "uwb", [2]float32{3, 4}, nil, nil)
	updated := zoneA
	updated.Name = stringPtrValueRef("updated")

	cache, err := NewMetadataCache(context.Background(), fakeQueries{
		listZonesFn:      metadataZoneList(t, zoneA),
		listFencesFn:     metadataFenceList(t),
		listTrackablesFn: metadataTrackableList(t),
		listProvidersFn:  metadataProviderList(t),
	})
	if err != nil {
		t.Fatalf("NewMetadataCache failed: %v", err)
	}
	cache.queries = fakeQueries{
		listZonesFn:      metadataZoneList(t, updated, zoneB),
		listFencesFn:     metadataFenceList(t),
		listTrackablesFn: metadataTrackableList(t),
		listProvidersFn:  metadataProviderList(t),
	}

	changes, err := cache.Reconcile(context.Background(), time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	if len(changes) != 2 {
		t.Fatalf("expected 2 reconcile changes, got %d", len(changes))
	}
	foundCreate := false
	foundUpdate := false
	for _, change := range changes {
		if change.ID == zoneB.Id.String() && change.Operation == metadataOperationCreate {
			foundCreate = true
		}
		if change.ID == zoneA.Id.String() && change.Operation == metadataOperationUpdate {
			foundUpdate = true
		}
	}
	if !foundCreate || !foundUpdate {
		t.Fatalf("unexpected reconcile changes: %+v", changes)
	}
}

func TestServiceWarmMetadataRemovesHotPathQueries(t *testing.T) {
	t.Parallel()

	zone := georeferencedZoneFixture(t, 47.3744, 8.5411)
	fence := testPointFence(t, uuid.New(), [2]float32{5, 7}, 2)
	trackable := gen.Trackable{Id: uuidAsOpenAPI(uuid.New()), Type: gen.TrackableTypeOmlox}
	trackableIDs := []string{trackable.Id.String()}
	location := testLocationWithCoordinates(t, stringPtrValueRef("local"), zone.Id.String(), [2]float32{5, 7})
	location.Trackables = &trackableIDs

	var listZoneCalls, listFenceCalls, listTrackableCalls int
	queries := fakeQueries{
		listZonesFn: func(context.Context) ([]sqlcgen.Zone, error) {
			listZoneCalls++
			return mustZoneRows(t, zone), nil
		},
		listFencesFn: func(context.Context) ([]sqlcgen.Fence, error) {
			listFenceCalls++
			return mustFenceRows(t, fence), nil
		},
		listTrackablesFn: func(context.Context) ([]sqlcgen.Trackable, error) {
			listTrackableCalls++
			return mustTrackableRows(t, trackable), nil
		},
	}

	service, err := New(zapTestLogger(t), queries, NewEventBus(), Config{
		LocationTTL:                time.Minute,
		ProximityTTL:               time.Minute,
		DedupTTL:                   time.Minute,
		MetadataReconcileInterval:  time.Minute,
		CollisionsEnabled:          true,
		CollisionStateTTL:          time.Minute,
		CollisionCollidingDebounce: time.Second,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	listZoneCalls = 0
	listFenceCalls = 0
	listTrackableCalls = 0

	if err := service.ProcessLocations(context.Background(), []gen.Location{location}); err != nil {
		t.Fatalf("ProcessLocations failed: %v", err)
	}

	if listZoneCalls != 0 || listFenceCalls != 0 || listTrackableCalls != 0 {
		t.Fatalf("expected hot path to avoid metadata queries, got zones=%d fences=%d trackables=%d", listZoneCalls, listFenceCalls, listTrackableCalls)
	}
}

func TestProcessingStateSweepExpiresEntries(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)
	state := NewProcessingState(func() time.Time { return now })
	state.SetCollisionState("pair", activeCollisionState{Active: true}, time.Second)
	state.SetInsideFence("trackable-a", "fence-a", time.Second)
	state.SetProximityState("provider-a", proximityResolutionState{ResolvedZoneID: "zone-a"}, time.Second)
	state.SetMotion("trackable-a", gen.TrackableMotion{Id: "trackable-a"}, time.Second)
	state.Deduplicate("dedup", time.Second)

	now = now.Add(2 * time.Second)
	state.SweepExpired()

	if _, ok := state.GetCollisionState("pair"); ok {
		t.Fatal("expected collision state to expire")
	}
	if state.IsInsideFence("trackable-a", "fence-a") {
		t.Fatal("expected fence membership to expire")
	}
	if _, ok := state.GetProximityState("provider-a"); ok {
		t.Fatal("expected proximity state to expire")
	}
	if _, ok := state.GetMotion("trackable-a"); ok {
		t.Fatal("expected motion state to expire")
	}
	if !state.Deduplicate("dedup", time.Second) {
		t.Fatal("expected dedup state to expire")
	}
}

func TestZoneCRUDEmitsMetadataChanges(t *testing.T) {
	t.Parallel()

	bus := NewEventBus()
	ch, unsubscribe := bus.Subscribe(8)
	defer unsubscribe()
	zone := georeferencedZoneFixture(t, 47.3744, 8.5411)
	updated := zone
	updated.Description = stringPtrValueRef("updated")

	service, err := New(zapTestLogger(t), fakeQueries{
		listZonesFn:      metadataZoneList(t),
		listFencesFn:     metadataFenceList(t),
		listTrackablesFn: metadataTrackableList(t),
		listProvidersFn:  metadataProviderList(t),
		createZoneFn: func(context.Context, sqlcgen.CreateZoneParams) (sqlcgen.Zone, error) {
			payload, _ := json.Marshal(zone)
			return sqlcgen.Zone{Payload: payload}, nil
		},
		updateZoneFn: func(context.Context, sqlcgen.UpdateZoneParams) (sqlcgen.Zone, error) {
			payload, _ := json.Marshal(updated)
			return sqlcgen.Zone{Payload: payload}, nil
		},
		deleteZoneFn: func(context.Context, pgtype.UUID) (int64, error) {
			return 1, nil
		},
	}, bus, Config{
		LocationTTL:               time.Minute,
		ProximityTTL:              time.Minute,
		DedupTTL:                  time.Minute,
		MetadataReconcileInterval: time.Minute,
		CollisionStateTTL:         time.Minute,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	createBody, _ := json.Marshal(zone)
	updateBody, _ := json.Marshal(updated)
	if _, err := service.CreateZone(context.Background(), createBody); err != nil {
		t.Fatalf("CreateZone failed: %v", err)
	}
	if _, err := service.UpdateZone(context.Background(), zone.Id, updateBody); err != nil {
		t.Fatalf("UpdateZone failed: %v", err)
	}
	if err := service.DeleteZone(context.Background(), zone.Id); err != nil {
		t.Fatalf("DeleteZone failed: %v", err)
	}

	events := collectEvents(ch, 3)
	if len(events) != 3 {
		t.Fatalf("expected 3 metadata events, got %d", len(events))
	}
	ops := []string{}
	for _, event := range events {
		change, err := Decode[MetadataChange](event)
		if err != nil {
			t.Fatalf("decode metadata event failed: %v", err)
		}
		ops = append(ops, change.Operation)
	}
	if ops[0] != metadataOperationCreate || ops[1] != metadataOperationUpdate || ops[2] != metadataOperationDelete {
		t.Fatalf("unexpected operations: %+v", ops)
	}
}

func metadataZoneList(t *testing.T, zones ...gen.Zone) func(context.Context) ([]sqlcgen.Zone, error) {
	t.Helper()
	return func(context.Context) ([]sqlcgen.Zone, error) {
		return mustZoneRows(t, zones...), nil
	}
}

func metadataFenceList(t *testing.T, fences ...gen.Fence) func(context.Context) ([]sqlcgen.Fence, error) {
	t.Helper()
	return func(context.Context) ([]sqlcgen.Fence, error) {
		return mustFenceRows(t, fences...), nil
	}
}

func metadataTrackableList(t *testing.T, items ...gen.Trackable) func(context.Context) ([]sqlcgen.Trackable, error) {
	t.Helper()
	return func(context.Context) ([]sqlcgen.Trackable, error) {
		return mustTrackableRows(t, items...), nil
	}
}

func metadataProviderList(t *testing.T, items ...gen.LocationProvider) func(context.Context) ([]sqlcgen.Provider, error) {
	t.Helper()
	return func(context.Context) ([]sqlcgen.Provider, error) {
		return mustProviderRows(t, items...), nil
	}
}

func mustZoneRows(t *testing.T, zones ...gen.Zone) []sqlcgen.Zone {
	t.Helper()
	rows := make([]sqlcgen.Zone, 0, len(zones))
	for _, item := range zones {
		payload, err := json.Marshal(item)
		if err != nil {
			t.Fatalf("marshal zone failed: %v", err)
		}
		rows = append(rows, sqlcgen.Zone{Payload: payload})
	}
	return rows
}

func mustFenceRows(t *testing.T, fences ...gen.Fence) []sqlcgen.Fence {
	t.Helper()
	rows := make([]sqlcgen.Fence, 0, len(fences))
	for _, item := range fences {
		payload, err := json.Marshal(item)
		if err != nil {
			t.Fatalf("marshal fence failed: %v", err)
		}
		rows = append(rows, sqlcgen.Fence{Payload: payload})
	}
	return rows
}

func mustTrackableRows(t *testing.T, items ...gen.Trackable) []sqlcgen.Trackable {
	t.Helper()
	rows := make([]sqlcgen.Trackable, 0, len(items))
	for _, item := range items {
		payload, err := json.Marshal(item)
		if err != nil {
			t.Fatalf("marshal trackable failed: %v", err)
		}
		rows = append(rows, sqlcgen.Trackable{Payload: payload})
	}
	return rows
}

func mustProviderRows(t *testing.T, items ...gen.LocationProvider) []sqlcgen.Provider {
	t.Helper()
	rows := make([]sqlcgen.Provider, 0, len(items))
	for _, item := range items {
		payload, err := json.Marshal(item)
		if err != nil {
			t.Fatalf("marshal provider failed: %v", err)
		}
		rows = append(rows, sqlcgen.Provider{Payload: payload})
	}
	return rows
}

func stringPtrValueRef(value string) *string {
	return &value
}
