package hub

import (
	"context"
	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"time"
)

// applyLocationDefaults implements the published location object and section 7.9
// defaults without replacing explicitly supplied floor/elevation metadata.
func (s *Service) applyLocationDefaults(ctx context.Context, location gen.Location) gen.Location {
	if location.TimestampGenerated == nil {
		now := time.Now().UTC()
		if s.now != nil {
			now = s.now().UTC()
		}
		location.TimestampGenerated = &now
	}
	if location.Crs == nil || *location.Crs == "" {
		crs := "local"
		location.Crs = &crs
	}
	if location.ElevationRef == nil {
		ref := gen.LocationElevationRefFloor
		location.ElevationRef = &ref
	}
	if location.Floor == nil {
		floor := 0.0
		if s.metadata != nil || s.queries != nil {
			if zone, err := s.zoneForLocationSource(ctx, location); err == nil && zone.Floor != nil {
				floor = *zone.Floor
			}
		}
		location.Floor = &floor
	}
	return location
}
