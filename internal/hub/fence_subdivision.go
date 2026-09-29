package hub

import (
	"encoding/json"
	"math"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
)

// SubdivideFence densifies submitted polygon edges before a nonlinear coordinate
// transformation. Ten meters is this implementation's choice: the OMLOX JSON
// requires subdivision support but does not prescribe a segment length.
func SubdivideFence(body json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, badRequest("invalid fence payload")
	}
	var fence gen.FenceWrite
	if err := json.Unmarshal(body, &fence); err != nil {
		return nil, badRequest("invalid fence payload")
	}
	if point, err := fence.Region.AsPoint(); err == nil && point.Type == "Point" {
		return body, nil
	}
	polygon, err := fence.Region.AsPolygon()
	if err != nil {
		return nil, badRequest("invalid fence region")
	}
	geographic := fence.Crs == nil || *fence.Crs == "" || *fence.Crs == "EPSG:4326"
	if err = validatePolygon(polygon, geographic); err != nil {
		return nil, err
	}
	const maxVertices = 100000
	count := 0
	for r, ring := range polygon.Coordinates {
		out := make([]gen.GeoJsonPosition, 0, len(ring))
		for i := 1; i < len(ring); i++ {
			start, _ := geoPoint(ring[i-1])
			end, _ := geoPoint(ring[i])
			dx, dy := end[0]-start[0], end[1]-start[1]
			metersX, metersY := dx, dy
			if geographic {
				dx = math.Remainder(dx, 360)
				metersX = dx * metersPerLongitudeDegreeAtLatitude(degreesToRadians((start[1]+end[1])/2))
				metersY = dy * metersPerLatitudeDegree
			}
			segments := math.Ceil(math.Hypot(metersX, metersY) / 10)
			if segments < 1 {
				segments = 1
			}
			if segments > float64(maxVertices-count-1) {
				return nil, badRequest("subdivision exceeds 100000 vertices")
			}
			sz, sh := pointHeight(gen.Point{Coordinates: ring[i-1]})
			ez, eh := pointHeight(gen.Point{Coordinates: ring[i]})
			for j := 0; j < int(segments); j++ {
				if j == 0 {
					out = append(out, ring[i-1])
					continue
				}
				t := float64(j) / segments
				x, y := start[0]+t*dx, start[1]+t*dy
				if geographic {
					x = math.Remainder(x, 360)
				}
				coord := gen.GeoJsonPosition{}
				if sh || eh {
					_ = coord.FromGeoJsonPosition3D([]float64{x, y, sz + t*(ez-sz)})
				} else {
					_ = coord.FromGeoJsonPosition2D([]float64{x, y})
				}
				out = append(out, coord)
			}
			count += int(segments)
		}
		out = append(out, ring[len(ring)-1])
		count++
		polygon.Coordinates[r] = out
	}
	fields["region"], err = json.Marshal(polygon)
	if err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}
