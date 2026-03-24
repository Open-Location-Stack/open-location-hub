package hub

import (
	"encoding/json"
	"testing"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
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
