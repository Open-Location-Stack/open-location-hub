package hub

import (
	"math"

	"github.com/dhconnelly/rtreego"
	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
)

type indexedCollisionMotion struct {
	motion   gen.TrackableMotion
	point    [2]float64
	position rtreego.Point
	space    string
}
type collisionIndexEntry struct {
	indexedCollisionMotion
	rect rtreego.Rect
}

func (e *collisionIndexEntry) Bounds() rtreego.Rect { return e.rect }

type collisionSpatialIndex struct {
	tree    *rtreego.Rtree
	entries map[string]*collisionIndexEntry
}

func newCollisionSpatialIndex(motions []indexedCollisionMotion) collisionSpatialIndex {
	index := collisionSpatialIndex{tree: rtreego.NewTree(3, 8, 32), entries: map[string]*collisionIndexEntry{}}
	for _, motion := range motions {
		index.add(motion)
	}
	return index
}
func (i collisionSpatialIndex) add(motion indexedCollisionMotion) {
	if old := i.entries[motion.motion.Id]; old != nil {
		i.tree.Delete(old)
	}
	entry := &collisionIndexEntry{indexedCollisionMotion: motion, rect: motion.position.ToRect(1e-7)}
	i.entries[motion.motion.Id] = entry
	i.tree.Insert(entry)
}
func (i collisionSpatialIndex) Nearby(location gen.Location, point [2]float64, distance float64) []indexedCollisionMotion {
	position, space, ok := collisionIndexPosition(location, point)
	if !ok {
		return nil
	}
	// Earth-centered coordinates avoid date-line discontinuities and polar
	// Mercator distortion. Chord distance underestimates surface distance;
	// the margin also covers the short-range planar narrow-phase approximation.
	radius := math.Max(1e-6, distance*1.02)
	query := position.ToRect(radius)
	results := i.tree.SearchIntersect(query)
	out := make([]indexedCollisionMotion, 0, len(results))
	for _, item := range results {
		entry := item.(*collisionIndexEntry)
		if entry.space == space {
			out = append(out, entry.indexedCollisionMotion)
		}
	}
	return out
}
func newIndexedCollisionMotion(motion gen.TrackableMotion, point [2]float64) (indexedCollisionMotion, bool) {
	position, space, ok := collisionIndexPosition(motion.Location, point)
	return indexedCollisionMotion{motion: motion, point: point, position: position, space: space}, ok
}
func collisionIndexPosition(location gen.Location, point [2]float64) (rtreego.Point, string, bool) {
	crs := locationCRS(location)
	if crs == "EPSG:4326" {
		if point[0] < -180 || point[0] > 180 || point[1] < -90 || point[1] > 90 {
			return nil, crs, false
		}
		const radius = 6371008.8
		lon, lat := degreesToRadians(point[0]), degreesToRadians(point[1])
		return rtreego.Point{radius * math.Cos(lat) * math.Cos(lon), radius * math.Cos(lat) * math.Sin(lon), radius * math.Sin(lat)}, crs, true
	}
	return rtreego.Point{point[0], point[1], 0}, crs, true
}
