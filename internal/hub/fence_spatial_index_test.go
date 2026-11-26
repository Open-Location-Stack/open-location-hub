package hub

import (
	"slices"
	"testing"

	"github.com/dhconnelly/rtreego"
	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/google/uuid"
)

func TestFenceCellsMatchWholeSpaceIndex(t *testing.T) {
	t.Parallel()
	index := newFenceSpatialIndex("local:facility")
	rectangle := func(minX, minY, width, height float64) rtreego.Rect {
		t.Helper()
		r, err := rtreego.NewRect(rtreego.Point{minX, minY}, []float64{width, height})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	for _, rect := range []rtreego.Rect{
		rectangle(-10, -10, 20, 20),
		rectangle(250, 250, 20, 20),
		rectangle(1001, 1001, 2, 2),
		rectangle(-20_000, -20_000, 40_000, 40_000),
	} {
		index.insert(indexedFence{fence: gen.Fence{Id: uuid.New()}, rect: rect})
	}
	ids := func(entries []rtreego.Spatial) []string {
		result := make([]string, 0, len(entries))
		for _, entry := range entries {
			result = append(result, entry.(indexedFence).fence.Id.String())
		}
		slices.Sort(result)
		return result
	}
	for _, query := range []rtreego.Rect{
		rectangle(-1, -1, 2, 2),
		rectangle(255, 255, 2, 2),
		rectangle(256, 256, 2, 2),
		rectangle(10_000, 10_000, 2, 2),
		rectangle(-15_000, -15_000, 16_500, 16_500),
	} {
		if got, want := ids(index.search(query)), ids(index.all.SearchIntersect(query)); !slices.Equal(got, want) {
			t.Fatalf("query %v: cell candidates %v, whole-space candidates %v", query, got, want)
		}
	}
}
