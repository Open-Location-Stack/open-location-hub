package hub

import (
	"math"

	"github.com/dhconnelly/rtreego"
)

const fenceCellLimit = 64

type fenceCell struct {
	x, y int64
}

// fenceSpatialIndex is immutable after a metadata snapshot is published.
type fenceSpatialIndex struct {
	cellWidth float64
	cells     map[fenceCell]*rtreego.Rtree
	wide      *rtreego.Rtree
	all       *rtreego.Rtree
}

func newFenceSpatialIndex(scope string) *fenceSpatialIndex {
	width := 256.0
	if scope == "crs:EPSG:4326" {
		width = 0.25
	}
	return &fenceSpatialIndex{
		cellWidth: width,
		cells:     make(map[fenceCell]*rtreego.Rtree),
		wide:      newFenceTree(),
		all:       newFenceTree(),
	}
}

func newFenceTree() *rtreego.Rtree {
	return rtreego.NewTree(fenceIndexDimensions, fenceIndexMinEntries, fenceIndexMaxEntries)
}

func (i *fenceSpatialIndex) insert(entry indexedFence) {
	i.all.Insert(entry)
	low, high, bounded := i.cellRange(entry.rect)
	if !bounded {
		i.wide.Insert(entry)
		return
	}
	for x := low.x; x <= high.x; x++ {
		for y := low.y; y <= high.y; y++ {
			key := fenceCell{x, y}
			tree := i.cells[key]
			if tree == nil {
				tree = newFenceTree()
				i.cells[key] = tree
			}
			tree.Insert(entry)
		}
	}
}

func (i *fenceSpatialIndex) search(query rtreego.Rect) []rtreego.Spatial {
	low, high, bounded := i.cellRange(query)
	if !bounded {
		return i.all.SearchIntersect(query)
	}
	results := i.wide.SearchIntersect(query)
	if low == high {
		if tree := i.cells[low]; tree != nil {
			results = append(results, tree.SearchIntersect(query)...)
		}
		return results
	}

	seen := make(map[string]struct{})
	for x := low.x; x <= high.x; x++ {
		for y := low.y; y <= high.y; y++ {
			if tree := i.cells[fenceCell{x, y}]; tree != nil {
				for _, result := range tree.SearchIntersect(query) {
					id := result.(indexedFence).fence.Id.String()
					if _, duplicate := seen[id]; !duplicate {
						seen[id] = struct{}{}
						results = append(results, result)
					}
				}
			}
		}
	}
	return results
}

func (i *fenceSpatialIndex) cellRange(rect rtreego.Rect) (low, high fenceCell, bounded bool) {
	minX, minY := rect.PointCoord(0), rect.PointCoord(1)
	x0 := math.Floor(math.Nextafter(minX/i.cellWidth, math.Inf(-1)))
	y0 := math.Floor(math.Nextafter(minY/i.cellWidth, math.Inf(-1)))
	x1 := math.Floor(math.Nextafter((minX+rect.LengthsCoord(0))/i.cellWidth, math.Inf(1)))
	y1 := math.Floor(math.Nextafter((minY+rect.LengthsCoord(1))/i.cellWidth, math.Inf(1)))
	cells := (x1 - x0 + 1) * (y1 - y0 + 1)
	const exactIntegerLimit = 1 << 52
	if !(cells >= 1 && cells <= fenceCellLimit) ||
		x0 < -exactIntegerLimit || y0 < -exactIntegerLimit ||
		x1 > exactIntegerLimit || y1 > exactIntegerLimit {
		return fenceCell{}, fenceCell{}, false
	}
	return fenceCell{int64(x0), int64(y0)}, fenceCell{int64(x1), int64(y1)}, true
}
