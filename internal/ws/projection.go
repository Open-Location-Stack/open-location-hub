package ws

import (
	"context"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/hub"
)

type subscriptionProjector interface {
	ProjectLocation(context.Context, gen.Location, string, string) (gen.Location, error)
	ProjectMotion(context.Context, gen.TrackableMotion, string, string) (gen.TrackableMotion, error)
	ProjectCollision(context.Context, gen.CollisionEvent, string, string) (gen.CollisionEvent, error)
}

type projectionKey struct {
	index int
	crs   string
	zone  string
}

type projectionResult struct {
	event hub.Event
	ok    bool
}

// One cache belongs to one broadcast batch and is shared by its connections.
// Event indexes are only meaningful within that batch; nothing survives it.
type projectionCache struct {
	projector subscriptionProjector
	results   map[projectionKey]projectionResult
}

func newProjectionCache(projector subscriptionProjector) *projectionCache {
	return &projectionCache{projector: projector, results: make(map[projectionKey]projectionResult)}
}

func projectionParams(filter any) (string, string) {
	switch f := filter.(type) {
	case locationFilter:
		return f.CRS, f.ZoneID
	case motionFilter:
		return f.CRS, f.ZoneID
	case collisionFilter:
		return f.CRS, f.ZoneID
	}
	return "", ""
}

// Select and filter one observation variant before projecting. CRS and target
// zone are output parameters, not filters on the observation's source zone.
func projectSubscription(sub subscription, events []hub.Event, cache *projectionCache) (subscription, []hub.Event) {
	crs, zone := projectionParams(sub.filter)
	if crs == "" {
		crs = "EPSG:4326"
	}
	native := crs == "local" && zone == ""
	switch f := sub.filter.(type) {
	case locationFilter:
		f.CRS, f.ZoneID = "", ""
		sub.filter = f
	case motionFilter:
		f.CRS, f.ZoneID = "", ""
		sub.filter = f
	case collisionFilter:
		f.CRS, f.ZoneID = "", ""
		sub.filter = f
	default:
		return sub, events
	}
	out := make([]hub.Event, 0, len(events))
	for i, event := range events {
		if !matchesProjectionInput(sub, event, native) {
			continue
		}
		key := projectionKey{index: i, crs: crs, zone: zone}
		result, exists := cache.results[key]
		if !exists {
			result = cache.project(event, crs, zone, native)
			cache.results[key] = result
		}
		if result.ok {
			out = append(out, result.event)
		}
	}
	return sub, out
}

func matchesProjectionInput(sub subscription, event hub.Event, native bool) bool {
	switch sub.topic {
	case topicLocationUpdates, topicLocationGeoJSON:
		if event.Kind != hub.EventLocation || native && !event.Native || !native && event.Scope != hub.ScopeEPSG4326 {
			return false
		}
		envelope, ok := event.Payload.(hub.LocationEnvelope)
		return ok && matchLocation(sub.filter.(locationFilter), envelope.Location)
	case topicTrackableMotions:
		if event.Kind != hub.EventTrackableMotion || native && !event.Native || !native && event.Scope != hub.ScopeEPSG4326 {
			return false
		}
		envelope, ok := event.Payload.(hub.TrackableMotionEnvelope)
		if !ok {
			return false
		}
		motion := envelope.Motion
		if native && envelope.Original != nil {
			motion = *envelope.Original
		}
		return matchMotion(sub.filter.(motionFilter), motion)
	case topicCollisionEvents:
		envelope, ok := event.Payload.(hub.CollisionEnvelope)
		return event.Kind == hub.EventCollisionEvent && ok && matchCollision(sub.filter.(collisionFilter), envelope.Event)
	default:
		return false
	}
}

func (c *projectionCache) project(event hub.Event, crs, zone string, native bool) projectionResult {
	switch envelope := event.Payload.(type) {
	case hub.LocationEnvelope:
		loc, err := c.projector.ProjectLocation(context.Background(), envelope.Location, crs, zone)
		if err != nil {
			return projectionResult{}
		}
		event.Payload = hub.LocationEnvelope{Location: loc, GeoJSON: hub.LocationGeoJSON(loc)}
	case hub.TrackableMotionEnvelope:
		motion := envelope.Motion
		if native && envelope.Original != nil {
			motion = *envelope.Original
		}
		motion, err := c.projector.ProjectMotion(context.Background(), motion, crs, zone)
		if err != nil {
			return projectionResult{}
		}
		event.Payload = hub.TrackableMotionEnvelope{Motion: motion}
	case hub.CollisionEnvelope:
		collision, err := c.projector.ProjectCollision(context.Background(), envelope.Event, crs, zone)
		if err != nil {
			return projectionResult{}
		}
		event.Payload = hub.CollisionEnvelope{Event: collision}
	}
	return projectionResult{event: event, ok: true}
}
