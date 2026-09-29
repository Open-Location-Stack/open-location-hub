package hub

import (
	"math"
	"sort"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
)

// circularIntersection approximates the actual lens shared by the two circular
// extents, rather than returning the midpoint for every overlapping pair. Exact
// tangent contacts and zero-radius objects are represented as a point.
func circularIntersection(left, right gen.Location, lr, rr float64) *gen.CollisionEvent_CollisionArea {
	lp, err := point2D(left.Position)
	if err != nil {
		return nil
	}
	rp, err := point2D(right.Position)
	if err != nil {
		return nil
	}
	sx, sy := 1.0, 1.0
	if collisionUsesWGS84(left, right) {
		sx = metersPerLongitudeDegreeAtLatitude(degreesToRadians((lp[1] + rp[1]) / 2))
		sy = metersPerLatitudeDegree
	}
	x, y := (rp[0]-lp[0])*sx, (rp[1]-lp[1])*sy
	if collisionUsesWGS84(left, right) {
		x = math.Remainder(rp[0]-lp[0], 360) * sx
	}
	d := math.Hypot(x, y)
	toPoint := func(x, y float64) [2]float64 { return [2]float64{lp[0] + x/sx, lp[1] + y/sy} }
	if d > lr+rr+1e-8 {
		return nil
	}
	if lr == 0 || rr == 0 || math.Abs(d-lr-rr) <= 1e-8 {
		px, py := 0.0, 0.0
		if d > 0 {
			px = x * lr / d
			py = y * lr / d
		}
		if rr == 0 {
			px = x
			py = y
		}
		area := collisionAreaPoint(toPoint(px, py))
		return &area
	}
	if d+math.Min(lr, rr) <= math.Max(lr, rr) {
		location, radius := left, lr
		if rr < lr {
			location, radius = right, rr
		}
		polygon := circularGeometry(location, radius)
		area := gen.CollisionEvent_CollisionArea{}
		_ = area.FromLineString(gen.LineString{Type: "LineString", Coordinates: polygon.Coordinates[0]})
		return &area
	}
	points := make([][2]float64, 0, 132)
	for i := 0; i < 64; i++ {
		a := float64(i) * 2 * math.Pi / 64
		px, py := lr*math.Cos(a), lr*math.Sin(a)
		if math.Hypot(px-x, py-y) <= rr {
			points = append(points, [2]float64{px, py})
		}
		px, py = x+rr*math.Cos(a), y+rr*math.Sin(a)
		if math.Hypot(px, py) <= lr {
			points = append(points, [2]float64{px, py})
		}
	}
	// Include the exact circle intersections and both facing arc midpoints, so
	// even a thin lens has a nonzero polygonal area at this fixed resolution.
	a := (lr*lr - rr*rr + d*d) / (2 * d)
	h := math.Sqrt(math.Max(0, lr*lr-a*a))
	ux, uy := x/d, y/d
	points = append(points, [2]float64{a*ux - h*uy, a*uy + h*ux}, [2]float64{a*ux + h*uy, a*uy - h*ux}, [2]float64{lr * ux, lr * uy}, [2]float64{x - rr*ux, y - rr*uy})
	cx, cy := 0.0, 0.0
	for _, p := range points {
		cx += p[0]
		cy += p[1]
	}
	cx /= float64(len(points))
	cy /= float64(len(points))
	sort.Slice(points, func(i, j int) bool {
		return math.Atan2(points[i][1]-cy, points[i][0]-cx) < math.Atan2(points[j][1]-cy, points[j][0]-cx)
	})
	ring := make([]gen.GeoJsonPosition, 0, len(points)+1)
	var previous [2]float64
	for i, p := range points {
		if i > 0 && math.Hypot(p[0]-previous[0], p[1]-previous[1]) < 1e-10 {
			continue
		}
		previous = p
		xy := toPoint(p[0], p[1])
		coord := gen.GeoJsonPosition{}
		_ = coord.FromGeoJsonPosition2D([]float64{xy[0], xy[1]})
		ring = append(ring, coord)
	}
	ring = append(ring, ring[0])
	area := gen.CollisionEvent_CollisionArea{}
	_ = area.FromLineString(gen.LineString{Type: "LineString", Coordinates: ring})
	return &area
}
