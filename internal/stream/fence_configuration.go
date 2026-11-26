package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/hub"
	"github.com/formation-res/open-location-hub/internal/storage/postgres/sqlcgen"
	"github.com/google/uuid"
)

type fenceConfiguration struct {
	zones   map[string]gen.Zone
	fences  map[string]gen.Fence
	catalog *hub.FenceCatalog
}

func loadFenceConfiguration(ctx context.Context, q *sqlcgen.Queries) (*fenceConfiguration, error) {
	zones, err := q.ListZones(ctx)
	if err != nil {
		return nil, err
	}
	fences, err := q.ListFences(ctx)
	if err != nil {
		return nil, err
	}
	geometry := &fenceConfiguration{zones: make(map[string]gen.Zone, len(zones)), fences: make(map[string]gen.Fence, len(fences))}
	for _, row := range zones {
		id := row.ID.String()
		var zone gen.Zone
		if err := decodeFenceResource(id, row.Payload, &zone); err != nil {
			return nil, err
		}
		geometry.zones[id] = zone
	}
	for _, row := range fences {
		id := row.ID.String()
		var fence gen.Fence
		if err := decodeFenceResource(id, row.Payload, &fence); err != nil {
			return nil, err
		}
		geometry.fences[id] = fence
	}
	geometry.rebuild()
	return geometry, nil
}

func decodeFenceResource(id string, payload []byte, resource any) error {
	identifier, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("invalid spatial resource ID %q: %w", id, err)
	}
	if err := json.Unmarshal(payload, resource); err != nil {
		return fmt.Errorf("decode spatial resource %s: %w", id, err)
	}
	switch value := resource.(type) {
	case *gen.Zone:
		value.Id = identifier
	case *gen.Fence:
		value.Id = identifier
	}
	return nil
}

func (f *fenceConfiguration) withChanges(changes []locatingChange) *fenceConfiguration {
	next := *f
	updateZones, updateFences := false, false
	for _, change := range changes {
		switch change.kind {
		case "zone":
			if !updateZones {
				next.zones = maps.Clone(next.zones)
				updateZones = true
			}
			if change.remove {
				delete(next.zones, change.id)
			} else {
				next.zones[change.id] = change.zone
			}
		case "fence":
			if !updateFences {
				next.fences = maps.Clone(next.fences)
				updateFences = true
			}
			if change.remove {
				delete(next.fences, change.id)
			} else {
				next.fences[change.id] = change.fence
			}
		}
	}
	if !updateZones && !updateFences {
		return f
	}
	next.rebuild()
	return &next
}

func (f *fenceConfiguration) rebuild() {
	// Order construction so overlapping bounds have repeatable indexing behavior.
	zones := make([]gen.Zone, 0, len(f.zones))
	for _, id := range slices.Sorted(maps.Keys(f.zones)) {
		zones = append(zones, f.zones[id])
	}
	fences := make([]gen.Fence, 0, len(f.fences))
	for _, id := range slices.Sorted(maps.Keys(f.fences)) {
		fences = append(fences, f.fences[id])
	}
	f.catalog = hub.NewFenceCatalog(zones, fences)
}
