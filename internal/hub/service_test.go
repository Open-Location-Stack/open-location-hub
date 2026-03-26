package hub

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/formation-res/open-rtls-hub/internal/storage/postgres/sqlcgen"
	"github.com/formation-res/open-rtls-hub/internal/transform"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"
)

func TestNormalizeZoneRequiresGroundControlPointsWhenConfigurationIsComplete(t *testing.T) {
	_, _, err := normalizeZone(json.RawMessage(`{"type":"uwb","name":"zone-a"}`), [16]byte{})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestNormalizeFenceRequiresZoneIDForLocalCRS(t *testing.T) {
	_, _, err := normalizeFence(json.RawMessage(`{"crs":"local","region":{"type":"Point","coordinates":[1,2]}}`), [16]byte{})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestFenceContainsPointForPointFence(t *testing.T) {
	var region gen.Fence_Region
	point := gen.Point{Type: "Point"}
	if err := point.Coordinates.FromGeoJsonPosition2D([]float32{10, 20}); err != nil {
		t.Fatalf("coordinates setup failed: %v", err)
	}
	if err := region.FromPoint(point); err != nil {
		t.Fatalf("region setup failed: %v", err)
	}
	radius := float32(5)
	inside, err := fenceContainsPoint(gen.Fence{Region: region, Radius: &radius}, [2]float64{12, 23})
	if err != nil {
		t.Fatalf("contains check failed: %v", err)
	}
	if !inside {
		t.Fatal("expected point to be inside the fence radius")
	}
}

func TestNormalizeZoneRejectsInvalidProximityResolutionProperties(t *testing.T) {
	_, _, err := normalizeZone(json.RawMessage(`{
		"type":"rfid",
		"position":{"type":"Point","coordinates":[1,2]},
		"properties":{"proximity_resolution":{"exit_grace_duration":true}}
	}`), [16]byte{})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestResolveProximityCandidateRejectsUnknownZone(t *testing.T) {
	_, err := resolveProximityCandidate(gen.Proximity{
		ProviderId:   "provider-a",
		ProviderType: "rfid",
		Source:       "missing-zone",
	}, []gen.Zone{testZone(t, uuid.New(), "rfid", [2]float32{1, 2}, nil, nil)}, testPolicy())
	if err == nil {
		t.Fatal("expected missing zone error")
	}
}

func TestResolveProximityCandidateRequiresPosition(t *testing.T) {
	zone := gen.Zone{Id: uuidAsOpenAPI(uuid.New()), Type: "rfid"}
	_, err := resolveProximityCandidate(gen.Proximity{
		ProviderId:   "provider-a",
		ProviderType: "rfid",
		Source:       zone.Id.String(),
	}, []gen.Zone{zone}, testPolicy())
	if err == nil {
		t.Fatal("expected position validation error")
	}
}

func TestResolveProximityCandidateRejectsNonProximityZoneType(t *testing.T) {
	_, err := resolveProximityCandidate(gen.Proximity{
		ProviderId:   "provider-a",
		ProviderType: "uwb",
		Source:       "zone-a",
	}, []gen.Zone{testZoneWithForeignID(t, uuid.New(), "uwb", "zone-a", [2]float32{1, 2}, nil, nil)}, testPolicy())
	if err == nil {
		t.Fatal("expected proximity zone type validation error")
	}
}

func TestResolveProximitySticksWithinBoundaryGrace(t *testing.T) {
	now := time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC)
	currentZone := testZone(t, uuid.New(), "rfid", [2]float32{0, 0}, float32Ptr(3), nil)
	candidateZone := testZone(t, uuid.New(), "ibeacon", [2]float32{5, 0}, float32Ptr(3), nil)
	candidate, err := resolveProximityCandidate(testProximity(candidateZone.Id.String()), []gen.Zone{candidateZone}, testPolicy())
	if err != nil {
		t.Fatalf("candidate resolution failed: %v", err)
	}
	state := proximityResolutionState{
		ResolvedZoneID:  currentZone.Id.String(),
		EnteredAt:       now.Add(-30 * time.Second),
		LastConfirmedAt: now.Add(-4 * time.Second),
		LastEmittedAt:   now.Add(-4 * time.Second),
	}
	resolution, _, err := resolveProximity(testProximity(candidateZone.Id.String()), candidate, &currentZone, state, testPolicy(), now)
	if err != nil {
		t.Fatalf("proximity resolution failed: %v", err)
	}
	if resolution.Zone.Id != currentZone.Id {
		t.Fatal("expected resolver to keep current zone")
	}
	if !resolution.Sticky {
		t.Fatal("expected sticky resolution")
	}
}

func TestResolveProximitySwitchesAfterGraceExpires(t *testing.T) {
	now := time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC)
	currentZone := testZone(t, uuid.New(), "rfid", [2]float32{0, 0}, float32Ptr(3), nil)
	candidateZone := testZone(t, uuid.New(), "ibeacon", [2]float32{5, 0}, float32Ptr(3), nil)
	candidate, err := resolveProximityCandidate(testProximity(candidateZone.Id.String()), []gen.Zone{candidateZone}, testPolicy())
	if err != nil {
		t.Fatalf("candidate resolution failed: %v", err)
	}
	state := proximityResolutionState{
		ResolvedZoneID:  currentZone.Id.String(),
		EnteredAt:       now.Add(-30 * time.Second),
		LastConfirmedAt: now.Add(-20 * time.Second),
		LastEmittedAt:   now.Add(-20 * time.Second),
	}
	resolution, _, err := resolveProximity(testProximity(candidateZone.Id.String()), candidate, &currentZone, state, testPolicy(), now)
	if err != nil {
		t.Fatalf("proximity resolution failed: %v", err)
	}
	if resolution.Zone.Id != candidateZone.Id {
		t.Fatal("expected resolver to switch to candidate zone")
	}
	if resolution.Sticky {
		t.Fatal("expected switch to clear sticky state")
	}
}

func TestProximityPolicyForZoneAppliesOverrides(t *testing.T) {
	defaults := testPolicy()
	defaults.ExitGraceDuration = 10 * time.Second
	props := gen.ExtensionProperties{
		"proximity_resolution": map[string]any{
			"exit_grace_duration": "30s",
		},
	}
	zone := testZone(t, uuid.New(), "rfid", [2]float32{0, 0}, float32Ptr(2), &props)
	policy, err := proximityPolicyForZone(zone, defaults)
	if err != nil {
		t.Fatalf("policy parse failed: %v", err)
	}
	if policy.ExitGraceDuration != 30*time.Second {
		t.Fatalf("expected override to win, got %s", policy.ExitGraceDuration)
	}
}

func TestDeriveLocationFromProximityAddsResolutionMetadata(t *testing.T) {
	zone := testZone(t, uuid.New(), "rfid", [2]float32{1, 2}, nil, nil)
	props := gen.ExtensionProperties{"raw": "value"}
	location := deriveLocationFromProximity(gen.Proximity{
		ProviderId:   "provider-a",
		ProviderType: "rfid",
		Source:       zone.Id.String(),
		Properties:   &props,
	}, zone, true)
	if location.Properties == nil {
		t.Fatal("expected location properties")
	}
	if (*location.Properties)["resolved_zone_id"] != zone.Id.String() {
		t.Fatal("expected resolved zone metadata")
	}
	if (*location.Properties)["sticky"] != true {
		t.Fatal("expected sticky metadata")
	}
	if (*location.Properties)["raw"] != "value" {
		t.Fatal("expected original proximity properties to be preserved")
	}
	if location.Crs == nil || *location.Crs != "local" {
		t.Fatal("expected proximity-derived location to remain in local CRS")
	}
}

func TestValidateLocationAllowsOmittedCRS(t *testing.T) {
	if err := validateLocation(testLocation(t, nil)); err != nil {
		t.Fatalf("expected omitted CRS to pass, got %v", err)
	}
}

func TestValidateLocationAllowsLocalCRS(t *testing.T) {
	crs := "local"
	if err := validateLocation(testLocation(t, &crs)); err != nil {
		t.Fatalf("expected local CRS to pass, got %v", err)
	}
}

func TestValidateLocationAllowsEPSG4326CRS(t *testing.T) {
	crs := "EPSG:4326"
	if err := validateLocation(testLocation(t, &crs)); err != nil {
		t.Fatalf("expected EPSG:4326 CRS to pass, got %v", err)
	}
}

func TestValidateLocationRejectsUnsupportedCRS(t *testing.T) {
	crs := "WGS84"
	err := validateLocation(testLocation(t, &crs))
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != 400 {
		t.Fatalf("expected bad request for unsupported CRS, got %v", err)
	}
}

func TestRecordLocationDeduplicatesAndStoresLatestStateWithTTL(t *testing.T) {
	cache := newMemoryCache()
	service := &Service{
		cache: cache,
		cfg: Config{
			DedupTTL:    2 * time.Minute,
			LocationTTL: 10 * time.Minute,
		},
	}
	location := testLocation(t, nil)
	ttl := 30 * time.Second

	if err := service.recordLocation(context.Background(), location, ttl); err != nil {
		t.Fatalf("recordLocation failed: %v", err)
	}
	if err := service.recordLocation(context.Background(), location, ttl); err != nil {
		t.Fatalf("second recordLocation failed: %v", err)
	}

	if len(cache.setNXCalls) != 2 {
		t.Fatalf("expected two dedup attempts, got %d", len(cache.setNXCalls))
	}
	if len(cache.setCalls) != 1 {
		t.Fatalf("expected only one latest-state write after dedup, got %d", len(cache.setCalls))
	}
	if cache.setCalls[0].key != latestLocationKey(location.ProviderId, location.Source) {
		t.Fatalf("unexpected latest-location key: %s", cache.setCalls[0].key)
	}
	if cache.setCalls[0].ttl != ttl {
		t.Fatalf("expected latest-location TTL %s, got %s", ttl, cache.setCalls[0].ttl)
	}
	if cache.setNXCalls[0].ttl != service.cfg.DedupTTL {
		t.Fatalf("expected dedup TTL %s, got %s", service.cfg.DedupTTL, cache.setNXCalls[0].ttl)
	}
}

func TestRecordLocationStoresTrackableLatestStateWithTTL(t *testing.T) {
	cache := newMemoryCache()
	service := &Service{
		cache: cache,
		cfg: Config{
			DedupTTL:    2 * time.Minute,
			LocationTTL: 10 * time.Minute,
		},
	}
	location := testLocation(t, nil)
	trackables := []string{"trackable-a", "trackable-b"}
	location.Trackables = &trackables
	ttl := 45 * time.Second

	if err := service.recordLocation(context.Background(), location, ttl); err != nil {
		t.Fatalf("recordLocation failed: %v", err)
	}

	if len(cache.setCalls) != 3 {
		t.Fatalf("expected latest location plus two trackable writes, got %d", len(cache.setCalls))
	}
	if cache.setCalls[1].key != latestTrackableLocationKey("trackable-a") {
		t.Fatalf("unexpected first trackable key: %s", cache.setCalls[1].key)
	}
	if cache.setCalls[2].key != latestTrackableLocationKey("trackable-b") {
		t.Fatalf("unexpected second trackable key: %s", cache.setCalls[2].key)
	}
	if cache.setCalls[1].ttl != ttl || cache.setCalls[2].ttl != ttl {
		t.Fatalf("expected trackable TTL %s, got %s and %s", ttl, cache.setCalls[1].ttl, cache.setCalls[2].ttl)
	}
}

func TestProcessProximitiesReEntersAfterStaleStateExpiry(t *testing.T) {
	now := time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC)
	cache := newMemoryCache()
	zone := testZone(t, uuid.New(), "rfid", [2]float32{1, 2}, float32Ptr(2), nil)
	proximity := testProximity(zone.Id.String())
	service := &Service{
		cache: cache,
		queries: fakeQueries{
			listZonesFn: func(context.Context) ([]sqlcgen.Zone, error) {
				payload, err := json.Marshal(zone)
				if err != nil {
					t.Fatalf("marshal zone failed: %v", err)
				}
				return []sqlcgen.Zone{{Payload: payload}}, nil
			},
		},
		cfg: Config{
			DedupTTL:                              2 * time.Minute,
			ProximityTTL:                          5 * time.Minute,
			ProximityResolutionExitGraceDuration:  15 * time.Second,
			ProximityResolutionBoundaryGrace:      2,
			ProximityResolutionMinDwellDuration:   5 * time.Second,
			ProximityResolutionPositionMode:       "zone_position",
			ProximityResolutionStaleStateTTL:      10 * time.Minute,
			ProximityResolutionFallbackRadius:     0,
			ProximityResolutionEntryConfidenceMin: 0,
		},
		now: func() time.Time { return now },
	}
	cache.values[proximityResolutionStateKey(proximity.ProviderType, proximity.ProviderId)] = mustJSON(t, proximityResolutionState{
		ResolvedZoneID:  "expired-zone",
		EnteredAt:       now.Add(-30 * time.Minute),
		LastConfirmedAt: now.Add(-20 * time.Minute),
		LastEmittedAt:   now.Add(-20 * time.Minute),
	})

	if err := service.ProcessProximities(context.Background(), []gen.Proximity{proximity}); err != nil {
		t.Fatalf("ProcessProximities failed: %v", err)
	}

	statePayload := cache.values[proximityResolutionStateKey(proximity.ProviderType, proximity.ProviderId)]
	var state proximityResolutionState
	if err := json.Unmarshal(statePayload, &state); err != nil {
		t.Fatalf("unmarshal state failed: %v", err)
	}
	if state.ResolvedZoneID != proximity.Source {
		t.Fatalf("expected re-entry into candidate zone %s, got %s", proximity.Source, state.ResolvedZoneID)
	}
	if !state.EnteredAt.Equal(now) {
		t.Fatalf("expected re-entry timestamp %s, got %s", now, state.EnteredAt)
	}
}

func TestPublishLocationTransformsLocalToWGS84(t *testing.T) {
	bus := NewEventBus()
	ch, unsubscribe := bus.Subscribe(8)
	defer unsubscribe()
	zone := georeferencedZoneFixture(t, 47.3744, 8.5411)
	service := &Service{
		bus:            bus,
		queries:        fakeQueries{listZonesFn: zoneListQuery(t, zone)},
		crsTransformer: transform.NewCRSTransformer(),
		transformCache: transform.NewCache(),
		logger:         zapTestLogger(t),
	}
	crs := "local"
	location := testLocationWithCoordinates(t, &crs, zone.Id.String(), [2]float32{5, 7})

	if err := service.publishLocation(context.Background(), location); err != nil {
		t.Fatalf("publishLocation failed: %v", err)
	}
	events := collectEvents(ch, 2)
	localPublished := decodeEventLocation(t, eventByScope(t, events, ScopeLocal))
	wgsPublished := decodeEventLocation(t, eventByScope(t, events, ScopeEPSG4326))
	if localPublished.Crs == nil || *localPublished.Crs != "local" {
		t.Fatal("expected local publication to stay local")
	}
	if wgsPublished.Crs == nil || *wgsPublished.Crs != "EPSG:4326" {
		t.Fatal("expected wgs84 publication to use EPSG:4326")
	}
	assertLocationsDiffer(t, localPublished, wgsPublished)
}

func TestPublishLocationTransformsWGS84ToLocalWhenZoneIsGeoreferenced(t *testing.T) {
	bus := NewEventBus()
	ch, unsubscribe := bus.Subscribe(8)
	defer unsubscribe()
	zone := georeferencedZoneFixture(t, -33.4489, -70.6693)
	service := &Service{
		bus:            bus,
		queries:        fakeQueries{listZonesFn: zoneListQuery(t, zone)},
		crsTransformer: transform.NewCRSTransformer(),
		transformCache: transform.NewCache(),
		logger:         zapTestLogger(t),
	}
	crs := "EPSG:4326"
	location := testLocationWithCoordinates(t, &crs, zone.Id.String(), [2]float32{-70.6692, -33.4488})

	if err := service.publishLocation(context.Background(), location); err != nil {
		t.Fatalf("publishLocation failed: %v", err)
	}
	events := collectEvents(ch, 2)
	localPublished := decodeEventLocation(t, eventByScope(t, events, ScopeLocal))
	wgsPublished := decodeEventLocation(t, eventByScope(t, events, ScopeEPSG4326))
	if localPublished.Crs == nil || *localPublished.Crs != "local" {
		t.Fatal("expected local publication to use local CRS")
	}
	if wgsPublished.Crs == nil || *wgsPublished.Crs != "EPSG:4326" {
		t.Fatal("expected wgs84 publication to use EPSG:4326")
	}
	assertLocationsDiffer(t, localPublished, wgsPublished)
}

func TestPublishLocationSkipsUnavailableDerivedVariant(t *testing.T) {
	bus := NewEventBus()
	ch, unsubscribe := bus.Subscribe(8)
	defer unsubscribe()
	crs := "local"
	location := testLocationWithCoordinates(t, &crs, "missing-zone", [2]float32{2, 3})
	service := &Service{
		bus:            bus,
		queries:        fakeQueries{listZonesFn: func(context.Context) ([]sqlcgen.Zone, error) { return nil, nil }},
		crsTransformer: transform.NewCRSTransformer(),
		transformCache: transform.NewCache(),
		logger:         zapTestLogger(t),
	}

	if err := service.publishLocation(context.Background(), location); err != nil {
		t.Fatalf("publishLocation failed: %v", err)
	}
	events := collectEvents(ch, 1)
	published := decodeEventLocation(t, events[0])
	if published.Crs == nil || *published.Crs != "local" {
		t.Fatal("expected only local topic to be published")
	}
}

func TestPublishTrackableMotionsUsesTransformedVariants(t *testing.T) {
	bus := NewEventBus()
	ch, unsubscribe := bus.Subscribe(8)
	defer unsubscribe()
	zone := georeferencedZoneFixture(t, 47.3744, 8.5411)
	service := &Service{
		bus:            bus,
		queries:        fakeQueries{listZonesFn: zoneListQuery(t, zone)},
		crsTransformer: transform.NewCRSTransformer(),
		transformCache: transform.NewCache(),
		logger:         zapTestLogger(t),
	}
	crs := "local"
	location := testLocationWithCoordinates(t, &crs, zone.Id.String(), [2]float32{5, 5})
	trackables := []string{"trackable-a"}
	location.Trackables = &trackables

	if _, err := service.publishTrackableMotions(context.Background(), location); err != nil {
		t.Fatalf("publishTrackableMotions failed: %v", err)
	}
	events := collectEvents(ch, 2)
	localMotion := decodeEventMotion(t, eventByScope(t, events, ScopeLocal))
	wgsMotion := decodeEventMotion(t, eventByScope(t, events, ScopeEPSG4326))
	if localMotion.Location.Crs == nil || *localMotion.Location.Crs != "local" {
		t.Fatal("expected local motion to stay local")
	}
	if wgsMotion.Location.Crs == nil || *wgsMotion.Location.Crs != "EPSG:4326" {
		t.Fatal("expected wgs84 motion to use EPSG:4326")
	}
}

func TestPublishFenceEventsUsesLocationTTLForMembershipState(t *testing.T) {
	cache := newMemoryCache()
	service := &Service{
		cache: cache,
		queries: fakeQueries{
			listFencesFn: func(context.Context) ([]sqlcgen.Fence, error) {
				fence := testPointFence(t, uuid.New(), [2]float32{1, 2}, 5)
				payload, err := json.Marshal(fence)
				if err != nil {
					t.Fatalf("marshal fence failed: %v", err)
				}
				return []sqlcgen.Fence{{Payload: payload}}, nil
			},
		},
		bus: NewEventBus(),
		cfg: Config{LocationTTL: 90 * time.Second},
	}
	location := testLocation(t, nil)
	trackables := []string{"trackable-a"}
	location.Trackables = &trackables

	if err := service.publishFenceEvents(context.Background(), location); err != nil {
		t.Fatalf("publishFenceEvents failed: %v", err)
	}

	if len(cache.setCalls) != 1 {
		t.Fatalf("expected one fence membership write, got %d", len(cache.setCalls))
	}
	if cache.setCalls[0].ttl != service.cfg.LocationTTL {
		t.Fatalf("expected fence membership TTL %s, got %s", service.cfg.LocationTTL, cache.setCalls[0].ttl)
	}
}

func testPolicy() proximityResolutionPolicy {
	return proximityResolutionPolicy{
		ExitGraceDuration: 15 * time.Second,
		BoundaryGrace:     2,
		MinDwellDuration:  5 * time.Second,
		PositionMode:      "zone_position",
		StaleStateTTL:     10 * time.Minute,
	}
}

func testLocation(t *testing.T, crs *string) gen.Location {
	t.Helper()
	return testLocationWithCoordinates(t, crs, "zone-a", [2]float32{1, 2})
}

func testLocationWithCoordinates(t *testing.T, crs *string, source string, coordinates [2]float32) gen.Location {
	t.Helper()
	point := gen.Point{Type: "Point"}
	if err := point.Coordinates.FromGeoJsonPosition2D([]float32{coordinates[0], coordinates[1]}); err != nil {
		t.Fatalf("coordinates setup failed: %v", err)
	}
	return gen.Location{
		Crs:          crs,
		Position:     point,
		ProviderId:   "provider-a",
		ProviderType: "uwb",
		Source:       source,
	}
}

func testProximity(source string) gen.Proximity {
	return gen.Proximity{
		ProviderId:   "provider-a",
		ProviderType: "rfid",
		Source:       source,
	}
}

func testZone(t *testing.T, id uuid.UUID, zoneType string, coordinates [2]float32, radius *float32, props *gen.ExtensionProperties) gen.Zone {
	t.Helper()
	point := gen.Point{Type: "Point"}
	if err := point.Coordinates.FromGeoJsonPosition2D([]float32{coordinates[0], coordinates[1]}); err != nil {
		t.Fatalf("coordinates setup failed: %v", err)
	}
	return gen.Zone{
		Id:         uuidAsOpenAPI(id),
		Type:       zoneType,
		Position:   &point,
		Radius:     radius,
		Properties: props,
	}
}

func testZoneWithForeignID(t *testing.T, id uuid.UUID, zoneType, foreignID string, coordinates [2]float32, radius *float32, props *gen.ExtensionProperties) gen.Zone {
	t.Helper()
	zone := testZone(t, id, zoneType, coordinates, radius, props)
	zone.ForeignId = &foreignID
	return zone
}

func testPointFence(t *testing.T, id uuid.UUID, coordinates [2]float32, radius float32) gen.Fence {
	t.Helper()
	point := gen.Point{Type: "Point"}
	if err := point.Coordinates.FromGeoJsonPosition2D([]float32{coordinates[0], coordinates[1]}); err != nil {
		t.Fatalf("coordinates setup failed: %v", err)
	}
	var region gen.Fence_Region
	if err := region.FromPoint(point); err != nil {
		t.Fatalf("region setup failed: %v", err)
	}
	return gen.Fence{
		Id:     uuidAsOpenAPI(id),
		Radius: &radius,
		Region: region,
	}
}

func uuidAsOpenAPI(id uuid.UUID) [16]byte {
	return [16]byte(id)
}

func float32Ptr(value float32) *float32 {
	return &value
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json marshal failed: %v", err)
	}
	return payload
}

type memoryCache struct {
	values     map[string][]byte
	setCalls   []cacheWrite
	setNXCalls []cacheWrite
}

type cacheWrite struct {
	key   string
	value []byte
	ttl   time.Duration
}

func newMemoryCache() *memoryCache {
	return &memoryCache{values: map[string][]byte{}}
}

func (c *memoryCache) Get(_ context.Context, key string) ([]byte, error) {
	return c.values[key], nil
}

func (c *memoryCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	c.values[key] = append([]byte(nil), value...)
	c.setCalls = append(c.setCalls, cacheWrite{key: key, value: append([]byte(nil), value...), ttl: ttl})
	return nil
}

func (c *memoryCache) SetNX(_ context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	c.setNXCalls = append(c.setNXCalls, cacheWrite{key: key, value: append([]byte(nil), value...), ttl: ttl})
	if _, exists := c.values[key]; exists {
		return false, nil
	}
	c.values[key] = append([]byte(nil), value...)
	return true, nil
}

func (c *memoryCache) Delete(_ context.Context, key string) error {
	delete(c.values, key)
	return nil
}

func decodeEventLocation(t *testing.T, event Event) gen.Location {
	t.Helper()
	envelope, err := Decode[LocationEnvelope](event)
	if err != nil {
		t.Fatalf("decode event location failed: %v", err)
	}
	return envelope.Location
}

func decodeEventMotion(t *testing.T, event Event) gen.TrackableMotion {
	t.Helper()
	envelope, err := Decode[TrackableMotionEnvelope](event)
	if err != nil {
		t.Fatalf("decode event motion failed: %v", err)
	}
	return envelope.Motion
}

func collectEvents(ch <-chan Event, count int) []Event {
	out := make([]Event, 0, count)
	timeout := time.After(2 * time.Second)
	for len(out) < count {
		select {
		case event := <-ch:
			out = append(out, event)
		case <-timeout:
			return out
		}
	}
	return out
}

func eventByScope(t *testing.T, events []Event, scope EventScope) Event {
	t.Helper()
	for _, event := range events {
		if event.Scope == scope {
			return event
		}
	}
	t.Fatalf("scope %s not found", scope)
	return Event{}
}

func assertLocationsDiffer(t *testing.T, a, b gen.Location) {
	t.Helper()
	ap, err := point2D(a.Position)
	if err != nil {
		t.Fatalf("decode first location failed: %v", err)
	}
	bp, err := point2D(b.Position)
	if err != nil {
		t.Fatalf("decode second location failed: %v", err)
	}
	if math.Abs(ap[0]-bp[0]) < 1e-6 && math.Abs(ap[1]-bp[1]) < 1e-6 {
		t.Fatal("expected transformed variants to differ")
	}
}

func zoneListQuery(t *testing.T, zones ...gen.Zone) func(context.Context) ([]sqlcgen.Zone, error) {
	t.Helper()
	return func(context.Context) ([]sqlcgen.Zone, error) {
		rows := make([]sqlcgen.Zone, 0, len(zones))
		for _, zone := range zones {
			payload, err := json.Marshal(zone)
			if err != nil {
				t.Fatalf("marshal zone failed: %v", err)
			}
			rows = append(rows, sqlcgen.Zone{Payload: payload})
		}
		return rows, nil
	}
}

func georeferencedZoneFixture(t *testing.T, lat, lon float64) gen.Zone {
	t.Helper()
	zoneID := uuid.New()
	gcps := []gen.GroundControlPoint{
		{Local: pointAt(t, 0, 0), Wgs84: pointAt(t, lon, lat)},
		{Local: pointAt(t, 10, 0), Wgs84: pointAt(t, lon+0.0001, lat)},
		{Local: pointAt(t, 0, 10), Wgs84: pointAt(t, lon, lat+0.0001)},
	}
	return gen.Zone{
		Id:                  [16]byte(zoneID),
		Type:                "uwb",
		GroundControlPoints: &gcps,
	}
}

func pointAt(t *testing.T, x, y float64) gen.Point {
	t.Helper()
	point := gen.Point{Type: "Point"}
	if err := point.Coordinates.FromGeoJsonPosition2D([]float32{float32(x), float32(y)}); err != nil {
		t.Fatalf("coordinates setup failed: %v", err)
	}
	return point
}

func zapTestLogger(t *testing.T) *zap.Logger {
	t.Helper()
	logger, err := zap.NewDevelopment()
	if err != nil {
		t.Fatalf("logger setup failed: %v", err)
	}
	return logger
}

type fakeQueries struct {
	listFencesFn func(context.Context) ([]sqlcgen.Fence, error)
	listZonesFn  func(context.Context) ([]sqlcgen.Zone, error)
}

func (f fakeQueries) CreateFence(context.Context, sqlcgen.CreateFenceParams) (sqlcgen.Fence, error) {
	panic("unexpected call")
}

func (f fakeQueries) CreateProvider(context.Context, sqlcgen.CreateProviderParams) (sqlcgen.Provider, error) {
	panic("unexpected call")
}

func (f fakeQueries) CreateTrackable(context.Context, sqlcgen.CreateTrackableParams) (sqlcgen.Trackable, error) {
	panic("unexpected call")
}

func (f fakeQueries) CreateZone(context.Context, sqlcgen.CreateZoneParams) (sqlcgen.Zone, error) {
	panic("unexpected call")
}

func (f fakeQueries) DeleteFence(context.Context, pgtype.UUID) (int64, error) {
	panic("unexpected call")
}

func (f fakeQueries) DeleteProvider(context.Context, string) (int64, error) {
	panic("unexpected call")
}

func (f fakeQueries) DeleteTrackable(context.Context, pgtype.UUID) (int64, error) {
	panic("unexpected call")
}

func (f fakeQueries) DeleteZone(context.Context, pgtype.UUID) (int64, error) {
	panic("unexpected call")
}

func (f fakeQueries) GetFence(context.Context, pgtype.UUID) (sqlcgen.Fence, error) {
	panic("unexpected call")
}

func (f fakeQueries) GetProvider(context.Context, string) (sqlcgen.Provider, error) {
	panic("unexpected call")
}

func (f fakeQueries) GetTrackable(context.Context, pgtype.UUID) (sqlcgen.Trackable, error) {
	panic("unexpected call")
}

func (f fakeQueries) GetZone(context.Context, pgtype.UUID) (sqlcgen.Zone, error) {
	panic("unexpected call")
}

func (f fakeQueries) ListFences(ctx context.Context) ([]sqlcgen.Fence, error) {
	if f.listFencesFn == nil {
		panic("unexpected call")
	}
	return f.listFencesFn(ctx)
}

func (f fakeQueries) ListProviders(context.Context) ([]sqlcgen.Provider, error) {
	panic("unexpected call")
}

func (f fakeQueries) ListTrackables(context.Context) ([]sqlcgen.Trackable, error) {
	panic("unexpected call")
}

func (f fakeQueries) ListZones(ctx context.Context) ([]sqlcgen.Zone, error) {
	if f.listZonesFn == nil {
		panic("unexpected call")
	}
	return f.listZonesFn(ctx)
}

func (f fakeQueries) UpdateFence(context.Context, sqlcgen.UpdateFenceParams) (sqlcgen.Fence, error) {
	panic("unexpected call")
}

func (f fakeQueries) UpdateProvider(context.Context, sqlcgen.UpdateProviderParams) (sqlcgen.Provider, error) {
	panic("unexpected call")
}

func (f fakeQueries) UpdateTrackable(context.Context, sqlcgen.UpdateTrackableParams) (sqlcgen.Trackable, error) {
	panic("unexpected call")
}

func (f fakeQueries) UpdateZone(context.Context, sqlcgen.UpdateZoneParams) (sqlcgen.Zone, error) {
	panic("unexpected call")
}
