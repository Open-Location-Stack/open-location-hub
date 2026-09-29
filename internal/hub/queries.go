package hub

import (
	"context"
	"strings"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/transform"
	"github.com/google/uuid"
)

// ProjectLocation expresses a stored location in the requested CRS and target zone.
// An omitted CRS requests WGS84. Local without a target preserves source coordinates.
func (s *Service) ProjectLocation(ctx context.Context, location gen.Location, crs, zoneID string) (gen.Location, error) {
	crs = strings.TrimSpace(crs)
	if crs == "" {
		crs = "EPSG:4326"
	}
	if zoneID != "" && crs != "local" {
		return gen.Location{}, badRequest("zone_id requires crs=local")
	}
	if crs == "local" && zoneID == "" {
		return cloneLocation(location)
	}
	wgs, err := s.locationToWGS84(ctx, location)
	if err != nil {
		return gen.Location{}, err
	}
	if crs == "EPSG:4326" {
		return wgs, nil
	}
	if crs == "local" {
		id, err := uuid.Parse(zoneID)
		if err != nil {
			return gen.Location{}, badRequest("zone_id must be a UUID")
		}
		zone, err := s.GetZone(ctx, id)
		if err != nil {
			return gen.Location{}, err
		}
		cache := s.transformCache
		if cache == nil {
			cache = transform.NewCache()
		}
		transformer, err := cache.Get(zone)
		if err != nil {
			return gen.Location{}, badRequest(err.Error())
		}
		wgs.Position, err = transformer.WGS84ToLocal(wgs.Position)
		if err != nil {
			return gen.Location{}, badRequest(err.Error())
		}
		wgs.Crs = &crs
		return wgs, nil
	}
	if err = validateLocationCRS(&crs); err != nil {
		return gen.Location{}, err
	}
	projector := s.crsTransformer
	if projector == nil {
		projector = transform.NewCRSTransformer()
	}
	wgs.Position, err = projector.FromWGS84(crs, wgs.Position)
	if err != nil {
		return gen.Location{}, badRequest(err.Error())
	}
	wgs.Crs = &crs
	return wgs, nil
}

// ProjectFence transforms every fence vertex to a requested coordinate system.
func (s *Service) ProjectFence(ctx context.Context, fence gen.Fence, crs, zoneID string) (gen.Fence, error) {
	if crs == "local" && zoneID == "" {
		return fence, nil
	}
	source := ""
	if fence.ZoneId != nil {
		source = *fence.ZoneId
	}
	inputCRS := "EPSG:4326"
	if fence.Crs != nil {
		inputCRS = *fence.Crs
	}
	project := func(point gen.Point) (gen.Point, error) {
		location, err := s.ProjectLocation(ctx, gen.Location{Position: point, Source: source, Crs: &inputCRS}, crs, zoneID)
		return location.Position, err
	}
	if point, err := fence.Region.AsPoint(); err == nil && point.Type == "Point" {
		p, e := project(point)
		if e != nil {
			return gen.Fence{}, e
		}
		if e = fence.Region.FromPoint(p); e != nil {
			return gen.Fence{}, e
		}
	} else {
		polygon, e := fence.Region.AsPolygon()
		if e != nil {
			return gen.Fence{}, badRequest("invalid fence geometry")
		}
		for i, ring := range polygon.Coordinates {
			for j, coord := range ring {
				p, e := project(gen.Point{Type: "Point", Coordinates: coord})
				if e != nil {
					return gen.Fence{}, e
				}
				polygon.Coordinates[i][j] = p.Coordinates
			}
		}
		if e = fence.Region.FromPolygon(polygon); e != nil {
			return gen.Fence{}, e
		}
	}
	if crs == "" {
		crs = "EPSG:4326"
	}
	fence.Crs = &crs
	if crs == "local" && zoneID != "" {
		fence.ZoneId = &zoneID
	} else if crs != "local" {
		fence.ZoneId = nil
	}
	return fence, nil
}

// ListProviderTrackables returns the trackables associated with an existing provider.
func (s *Service) ListProviderTrackables(ctx context.Context, id gen.ProviderId) ([]gen.Trackable, error) {
	if _, err := s.GetProvider(ctx, id); err != nil {
		return nil, err
	}
	if cache := s.metadataCache(); cache != nil {
		out := cache.TrackablesByProviderID(id)
		if out == nil {
			out = []gen.Trackable{}
		}
		return out, nil
	}
	all, err := s.ListTrackables(ctx)
	if err != nil {
		return nil, err
	}
	out := trackablesForProvider(all, id)
	if out == nil {
		out = []gen.Trackable{}
	}
	return out, nil
}

// ListFenceTrackables returns fence members, or geometric matches for spatial queries.
func (s *Service) ListFenceTrackables(ctx context.Context, id gen.FenceId, spatial bool) ([]gen.Trackable, error) {
	fence, err := s.GetFence(ctx, id)
	if err != nil {
		return nil, err
	}
	all, err := s.ListTrackables(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Trackable, 0)
	for _, t := range all {
		inside := s.processingState().IsInsideFence(t.Id.String(), id.String())
		if spatial {
			loc, e := s.GetTrackableLocation(ctx, t.Id)
			inside = e == nil && s.fenceIntersectsLocation(ctx, fence, loc, trackableRadius(t), t.Extrusion)
		}
		if inside {
			out = append(out, t)
		}
	}
	return out, nil
}

// ListFenceMotions returns the selected motions of trackables inside a fence.
func (s *Service) ListFenceMotions(ctx context.Context, id gen.FenceId, spatial bool) ([]gen.TrackableMotion, error) {
	trackables, err := s.ListFenceTrackables(ctx, id, spatial)
	if err != nil {
		return nil, err
	}
	out := make([]gen.TrackableMotion, 0, len(trackables))
	for _, t := range trackables {
		motion, err := s.GetTrackableMotion(ctx, t.Id)
		if err == nil {
			out = append(out, motion)
		}
	}
	return out, nil
}

// ValidateProjection checks subscription/query projection parameters before data arrives.
func (s *Service) ValidateProjection(ctx context.Context, crs, zoneID string) error {
	if err := validateSupportedCRS(&crs); err != nil {
		return err
	}
	if zoneID == "" {
		return nil
	}
	if crs != "local" {
		return badRequest("zone_id requires crs=local")
	}
	id, err := uuid.Parse(zoneID)
	if err != nil {
		return badRequest("zone_id must be a UUID")
	}
	zone, err := s.GetZone(ctx, id)
	if err != nil {
		return err
	}
	_, err = transform.NewLocalTransformer(zone)
	if err != nil {
		return badRequest(err.Error())
	}
	return nil
}

// ProjectMotion projects both the observation and its optional polygon.
func (s *Service) ProjectMotion(ctx context.Context, motion gen.TrackableMotion, crs, zoneID string) (gen.TrackableMotion, error) {
	original := motion.Location
	projected, err := s.ProjectLocation(ctx, original, crs, zoneID)
	if err != nil {
		return gen.TrackableMotion{}, err
	}
	motion.Location = projected
	if motion.Geometry != nil {
		inputCRS := "local"
		if original.Crs != nil {
			inputCRS = *original.Crs
		}
		fence := gen.Fence{Crs: &inputCRS, ZoneId: &original.Source}
		if err = fence.Region.FromPolygon(*motion.Geometry); err != nil {
			return gen.TrackableMotion{}, err
		}
		fence, err = s.ProjectFence(ctx, fence, crs, zoneID)
		if err != nil {
			return gen.TrackableMotion{}, err
		}
		polygon, err := fence.Region.AsPolygon()
		if err != nil {
			return gen.TrackableMotion{}, err
		}
		motion.Geometry = &polygon
	}
	return motion, nil
}

// ProjectCollision projects a WGS84 collision snapshot and its intersection boundary.
func (s *Service) ProjectCollision(ctx context.Context, event gen.CollisionEvent, crs, zoneID string) (gen.CollisionEvent, error) {
	inputCRS := "EPSG:4326"
	point := func(p gen.Point) (gen.Point, error) {
		l, e := s.ProjectLocation(ctx, gen.Location{Position: p, Crs: &inputCRS}, crs, zoneID)
		return l.Position, e
	}
	event.Collisions = append([]gen.Collision(nil), event.Collisions...)
	for i, c := range event.Collisions {
		var err error
		c.Position, err = point(c.Position)
		if err != nil {
			return gen.CollisionEvent{}, err
		}
		f := gen.Fence{Crs: &inputCRS}
		if err = f.Region.FromPolygon(c.Geometry); err != nil {
			return gen.CollisionEvent{}, err
		}
		f, err = s.ProjectFence(ctx, f, crs, zoneID)
		if err != nil {
			return gen.CollisionEvent{}, err
		}
		c.Geometry, err = f.Region.AsPolygon()
		if err != nil {
			return gen.CollisionEvent{}, err
		}
		event.Collisions[i] = c
	}
	if event.CollisionArea != nil {
		area := *event.CollisionArea
		if p, e := area.AsPoint(); e == nil && p.Type == "Point" {
			p, e = point(p)
			if e != nil {
				return gen.CollisionEvent{}, e
			}
			if e = area.FromPoint(p); e != nil {
				return gen.CollisionEvent{}, e
			}
		} else {
			line, e := area.AsLineString()
			if e != nil {
				return gen.CollisionEvent{}, e
			}
			for i, c := range line.Coordinates {
				p, e := point(gen.Point{Type: "Point", Coordinates: c})
				if e != nil {
					return gen.CollisionEvent{}, e
				}
				line.Coordinates[i] = p.Coordinates
			}
			if e = area.FromLineString(line); e != nil {
				return gen.CollisionEvent{}, e
			}
		}
		event.CollisionArea = &area
	}
	return event, nil
}

// LocationGeoJSON wraps an observation in the OMLOX feature collection envelope.
func LocationGeoJSON(location gen.Location) GeoJSONFeatureCollection {
	result, _ := locationGeoJSONFeatureCollection(location)
	return result
}
