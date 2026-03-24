package hub

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/google/uuid"
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

func uuidAsOpenAPI(id uuid.UUID) [16]byte {
	return [16]byte(id)
}

func float32Ptr(value float32) *float32 {
	return &value
}
