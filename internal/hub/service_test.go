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
	t.Parallel()

	_, _, err := normalizeZone(json.RawMessage(`{"type":"uwb","name":"zone-a"}`), [16]byte{})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestNormalizeFenceRequiresZoneIDForLocalCRS(t *testing.T) {
	t.Parallel()

	_, _, err := normalizeFence(json.RawMessage(`{"crs":"local","region":{"type":"Point","coordinates":[1,2]}}`), [16]byte{})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestNormalizeZoneGeneratesVersion7ID(t *testing.T) {
	t.Parallel()

	zone, _, err := normalizeZone(json.RawMessage(`{
		"type":"uwb",
		"incomplete_configuration":true
	}`), [16]byte{})
	if err != nil {
		t.Fatalf("normalizeZone failed: %v", err)
	}
	if got := uuid.UUID(zone.Id).Version(); got != 7 {
		t.Fatalf("expected UUIDv7, got version %d", got)
	}
}

func TestNormalizeFenceGeneratesVersion7ID(t *testing.T) {
	t.Parallel()

	fence, _, err := normalizeFence(json.RawMessage(`{
		"region":{"type":"Point","coordinates":[1,2]},
		"radius":5
	}`), [16]byte{})
	if err != nil {
		t.Fatalf("normalizeFence failed: %v", err)
	}
	if got := uuid.UUID(fence.Id).Version(); got != 7 {
		t.Fatalf("expected UUIDv7, got version %d", got)
	}
}

func TestNormalizeTrackableGeneratesVersion7ID(t *testing.T) {
	t.Parallel()

	trackable, _, err := normalizeTrackable(gen.TrackableWrite{
		Type: gen.TrackableWriteTypeOmlox,
	}, uuid.Nil)
	if err != nil {
		t.Fatalf("normalizeTrackable failed: %v", err)
	}
	if got := uuid.UUID(trackable.Id).Version(); got != 7 {
		t.Fatalf("expected UUIDv7, got version %d", got)
	}
}

func TestFenceContainsPointForPointFence(t *testing.T) {
	t.Parallel()

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

func TestEffectiveRadiusMetersUsesConfiguredDefault(t *testing.T) {
	t.Parallel()

	trackable := gen.Trackable{Id: uuidAsOpenAPI(uuid.New()), Type: gen.TrackableTypeOmlox}
	if got := effectiveRadiusMeters(trackable, 12.5); got != 12.5 {
		t.Fatalf("expected configured default radius, got %v", got)
	}
}

func TestEffectiveRadiusMetersPrefersTrackableOverride(t *testing.T) {
	t.Parallel()

	trackable := gen.Trackable{
		Id:     uuidAsOpenAPI(uuid.New()),
		Type:   gen.TrackableTypeOmlox,
		Radius: float32Ptr(7),
	}
	if got := effectiveRadiusMeters(trackable, 12.5); got != 7 {
		t.Fatalf("expected trackable radius override, got %v", got)
	}
}

func TestLocationPropertiesExcludeGeometryAndPreserveFields(t *testing.T) {
	t.Parallel()

	crs := "local"
	location := testLocationWithCoordinates(t, &crs, "source-a", [2]float32{1, 2})
	location.ProviderId = "provider-a"
	accuracy := float32(1.5)
	location.Accuracy = &accuracy
	location.Properties = &gen.ExtensionProperties{"label": "test"}

	properties := locationProperties(location)

	if _, ok := properties["position"]; ok {
		t.Fatal("location properties should not include position")
	}
	if got := properties["provider_id"]; got != location.ProviderId {
		t.Fatalf("expected provider_id %q, got %#v", location.ProviderId, got)
	}
	if got := properties["source"]; got != location.Source {
		t.Fatalf("expected source %q, got %#v", location.Source, got)
	}
	if got := properties["accuracy"]; got != accuracy {
		t.Fatalf("expected accuracy %v, got %#v", accuracy, got)
	}
	nested, ok := properties["properties"].(map[string]any)
	if !ok || nested["label"] != "test" {
		t.Fatalf("expected nested extension properties, got %#v", properties["properties"])
	}
}

func TestFenceEventPropertiesExcludeGeometryAndPreserveFields(t *testing.T) {
	t.Parallel()

	fenceID := uuidAsOpenAPI(uuid.New())
	eventID := uuidAsOpenAPI(uuid.New())
	trackableID := "trackable-a"
	providerID := "provider-a"
	entryTime := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	event := gen.FenceEvent{
		Id:          eventID,
		FenceId:     fenceID,
		EventType:   gen.FenceEventEventType("region_entry"),
		EntryTime:   &entryTime,
		TrackableId: &trackableID,
		ProviderId:  &providerID,
		Properties:  &gen.ExtensionProperties{"label": "test"},
	}

	properties := fenceEventProperties(event)

	if _, ok := properties["region"]; ok {
		t.Fatal("fence event properties should not include region")
	}
	if got, ok := properties["fence_id"].(uuid.UUID); !ok || got != uuid.UUID(fenceID) {
		t.Fatalf("expected fence_id %#v, got %#v", uuid.UUID(fenceID), properties["fence_id"])
	}
	if got := properties["event_type"]; got != event.EventType {
		t.Fatalf("expected event_type %q, got %#v", event.EventType, got)
	}
	if got := properties["provider_id"]; got != providerID {
		t.Fatalf("expected provider_id %q, got %#v", providerID, got)
	}
	nested, ok := properties["properties"].(map[string]any)
	if !ok || nested["label"] != "test" {
		t.Fatalf("expected nested extension properties, got %#v", properties["properties"])
	}
}

func TestMotionsMayCollideUsesMeterAwareWGS84Approximation(t *testing.T) {
	t.Parallel()

	crs := "EPSG:4326"
	leftMotion := gen.TrackableMotion{Id: "left", Location: testLocationWithCoordinates(t, &crs, "left", [2]float32{8.5411, 47.3769})}
	rightMotion := gen.TrackableMotion{Id: "right", Location: testLocationWithCoordinates(t, &crs, "right", [2]float32{8.5411, 47.3778})}
	trackable := gen.Trackable{Id: uuidAsOpenAPI(uuid.New()), Type: gen.TrackableTypeOmlox, Radius: float32Ptr(50)}
	leftPoint, err := point2D(leftMotion.Location.Position)
	if err != nil {
		t.Fatalf("left point decode failed: %v", err)
	}
	rightPoint, err := point2D(rightMotion.Location.Position)
	if err != nil {
		t.Fatalf("right point decode failed: %v", err)
	}

	if motionsMayCollide(leftMotion, leftPoint, trackable, rightMotion, rightPoint, trackable, defaultCollisionRadiusMeters) {
		t.Fatal("expected motions about 100m apart not to collide with 50m radii")
	}
}

func TestMotionsCollideUsesMeterAwareWGS84Approximation(t *testing.T) {
	t.Parallel()

	crs := "EPSG:4326"
	leftMotion := gen.TrackableMotion{Id: "left", Location: testLocationWithCoordinates(t, &crs, "left", [2]float32{8.5411, 47.3769})}
	rightMotion := gen.TrackableMotion{Id: "right", Location: testLocationWithCoordinates(t, &crs, "right", [2]float32{8.5411, 47.37735})}
	trackable := gen.Trackable{Id: uuidAsOpenAPI(uuid.New()), Type: gen.TrackableTypeOmlox, Radius: float32Ptr(30)}
	leftPoint, err := point2D(leftMotion.Location.Position)
	if err != nil {
		t.Fatalf("left point decode failed: %v", err)
	}
	rightPoint, err := point2D(rightMotion.Location.Position)
	if err != nil {
		t.Fatalf("right point decode failed: %v", err)
	}

	colliding, _, distance := motionsCollide(leftMotion, trackable, rightMotion, trackable, leftPoint, rightPoint, defaultCollisionRadiusMeters)
	if !colliding {
		t.Fatal("expected motions about 50m apart to collide with 30m radii")
	}
	if distance < 40 || distance > 60 {
		t.Fatalf("expected distance near 50m, got %v", distance)
	}
}

func TestPointSquarePolygonConvertsMetersForWGS84(t *testing.T) {
	t.Parallel()

	crs := "EPSG:4326"
	location := testLocationWithCoordinates(t, &crs, "point", [2]float32{8.5411, 47.3769})
	polygon := pointSquarePolygon(location, 50)
	if polygon == nil {
		t.Fatal("expected fallback polygon")
	}
	bounds, err := polygonBounds(*polygon)
	if err != nil {
		t.Fatalf("polygon bounds failed: %v", err)
	}
	if bounds.maxY-bounds.minY >= 0.01 {
		t.Fatalf("expected meter-based WGS84 envelope, got latitude span %v", bounds.maxY-bounds.minY)
	}
}

func TestCollisionSpatialIndexNearbyFiltersFarWGS84Candidates(t *testing.T) {
	t.Parallel()

	crs := "EPSG:4326"
	nearMotion := gen.TrackableMotion{Id: "near", Location: testLocationWithCoordinates(t, &crs, "near", [2]float32{8.5411, 47.37735})}
	farMotion := gen.TrackableMotion{Id: "far", Location: testLocationWithCoordinates(t, &crs, "far", [2]float32{8.5411, 47.39})}
	queryMotion := gen.TrackableMotion{Id: "query", Location: testLocationWithCoordinates(t, &crs, "query", [2]float32{8.5411, 47.3769})}

	var indexed []indexedCollisionMotion
	for _, motion := range []gen.TrackableMotion{nearMotion, farMotion} {
		point, err := point2D(motion.Location.Position)
		if err != nil {
			t.Fatalf("point decode failed: %v", err)
		}
		item, ok := newIndexedCollisionMotion(motion, point)
		if !ok {
			t.Fatal("expected indexed motion")
		}
		indexed = append(indexed, item)
	}
	index := newCollisionSpatialIndex(indexed)
	queryPoint, err := point2D(queryMotion.Location.Position)
	if err != nil {
		t.Fatalf("query point decode failed: %v", err)
	}

	candidates := index.Nearby(queryMotion.Location, queryPoint, 120)
	ids := map[string]bool{}
	for _, candidate := range candidates {
		ids[candidate.motion.Id] = true
	}
	if !ids["near"] {
		t.Fatal("expected nearby motion candidate")
	}
	if ids["far"] {
		t.Fatal("did not expect far motion candidate")
	}
}

func TestCollisionSpatialIndexNearbyFiltersFarLocalCandidates(t *testing.T) {
	t.Parallel()

	crs := "local"
	nearMotion := gen.TrackableMotion{Id: "near", Location: testLocationWithCoordinates(t, &crs, "near", [2]float32{20, 20})}
	farMotion := gen.TrackableMotion{Id: "far", Location: testLocationWithCoordinates(t, &crs, "far", [2]float32{600, 600})}
	queryMotion := gen.TrackableMotion{Id: "query", Location: testLocationWithCoordinates(t, &crs, "query", [2]float32{0, 0})}

	var indexed []indexedCollisionMotion
	for _, motion := range []gen.TrackableMotion{nearMotion, farMotion} {
		point, err := point2D(motion.Location.Position)
		if err != nil {
			t.Fatalf("point decode failed: %v", err)
		}
		item, ok := newIndexedCollisionMotion(motion, point)
		if !ok {
			t.Fatal("expected indexed motion")
		}
		indexed = append(indexed, item)
	}
	index := newCollisionSpatialIndex(indexed)
	queryPoint, err := point2D(queryMotion.Location.Position)
	if err != nil {
		t.Fatalf("query point decode failed: %v", err)
	}

	candidates := index.Nearby(queryMotion.Location, queryPoint, 100)
	ids := map[string]bool{}
	for _, candidate := range candidates {
		ids[candidate.motion.Id] = true
	}
	if !ids["near"] {
		t.Fatal("expected nearby local motion candidate")
	}
	if ids["far"] {
		t.Fatal("did not expect far local motion candidate")
	}
}

func TestNormalizeZoneRejectsInvalidProximityResolutionProperties(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

	if err := validateLocation(testLocation(t, nil)); err != nil {
		t.Fatalf("expected omitted CRS to pass, got %v", err)
	}
}

func TestValidateLocationAllowsLocalCRS(t *testing.T) {
	t.Parallel()

	crs := "local"
	if err := validateLocation(testLocation(t, &crs)); err != nil {
		t.Fatalf("expected local CRS to pass, got %v", err)
	}
}

func TestValidateLocationAllowsEPSG4326CRS(t *testing.T) {
	t.Parallel()

	crs := "EPSG:4326"
	if err := validateLocation(testLocation(t, &crs)); err != nil {
		t.Fatalf("expected EPSG:4326 CRS to pass, got %v", err)
	}
}

func TestValidateLocationRejectsUnsupportedCRS(t *testing.T) {
	t.Parallel()

	crs := "WGS84"
	err := validateLocation(testLocation(t, &crs))
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != 400 {
		t.Fatalf("expected bad request for unsupported CRS, got %v", err)
	}
}

func TestRecordLocationDeduplicatesAndStoresLatestStateWithTTL(t *testing.T) {
	t.Parallel()

	state := NewProcessingState(time.Now)
	service := &Service{
		state: state,
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

	if !state.Deduplicate(dedupKey(mustJSON(t, location)), service.cfg.DedupTTL) {
		// expected the explicit probe to observe the existing dedup window
	} else {
		t.Fatal("expected dedup state to be retained in memory")
	}
	latest, ok := state.latestLocations[latestLocationKey(location.ProviderId, location.Source)]
	if !ok {
		t.Fatal("expected latest location state to be stored")
	}
	if time.Until(latest.expiresAt) <= 0 {
		t.Fatal("expected latest location state to carry a positive ttl")
	}
}

func TestRecordLocationStoresTrackableLatestStateWithTTL(t *testing.T) {
	t.Parallel()

	state := NewProcessingState(time.Now)
	service := &Service{
		state: state,
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

	if _, ok := state.latestTrackableLocation[latestTrackableLocationKey("trackable-a")]; !ok {
		t.Fatal("expected first trackable latest state")
	}
	if _, ok := state.latestTrackableLocation[latestTrackableLocationKey("trackable-b")]; !ok {
		t.Fatal("expected second trackable latest state")
	}
}

func TestProcessProximitiesReEntersAfterStaleStateExpiry(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC)
	zone := testZone(t, uuid.New(), "rfid", [2]float32{1, 2}, float32Ptr(2), nil)
	proximity := testProximity(zone.Id.String())
	service := &Service{
		state: NewProcessingState(func() time.Time { return now }),
		metadata: &MetadataCache{
			snapshot: newMetadataSnapshot([]zoneRecord{{Zone: zone, Signature: "zone"}}, nil, nil, nil),
		},
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
	service.processingState().SetProximityState(proximityResolutionStateKey(proximity.ProviderType, proximity.ProviderId), proximityResolutionState{
		ResolvedZoneID:  "expired-zone",
		EnteredAt:       now.Add(-30 * time.Minute),
		LastConfirmedAt: now.Add(-20 * time.Minute),
		LastEmittedAt:   now.Add(-20 * time.Minute),
	}, time.Minute)

	if err := service.ProcessProximities(context.Background(), []gen.Proximity{proximity}); err != nil {
		t.Fatalf("ProcessProximities failed: %v", err)
	}

	state, ok := service.processingState().GetProximityState(proximityResolutionStateKey(proximity.ProviderType, proximity.ProviderId))
	if !ok {
		t.Fatal("expected proximity state after processing")
	}
	if state.ResolvedZoneID != proximity.Source {
		t.Fatalf("expected re-entry into candidate zone %s, got %s", proximity.Source, state.ResolvedZoneID)
	}
	if !state.EnteredAt.Equal(now) {
		t.Fatalf("expected re-entry timestamp %s, got %s", now, state.EnteredAt)
	}
}

func TestPublishLocationTransformsLocalToWGS84(t *testing.T) {
	t.Parallel()

	bus := NewEventBus()
	ch, unsubscribe := bus.Subscribe(8)
	defer unsubscribe()
	zone := georeferencedZoneFixture(t, 47.3744, 8.5411)
	service := &Service{
		bus:            bus,
		metadata:       &MetadataCache{snapshot: newMetadataSnapshot([]zoneRecord{{Zone: zone, Signature: "zone"}}, nil, nil, nil)},
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
	if got := eventByScope(t, events, ScopeLocal).OriginHubID; got != "" {
		t.Fatalf("expected empty origin hub id by default, got %q", got)
	}
	if localPublished.Crs == nil || *localPublished.Crs != "local" {
		t.Fatal("expected local publication to stay local")
	}
	if wgsPublished.Crs == nil || *wgsPublished.Crs != "EPSG:4326" {
		t.Fatal("expected wgs84 publication to use EPSG:4326")
	}
	assertLocationsDiffer(t, localPublished, wgsPublished)
}

func TestPublishLocationEmitsOriginHubIDWhenConfigured(t *testing.T) {
	t.Parallel()

	bus := NewEventBus()
	ch, unsubscribe := bus.Subscribe(8)
	defer unsubscribe()
	zone := georeferencedZoneFixture(t, 47.3744, 8.5411)
	service := &Service{
		bus:            bus,
		cfg:            Config{HubID: "4f630dd4-e5f2-4398-9970-c63cad9bc109"},
		metadata:       &MetadataCache{snapshot: newMetadataSnapshot([]zoneRecord{{Zone: zone, Signature: "zone"}}, nil, nil, nil)},
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
	if got := eventByScope(t, events, ScopeLocal).OriginHubID; got != "4f630dd4-e5f2-4398-9970-c63cad9bc109" {
		t.Fatalf("unexpected local origin hub id: %q", got)
	}
	if got := eventByScope(t, events, ScopeEPSG4326).OriginHubID; got != "4f630dd4-e5f2-4398-9970-c63cad9bc109" {
		t.Fatalf("unexpected wgs84 origin hub id: %q", got)
	}
}

func TestPublishLocationTransformsWGS84ToLocalWhenZoneIsGeoreferenced(t *testing.T) {
	t.Parallel()

	bus := NewEventBus()
	ch, unsubscribe := bus.Subscribe(8)
	defer unsubscribe()
	zone := georeferencedZoneFixture(t, -33.4489, -70.6693)
	service := &Service{
		bus:            bus,
		metadata:       &MetadataCache{snapshot: newMetadataSnapshot([]zoneRecord{{Zone: zone, Signature: "zone"}}, nil, nil, nil)},
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
	t.Parallel()

	bus := NewEventBus()
	ch, unsubscribe := bus.Subscribe(8)
	defer unsubscribe()
	crs := "local"
	location := testLocationWithCoordinates(t, &crs, "missing-zone", [2]float32{2, 3})
	service := &Service{
		bus:            bus,
		metadata:       &MetadataCache{snapshot: newMetadataSnapshot(nil, nil, nil, nil)},
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
	t.Parallel()

	bus := NewEventBus()
	ch, unsubscribe := bus.Subscribe(8)
	defer unsubscribe()
	zone := georeferencedZoneFixture(t, 47.3744, 8.5411)
	service := &Service{
		bus:            bus,
		metadata:       &MetadataCache{snapshot: newMetadataSnapshot([]zoneRecord{{Zone: zone, Signature: "zone"}}, nil, nil, nil)},
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
	t.Parallel()

	state := NewProcessingState(time.Now)
	fence := testPointFence(t, uuid.New(), [2]float32{1, 2}, 5)
	service := &Service{
		state:    state,
		metadata: &MetadataCache{snapshot: newMetadataSnapshot(nil, []fenceRecord{{Fence: fence, Signature: "fence"}}, nil, nil)},
		bus:      NewEventBus(),
		cfg:      Config{LocationTTL: 90 * time.Second},
	}
	location := testLocation(t, nil)
	trackables := []string{"trackable-a"}
	location.Trackables = &trackables

	if err := service.publishFenceEvents(context.Background(), location); err != nil {
		t.Fatalf("publishFenceEvents failed: %v", err)
	}

	if !state.IsInsideFence("trackable-a", fence.Id.String()) {
		t.Fatal("expected fence membership state to be held in memory")
	}
}

type captureCollisionQueue struct {
	works []collisionWork
}

func (q *captureCollisionQueue) Submit(work collisionWork) {
	q.works = append(q.works, work)
}

func TestEnqueueCollisionWorkUsesCollisionQueueWhenAvailable(t *testing.T) {
	t.Parallel()

	queue := &captureCollisionQueue{}
	service := &Service{
		cfg: Config{
			CollisionsEnabled: true,
		},
		collisionQueue: queue,
	}
	motions := []gen.TrackableMotion{{Id: "trackable-a"}}

	if err := service.enqueueCollisionWork(context.Background(), motions); err != nil {
		t.Fatalf("enqueueCollisionWork failed: %v", err)
	}
	if len(queue.works) != 1 {
		t.Fatalf("expected one collision work item, got %d", len(queue.works))
	}
	if len(queue.works[0].Motions) != 1 || queue.works[0].Motions[0].Id != "trackable-a" {
		t.Fatalf("unexpected queued motions: %+v", queue.works[0].Motions)
	}
}

func TestProcessDerivedLocationEvaluatesGeofencesWhenPublicationSuppressed(t *testing.T) {
	t.Parallel()

	bus := NewEventBus()
	ch, unsubscribe := bus.Subscribe(8)
	defer unsubscribe()
	state := NewProcessingState(time.Now)
	fence := testPointFence(t, uuid.New(), [2]float32{1, 2}, 5)
	crs := "local"
	location := testLocation(t, &crs)
	trackables := []string{"trackable-a"}
	location.Trackables = &trackables

	service := &Service{
		bus:      bus,
		state:    state,
		metadata: &MetadataCache{snapshot: newMetadataSnapshot(nil, []fenceRecord{{Fence: fence, Signature: "fence"}}, nil, nil)},
		cfg:      Config{LocationTTL: time.Minute},
	}

	if err := service.processDerivedLocation(context.Background(), location, false); err != nil {
		t.Fatalf("processDerivedLocation failed: %v", err)
	}

	events := collectEvents(ch, 1)
	if len(events) != 1 {
		t.Fatalf("expected one fence event, got %d", len(events))
	}
	if events[0].Kind != EventFenceEvent {
		t.Fatalf("expected fence event, got %s", events[0].Kind)
	}
}

func TestProcessDerivedLocationEvaluatesCollisionsWhenPublicationSuppressed(t *testing.T) {
	t.Parallel()

	queue := &captureCollisionQueue{}
	bus := NewEventBus()
	crs := "EPSG:4326"
	location := testLocationWithCoordinates(t, &crs, "external-source", [2]float32{8.5, 47.3})
	trackables := []string{"trackable-a"}
	location.Trackables = &trackables

	service := &Service{
		bus:            bus,
		collisionQueue: queue,
		metadata:       &MetadataCache{snapshot: newMetadataSnapshot(nil, nil, nil, nil)},
		cfg: Config{
			CollisionsEnabled: true,
		},
	}

	if err := service.processDerivedLocation(context.Background(), location, false); err != nil {
		t.Fatalf("processDerivedLocation failed: %v", err)
	}
	if len(queue.works) != 1 {
		t.Fatalf("expected one collision work item, got %d", len(queue.works))
	}
}

func TestProcessDerivedLocationSkipsCollisionsWhenWGS84Unavailable(t *testing.T) {
	t.Parallel()

	queue := &captureCollisionQueue{}
	bus := NewEventBus()
	crs := "local"
	location := testLocationWithCoordinates(t, &crs, "external-source", [2]float32{8.5, 47.3})
	trackables := []string{"trackable-a"}
	location.Trackables = &trackables

	service := &Service{
		bus:            bus,
		collisionQueue: queue,
		metadata:       &MetadataCache{snapshot: newMetadataSnapshot(nil, nil, nil, nil)},
		cfg: Config{
			CollisionsEnabled: true,
		},
	}

	if err := service.processDerivedLocation(context.Background(), location, true); err != nil {
		t.Fatalf("processDerivedLocation failed: %v", err)
	}
	if len(queue.works) != 0 {
		t.Fatalf("expected no collision work without WGS84 transform, got %d", len(queue.works))
	}
}

type capturingDerivedSubmitter struct {
	works []derivedLocationWork
}

func (c *capturingDerivedSubmitter) Submit(work derivedLocationWork) {
	c.works = append(c.works, work)
}

func TestRecordLocationWithNativeQueueQueuesWork(t *testing.T) {
	t.Parallel()

	bus := NewEventBus()
	queue := &capturingDerivedSubmitter{}
	crs := "EPSG:4326"
	location := testLocationWithCoordinates(t, &crs, "external-source", [2]float32{8.5, 47.3})
	trackables := []string{"trackable-a"}
	location.Trackables = &trackables

	service := &Service{
		bus:         bus,
		nativeQueue: queue,
		cfg: Config{
			NativeLocationBuffer:       16,
			LocationTTL:                time.Minute,
			DedupTTL:                   time.Minute,
			CollisionStateTTL:          time.Minute,
			CollisionCollidingDebounce: time.Second,
			MetadataReconcileInterval:  time.Second,
		},
		metadata: &MetadataCache{snapshot: newMetadataSnapshot(nil, nil, nil, nil)},
		state:    NewProcessingState(time.Now),
		logger:   zapTestLogger(t),
	}

	if err := service.recordLocation(context.Background(), location, time.Minute); err != nil {
		t.Fatalf("recordLocation failed: %v", err)
	}
	if len(queue.works) != 1 {
		t.Fatalf("expected one queued native work item, got %d", len(queue.works))
	}
}

func TestProcessNativeLocationPublishesNativeAndQueuesDecisionWork(t *testing.T) {
	t.Parallel()

	bus := NewEventBus()
	ch, unsubscribe := bus.Subscribe(8)
	defer unsubscribe()
	queue := &capturingDerivedSubmitter{}
	crs := "EPSG:4326"
	location := testLocationWithCoordinates(t, &crs, "external-source", [2]float32{8.5, 47.3})
	trackables := []string{"trackable-a"}
	location.Trackables = &trackables

	service := &Service{
		bus:          bus,
		derivedQueue: queue,
		cfg: Config{
			DerivedLocationBuffer:      16,
			LocationTTL:                time.Minute,
			DedupTTL:                   time.Minute,
			CollisionStateTTL:          time.Minute,
			CollisionCollidingDebounce: time.Second,
			MetadataReconcileInterval:  time.Second,
		},
		metadata: &MetadataCache{snapshot: newMetadataSnapshot(nil, nil, nil, nil)},
		state:    NewProcessingState(time.Now),
		logger:   zapTestLogger(t),
	}

	if err := service.processNativeLocation(context.Background(), location); err != nil {
		t.Fatalf("processNativeLocation failed: %v", err)
	}
	events := collectEvents(ch, 2)
	if len(events) != 2 {
		t.Fatalf("expected 2 native events, got %d", len(events))
	}
	if got := decodeEventLocation(t, eventByScope(t, events, ScopeEPSG4326)); got.Source != location.Source {
		t.Fatalf("unexpected native location source: %s", got.Source)
	}
	if len(queue.works) != 1 {
		t.Fatalf("expected one queued decision work item, got %d", len(queue.works))
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

func testLocationWithCoordinates3D(t *testing.T, crs *string, source string, coordinates [3]float32) gen.Location {
	t.Helper()
	point := gen.Point{Type: "Point"}
	if err := point.Coordinates.FromGeoJsonPosition3D([]float32{coordinates[0], coordinates[1], coordinates[2]}); err != nil {
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
	getHubMetadataFn  func(context.Context) (sqlcgen.HubMetadatum, error)
	createHubMetaFn   func(context.Context, sqlcgen.CreateHubMetadataParams) (sqlcgen.HubMetadatum, error)
	updateHubMetaFn   func(context.Context, sqlcgen.UpdateHubMetadataParams) (sqlcgen.HubMetadatum, error)
	listFencesFn      func(context.Context) ([]sqlcgen.Fence, error)
	listProvidersFn   func(context.Context) ([]sqlcgen.Provider, error)
	listTrackablesFn  func(context.Context) ([]sqlcgen.Trackable, error)
	listZonesFn       func(context.Context) ([]sqlcgen.Zone, error)
	createZoneFn      func(context.Context, sqlcgen.CreateZoneParams) (sqlcgen.Zone, error)
	updateZoneFn      func(context.Context, sqlcgen.UpdateZoneParams) (sqlcgen.Zone, error)
	deleteZoneFn      func(context.Context, pgtype.UUID) (int64, error)
	createFenceFn     func(context.Context, sqlcgen.CreateFenceParams) (sqlcgen.Fence, error)
	updateFenceFn     func(context.Context, sqlcgen.UpdateFenceParams) (sqlcgen.Fence, error)
	deleteFenceFn     func(context.Context, pgtype.UUID) (int64, error)
	createTrackableFn func(context.Context, sqlcgen.CreateTrackableParams) (sqlcgen.Trackable, error)
	updateTrackableFn func(context.Context, sqlcgen.UpdateTrackableParams) (sqlcgen.Trackable, error)
	deleteTrackableFn func(context.Context, pgtype.UUID) (int64, error)
	createProviderFn  func(context.Context, sqlcgen.CreateProviderParams) (sqlcgen.Provider, error)
	updateProviderFn  func(context.Context, sqlcgen.UpdateProviderParams) (sqlcgen.Provider, error)
	deleteProviderFn  func(context.Context, string) (int64, error)
}

func (f fakeQueries) CreateFence(ctx context.Context, params sqlcgen.CreateFenceParams) (sqlcgen.Fence, error) {
	if f.createFenceFn != nil {
		return f.createFenceFn(ctx, params)
	}
	panic("unexpected call")
}

func (f fakeQueries) CreateHubMetadata(ctx context.Context, params sqlcgen.CreateHubMetadataParams) (sqlcgen.HubMetadatum, error) {
	if f.createHubMetaFn != nil {
		return f.createHubMetaFn(ctx, params)
	}
	panic("unexpected call")
}

func (f fakeQueries) CreateProvider(ctx context.Context, params sqlcgen.CreateProviderParams) (sqlcgen.Provider, error) {
	if f.createProviderFn != nil {
		return f.createProviderFn(ctx, params)
	}
	panic("unexpected call")
}

func (f fakeQueries) CreateTrackable(ctx context.Context, params sqlcgen.CreateTrackableParams) (sqlcgen.Trackable, error) {
	if f.createTrackableFn != nil {
		return f.createTrackableFn(ctx, params)
	}
	panic("unexpected call")
}

func (f fakeQueries) CreateZone(ctx context.Context, params sqlcgen.CreateZoneParams) (sqlcgen.Zone, error) {
	if f.createZoneFn != nil {
		return f.createZoneFn(ctx, params)
	}
	panic("unexpected call")
}

func (f fakeQueries) DeleteFence(ctx context.Context, id pgtype.UUID) (int64, error) {
	if f.deleteFenceFn != nil {
		return f.deleteFenceFn(ctx, id)
	}
	panic("unexpected call")
}

func (f fakeQueries) DeleteProvider(ctx context.Context, id string) (int64, error) {
	if f.deleteProviderFn != nil {
		return f.deleteProviderFn(ctx, id)
	}
	panic("unexpected call")
}

func (f fakeQueries) DeleteTrackable(ctx context.Context, id pgtype.UUID) (int64, error) {
	if f.deleteTrackableFn != nil {
		return f.deleteTrackableFn(ctx, id)
	}
	panic("unexpected call")
}

func (f fakeQueries) DeleteZone(ctx context.Context, id pgtype.UUID) (int64, error) {
	if f.deleteZoneFn != nil {
		return f.deleteZoneFn(ctx, id)
	}
	panic("unexpected call")
}

func (f fakeQueries) GetFence(context.Context, pgtype.UUID) (sqlcgen.Fence, error) {
	panic("unexpected call")
}

func (f fakeQueries) GetHubMetadata(ctx context.Context) (sqlcgen.HubMetadatum, error) {
	if f.getHubMetadataFn != nil {
		return f.getHubMetadataFn(ctx)
	}
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

func (f fakeQueries) ListProviders(ctx context.Context) ([]sqlcgen.Provider, error) {
	if f.listProvidersFn == nil {
		return nil, nil
	}
	return f.listProvidersFn(ctx)
}

func (f fakeQueries) ListTrackables(ctx context.Context) ([]sqlcgen.Trackable, error) {
	if f.listTrackablesFn == nil {
		return nil, nil
	}
	return f.listTrackablesFn(ctx)
}

func (f fakeQueries) ListZones(ctx context.Context) ([]sqlcgen.Zone, error) {
	if f.listZonesFn == nil {
		panic("unexpected call")
	}
	return f.listZonesFn(ctx)
}

func (f fakeQueries) UpdateFence(ctx context.Context, params sqlcgen.UpdateFenceParams) (sqlcgen.Fence, error) {
	if f.updateFenceFn != nil {
		return f.updateFenceFn(ctx, params)
	}
	panic("unexpected call")
}

func (f fakeQueries) UpdateHubMetadata(ctx context.Context, params sqlcgen.UpdateHubMetadataParams) (sqlcgen.HubMetadatum, error) {
	if f.updateHubMetaFn != nil {
		return f.updateHubMetaFn(ctx, params)
	}
	panic("unexpected call")
}

func (f fakeQueries) UpdateProvider(ctx context.Context, params sqlcgen.UpdateProviderParams) (sqlcgen.Provider, error) {
	if f.updateProviderFn != nil {
		return f.updateProviderFn(ctx, params)
	}
	panic("unexpected call")
}

func (f fakeQueries) UpdateTrackable(ctx context.Context, params sqlcgen.UpdateTrackableParams) (sqlcgen.Trackable, error) {
	if f.updateTrackableFn != nil {
		return f.updateTrackableFn(ctx, params)
	}
	panic("unexpected call")
}

func (f fakeQueries) UpdateZone(ctx context.Context, params sqlcgen.UpdateZoneParams) (sqlcgen.Zone, error) {
	if f.updateZoneFn != nil {
		return f.updateZoneFn(ctx, params)
	}
	panic("unexpected call")
}
