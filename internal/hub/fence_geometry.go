package hub

import (
	"context"
	"math"
	"strings"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
)

// fenceContainmentForLocation compares a location and fence already expressed
// in the same coordinate frame. Radius and the returned separation are meters.
// Polygon holes are excluded, and a touching circular extent counts as inside.
func fenceContainmentForLocation(fence gen.Fence, location gen.Location, radius float64) (fenceContainment, error) {
	point, err := point2D(location.Position)
	if err != nil {
		return fenceContainment{}, err
	}
	geographic := fence.Crs == nil || *fence.Crs == "" || *fence.Crs == "EPSG:4326"
	if geographic {
		origin := point
		project := func(p gen.Point) (gen.Point, error) {
			xy, e := point2D(p)
			if e != nil {
				return gen.Point{}, e
			}
			x := math.Remainder(xy[0]-origin[0], 360) * metersPerLongitudeDegreeAtLatitude(degreesToRadians(origin[1]))
			y := (xy[1] - origin[1]) * metersPerLatitudeDegree
			e = p.Coordinates.FromGeoJsonPosition2D([]float64{x, y})
			return p, e
		}
		if p, e := fence.Region.AsPoint(); e == nil && p.Type == "Point" {
			p, e = project(p)
			if e != nil {
				return fenceContainment{}, e
			}
			if e = fence.Region.FromPoint(p); e != nil {
				return fenceContainment{}, e
			}
		} else {
			polygon, e := fence.Region.AsPolygon()
			if e != nil {
				return fenceContainment{}, e
			}
			for i, ring := range polygon.Coordinates {
				for j, coord := range ring {
					p, e := project(gen.Point{Type: "Point", Coordinates: coord})
					if e != nil {
						return fenceContainment{}, e
					}
					polygon.Coordinates[i][j] = p.Coordinates
				}
			}
			if e = fence.Region.FromPolygon(polygon); e != nil {
				return fenceContainment{}, e
			}
		}
		point = [2]float64{}
	}
	containment, err := fenceContainmentForPoint(fence, point)
	if err != nil {
		return fenceContainment{}, err
	}
	if !containment.Inside {
		containment.OutsideDistance = math.Max(0, containment.OutsideDistance-math.Max(0, radius))
		containment.Inside = containment.OutsideDistance <= 1e-8
	}
	return containment, nil
}

// fenceIntersectsLocation projects spatial REST queries before comparison; raw
// coordinates from unrelated zones must never be compared as if they coincide.
func (s *Service) fenceIntersectsLocation(ctx context.Context, fence gen.Fence, location gen.Location, radius float64, extrusions ...*float64) bool {
	if !fenceMatchesFloor(fence, location) {
		return false
	}
	crs := strings.TrimSpace(stringPtrValue(fence.Crs))
	if crs == "" {
		crs = "EPSG:4326"
	}
	same := crs == locationCRS(location)
	if same && crs == "local" {
		zone, err := s.zoneForLocationSource(ctx, location)
		same = err == nil && fence.ZoneId != nil && *fence.ZoneId == zone.Id.String()
	}
	if !same {
		var err error
		location, err = s.ProjectLocation(ctx, location, crs, stringPtrValue(fence.ZoneId))
		if err != nil {
			return false
		}
	}
	containment, err := fenceContainmentForLocation(fence, location, radius)
	zero := 0.0
	extrusion := &zero
	if len(extrusions) > 0 {
		extrusion = extrusions[0]
	}
	return err == nil && containment.Inside && fenceHeightGap(fence, location, extrusion) == 0 && elevationName(fence.ElevationRef) == elevationName(location.ElevationRef)
}

// circularGeometry is the polygonal approximation required by section 9.1.
// The closing vertex is copied exactly to keep valid GeoJSON rings.
func circularGeometry(location gen.Location, radius float64) *gen.Polygon {
	center, err := point2D(location.Position)
	if err != nil {
		return nil
	}
	dx, dy := collisionMetersToCoordinateOffsets(locationCRS(location), center, math.Max(0, radius))
	const segments = 64
	ring := make([]gen.GeoJsonPosition, segments+1)
	z, hasZ := pointHeight(location.Position)
	for i := 0; i < segments; i++ {
		angle := 2 * math.Pi * float64(i) / segments
		xy := []float64{center[0] + dx*math.Cos(angle), center[1] + dy*math.Sin(angle)}
		if hasZ {
			_ = ring[i].FromGeoJsonPosition3D(append(xy, z))
		} else {
			_ = ring[i].FromGeoJsonPosition2D(xy)
		}
	}
	ring[segments] = ring[0]
	return &gen.Polygon{Type: "Polygon", Coordinates: [][]gen.GeoJsonPosition{ring}}
}

func trackableRadius(trackable gen.Trackable) float64 {
	if trackable.Radius != nil {
		return math.Max(0, *trackable.Radius)
	}
	return 0
}
