package hub

import (
	"math"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
)

func pointHeight(point gen.Point) (float64, bool) {
	coords, err := point.Coordinates.AsGeoJsonPosition3D()
	if err != nil || len(coords) != 3 {
		return 0, false
	}
	return coords[2], true
}

// Absent extrusion is unbounded within the applicable floor; an explicit zero
// is a plane. Missing location height leaves only the horizontal/floor test.
func heightGap(left gen.Point, le *float64, right gen.Point, re *float64) float64 {
	lz, lok := pointHeight(left)
	rz, rok := pointHeight(right)
	if !lok || !rok || le == nil || re == nil {
		return 0
	}
	return math.Max(0, math.Max(rz-(lz+*le), lz-(rz+*re)))
}

func locationHeightGap(left gen.Location, le *float64, right gen.Location, re *float64) float64 {
	return heightGap(left.Position, le, right.Position, re)
}

func fenceHeightGap(fence gen.Fence, location gen.Location, extrusion *float64) float64 {
	if fence.Extrusion == nil {
		return 0
	}
	var base gen.Point
	if p, err := fence.Region.AsPoint(); err == nil && p.Type == "Point" {
		base = p
	} else {
		polygon, err := fence.Region.AsPolygon()
		if err != nil || len(polygon.Coordinates) == 0 || len(polygon.Coordinates[0]) == 0 {
			return 0
		}
		base = gen.Point{Type: "Point", Coordinates: polygon.Coordinates[0][0]}
	}
	if _, ok := pointHeight(base); !ok {
		xy, err := point2D(base)
		if err != nil {
			return 0
		}
		_ = base.Coordinates.FromGeoJsonPosition3D([]float64{xy[0], xy[1], 0})
	}
	return heightGap(base, fence.Extrusion, location.Position, extrusion)
}

func elevationName[T ~string](ref *T) string {
	if ref == nil || *ref == "" {
		return "floor"
	}
	return string(*ref)
}
