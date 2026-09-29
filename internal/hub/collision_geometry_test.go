package hub

import (
	"math"
	"testing"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
)

func TestCollisionIntersectionUsesActualBoundaryAndTangentPoint(t *testing.T) {
	crs := "local"
	left := testLocationWithCoordinates(t, &crs, "zone", [2]float64{0, 0})
	right := testLocationWithCoordinates(t, &crs, "zone", [2]float64{3, 0})
	area := circularIntersection(left, right, 2, 1)
	point, err := area.AsPoint()
	if err != nil || point.Type != "Point" {
		t.Fatalf("expected tangent point: %v", err)
	}
	xy, _ := point2D(point)
	if xy != [2]float64{2, 0} {
		t.Fatalf("tangent is not midpoint for unequal radii: %v", xy)
	}
	_ = right.Position.Coordinates.FromGeoJsonPosition2D([]float64{2, 0})
	area = circularIntersection(left, right, 2, 1)
	line, err := area.AsLineString()
	if err != nil || line.Type != "LineString" || len(line.Coordinates) < 4 {
		t.Fatalf("expected closed lens boundary: %+v %v", line, err)
	}
	for _, p := range line.Coordinates {
		xy, _ := geoPoint(p)
		if math.Hypot(xy[0], xy[1]) > 2+1e-8 || math.Hypot(xy[0]-2, xy[1]) > 1+1e-8 {
			t.Fatalf("intersection vertex lies outside a circle: %v", xy)
		}
	}
	first, _ := geoPoint(line.Coordinates[0])
	last, _ := geoPoint(line.Coordinates[len(line.Coordinates)-1])
	if first != last {
		t.Fatal("open intersection boundary")
	}
}

func TestCollisionIndexHandlesDateLineAndPolarNeighbors(t *testing.T) {
	crs := "EPSG:4326"
	for _, tc := range []struct {
		name        string
		left, right [2]float64
	}{
		{"date-line", [2]float64{179.9999, 0}, [2]float64{-179.9999, 0}},
		{"polar", [2]float64{10, 89.999}, [2]float64{11, 89.999}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := gen.TrackableMotion{Id: "other", Location: testLocationWithCoordinates(t, &crs, "other", tc.right)}
			entry, ok := newIndexedCollisionMotion(other, tc.right)
			if !ok {
				t.Fatal("valid geographic motion rejected")
			}
			index := newCollisionSpatialIndex([]indexedCollisionMotion{entry})
			query := testLocationWithCoordinates(t, &crs, "query", tc.left)
			if got := index.Nearby(query, tc.left, 30); len(got) != 1 {
				t.Fatalf("missed neighbor: %v", got)
			}
		})
	}
}
