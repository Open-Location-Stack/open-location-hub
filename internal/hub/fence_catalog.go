package hub

import (
	"context"
	"encoding/json"
	"math"

	"github.com/dhconnelly/rtreego"
	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
)

// FenceCatalog is an immutable lookup of native and projected geofence geometry.
type FenceCatalog struct {
	snapshot     metadataSnapshot
	projector    *Service
	nativeBounds map[string]rtreego.Rect
	worldBounds  map[string]rtreego.Rect
	worldWrapped []gen.Fence
	nativeJSON   map[string][]byte
	worldJSON    map[string][]byte
}

// NewFenceCatalog builds a spatial index from one committed configuration.
func NewFenceCatalog(zones []gen.Zone, fences []gen.Fence) *FenceCatalog {
	zoneRows := make([]zoneRecord, 0, len(zones))
	fenceRows := make([]fenceRecord, 0, len(fences))
	for _, zone := range zones {
		zoneRows = append(zoneRows, zoneRecord{Zone: zone})
	}
	for _, fence := range fences {
		fenceRows = append(fenceRows, fenceRecord{Fence: fence})
	}
	snapshot := newMetadataSnapshot(zoneRows, fenceRows, nil, nil)
	catalog := &FenceCatalog{
		snapshot: snapshot, projector: snapshot.fenceProjector(),
		nativeBounds: make(map[string]rtreego.Rect, len(fences)),
		worldBounds:  make(map[string]rtreego.Rect, len(fences)),
		nativeJSON:   make(map[string][]byte, len(fences)),
		worldJSON:    make(map[string][]byte, len(fences)),
	}
	for id, fence := range snapshot.fencesByID {
		if bounds, ok := fenceBoundingRect(fence); ok {
			catalog.nativeBounds[id] = bounds
		}
		catalog.nativeJSON[id], _ = json.Marshal(fence)
	}
	for id, fence := range snapshot.worldFencesByID {
		catalog.worldJSON[id], _ = json.Marshal(fence)
		if bounds, ok := fenceBoundingRect(fence); ok {
			catalog.worldBounds[id] = bounds
			start, width := bounds.PointCoord(0), bounds.LengthsCoord(0)
			if start < -180 || start+width > 180 || width > 180 {
				catalog.worldWrapped = append(catalog.worldWrapped, fence)
			}
		}
	}
	return catalog
}

// Frame projects the observation to WGS84 where a transform is available.
func (c *FenceCatalog) Frame(ctx context.Context, location gen.Location) (gen.Location, bool) {
	if locationCRS(location) == "EPSG:4326" {
		return location, false
	}
	if world, err := c.projector.locationToWGS84(ctx, location); err == nil {
		return world, true
	}
	return location, false
}

// Encoded returns the snapshot's cached JSON for a resolved fence.
func (c *FenceCatalog) Encoded(id string, location gen.Location) []byte {
	if locationCRS(location) == "EPSG:4326" {
		if _, ok := c.snapshot.worldFencesByID[id]; ok {
			return c.worldJSON[id]
		}
	}
	return c.nativeJSON[id]
}

// Candidates returns fences whose bounding boxes intersect the asset's extent.
func (c *FenceCatalog) Candidates(location gen.Location, trackable gen.Trackable) ([]gen.Fence, error) {
	snapshot := c.snapshot
	if locationCRS(location) == "EPSG:4326" {
		snapshot.fenceIndexes = snapshot.worldFenceIndexes
		matches, err := snapshot.fenceCandidatesWithRadius(location, trackableRadius(trackable))
		for _, fence := range c.worldWrapped {
			if fenceMatchesFloor(fence, location) && !c.CertainlyOutside(fence, location, trackableRadius(trackable)) {
				matches = append(matches, fence)
			}
		}
		return matches, err
	}
	return snapshot.fenceCandidatesWithRadius(location, trackableRadius(trackable))
}

// Resolve returns a previously routed fence in the observation's coordinate frame.
// The existence and comparability flags let callers distinguish deletion from
// movement into an unrelated frame.
func (c *FenceCatalog) Resolve(id string, location gen.Location) (fence gen.Fence, exists, comparable bool) {
	fence, exists = c.snapshot.fencesByID[id]
	if !exists {
		return fence, false, false
	}
	if world, ok := c.snapshot.worldFencesByID[id]; ok && locationCRS(location) == "EPSG:4326" {
		fence = world
	}
	locationScope, ok, _ := c.snapshot.locationFenceScopeKey(location)
	fenceScope, valid := fenceScopeKey(fence)
	return fence, true, ok && valid && locationScope == fenceScope &&
		elevationName(fence.ElevationRef) == elevationName(location.ElevationRef)
}

// FenceRoutingPolicy is the effective geometry and timeout policy for one input.
type FenceRoutingPolicy struct {
	Radius                                                   float64
	ExitTolerance                                            float64
	FenceTimeoutNanos, ToleranceTimeoutNanos, ExitDelayNanos int64
}

// ResolveFenceRoutingPolicy applies provider, trackable and fence overrides.
func ResolveFenceRoutingPolicy(fence gen.Fence, trackable gen.Trackable, provider gen.LocationProvider) FenceRoutingPolicy {
	policy := resolveFenceExitPolicy(fence, trackable, true, provider, provider.Id != "")
	return FenceRoutingPolicy{
		Radius: trackableRadius(trackable), ExitTolerance: policy.ExitTolerance,
		FenceTimeoutNanos: int64(policy.FenceTimeout), ToleranceTimeoutNanos: int64(policy.ToleranceTimeout), ExitDelayNanos: int64(policy.ExitDelay),
	}
}

// FenceFloorMatches reports whether the location can participate in membership.
func FenceFloorMatches(fence gen.Fence, location gen.Location) bool {
	return fenceMatchesFloor(fence, location)
}

// CertainlyOutside uses the expanded bounding box as a conservative rejection.
func (c *FenceCatalog) CertainlyOutside(fence gen.Fence, location gen.Location, radius float64) bool {
	id := fence.Id.String()
	bounds, valid := c.nativeBounds[id]
	point, err := point2D(location.Position)
	geographic := locationCRS(location) == "EPSG:4326"
	if geographic {
		bounds, valid = c.worldBounds[id]
	}
	if !valid || err != nil {
		return false
	}
	dx, dy := collisionMetersToCoordinateOffsets(locationCRS(location), point, radius)
	minQX, maxQX := point[0]-dx, point[0]+dx
	minQY, maxQY := point[1]-dy, point[1]+dy
	if math.Nextafter(minQY, math.Inf(-1)) > bounds.PointCoord(1)+bounds.LengthsCoord(1) ||
		math.Nextafter(maxQY, math.Inf(1)) < bounds.PointCoord(1) {
		return true
	}
	minX, maxX := bounds.PointCoord(0), bounds.PointCoord(0)+bounds.LengthsCoord(0)
	if geographic && maxX-minX > 180 {
		// Geography may take the shorter path across the antimeridian.
		return false
	}
	if geographic {
		for _, shift := range []float64{-360, 0, 360} {
			if math.Nextafter(minQX+shift, math.Inf(-1)) <= maxX && math.Nextafter(maxQX+shift, math.Inf(1)) >= minX {
				return false
			}
		}
		return true
	}
	return math.Nextafter(minQX, math.Inf(-1)) > maxX || math.Nextafter(maxQX, math.Inf(1)) < minX
}
