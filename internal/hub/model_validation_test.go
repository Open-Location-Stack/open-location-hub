package hub

import (
	"encoding/json"
	"testing"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/google/uuid"
)

func TestRejectInvalidModelEnumsAndNumericValues(t *testing.T) {
	if _, _, err := normalizeProvider(gen.LocationProviderWrite{Id: "provider", Type: "omlox"}, ""); err == nil {
		t.Fatal("omlox is not a provider technology")
	}
	if _, _, err := normalizeTrackable(gen.TrackableWrite{Type: "asset"}, uuid.Nil); err == nil {
		t.Fatal("asset is not a trackable type")
	}
	if _, _, err := normalizeTrackable(gen.TrackableWrite{Type: gen.TrackableWriteTypeVirtual, Radius: float64Ptr(-1)}, uuid.Nil); err == nil {
		t.Fatal("negative radius accepted")
	}
	for _, raw := range []string{
		`{"region":{"type":"Point","coordinates":[181,52]}}`,
		`{"region":{"type":"Point","coordinates":[1,2,3,4]}}`,
		`{"region":{"type":"Point","coordinates":[1,2]},"timeout":-2}`,
		`{"region":{"type":"Point","coordinates":[1,2]},"extrusion":-1}`,
		`{"region":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,1]]]}}`,
		`{"region":{"type":"Point","coordinates":[1,2]},"elevation_ref":"sea"}`,
	} {
		if _, _, err := normalizeFence(json.RawMessage(raw), uuid.Nil); err == nil {
			t.Fatalf("invalid fence accepted: %s", raw)
		}
	}
	for _, value := range []float64{0, 1, -1} {
		var timeout gen.PositiveOrMinusOne
		raw, _ := json.Marshal(value)
		_ = json.Unmarshal(raw, &timeout)
		if err := validDuration("timeout", &timeout); err != nil {
			t.Fatalf("valid timeout %g rejected: %v", value, err)
		}
	}
}

func TestSupportedCoordinateSystems(t *testing.T) {
	for _, crs := range []string{"local", "EPSG:4326", "EPSG:32601", "EPSG:32660", "EPSG:32701", "EPSG:32760", "EPSG:32661", "EPSG:32761"} {
		if err := validateLocationCRS(&crs); err != nil {
			t.Errorf("%s: %v", crs, err)
		}
	}
	for _, crs := range []string{"EPSG:garbage", "EPSG:4979", "EPSG:32662"} {
		if err := validateLocationCRS(&crs); err == nil {
			t.Errorf("accepted unsupported CRS %s", crs)
		}
	}
}

func TestFenceSubdivisionPreservesMetadataAndBoundsEdgeLength(t *testing.T) {
	input := json.RawMessage(`{"crs":"local","zone_id":"zone","name":"test","properties":{"keep":true},"region":{"type":"Polygon","coordinates":[[[0,0,1],[25,0,2],[25,25,3],[0,25,4],[0,0,1]]]}}`)
	raw, err := SubdivideFence(input)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	if doc["name"] != "test" || doc["properties"].(map[string]any)["keep"] != true {
		t.Fatal("subdivision lost metadata")
	}
	var fence gen.FenceWrite
	_ = json.Unmarshal(raw, &fence)
	polygon, _ := fence.Region.AsPolygon()
	ring := polygon.Coordinates[0]
	if len(ring) != 13 {
		t.Fatalf("25m edges should split into three segments: %d vertices", len(ring))
	}
	for i := 1; i < len(ring); i++ {
		a, _ := geoPoint(ring[i-1])
		b, _ := geoPoint(ring[i])
		if pointToSegmentDistance(a, b, b) > 10+1e-8 {
			t.Fatal("subdivision did not bound segment length")
		}
	}
	point := json.RawMessage(`{"region":{"type":"Point","coordinates":[13,52]},"radius":5}`)
	if raw, err := SubdivideFence(point); err != nil || string(raw) != string(point) {
		t.Fatal("subdivision must preserve a point region")
	}
}

func TestExitValidationReportsFieldsInStableOrder(t *testing.T) {
	var invalid gen.PositiveOrMinusOne
	if err := invalid.FromPositiveNumber(-2); err != nil {
		t.Fatal(err)
	}
	if err := validateExitSettings(nil, &invalid, &invalid, &invalid); err == nil || err.Error() != "timeout must be nonnegative or -1" {
		t.Fatalf("unexpected first validation error: %v", err)
	}
}

func TestPointValidationChecksDecodedCoordinateDimensions(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{`[13,52]`, true}, {`[13,52,4]`, true}, {`[13]`, false},
		{`[13,52,4,5]`, false}, {`[181,52]`, false}, {`[13,"52"]`, false},
	} {
		var coords gen.GeoJsonPosition
		if err := json.Unmarshal([]byte(tc.raw), &coords); err != nil {
			t.Fatal(err)
		}
		err := validatePoint(gen.Point{Type: "Point", Coordinates: coords}, true)
		if (err == nil) != tc.valid {
			t.Fatalf("coordinates %s: %v", tc.raw, err)
		}
	}
}
