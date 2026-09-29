package ws

import (
	"context"
	"github.com/formation-res/open-location-hub/internal/hub"
)

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

// Select one observation variant, then project. CRS and target zone are output
// parameters, not filters on the observation's source zone.
func projectSubscription(sub subscription, events []hub.Event, service *hub.Service) (subscription, []hub.Event) {
	crs, zone := projectionParams(sub.filter)
	native := crs == "local" && zone == ""
	switch f := sub.filter.(type) {
	case locationFilter:
		f.CRS = ""
		f.ZoneID = ""
		sub.filter = f
	case motionFilter:
		f.CRS = ""
		f.ZoneID = ""
		sub.filter = f
	case collisionFilter:
		f.CRS = ""
		f.ZoneID = ""
		sub.filter = f
	default:
		return sub, events
	}
	out := make([]hub.Event, 0, len(events))
	for _, event := range events {
		switch envelope := event.Payload.(type) {
		case hub.LocationEnvelope:
			if native && !event.Native || !native && event.Scope != hub.ScopeEPSG4326 {
				continue
			}
			loc, err := service.ProjectLocation(context.Background(), envelope.Location, crs, zone)
			if err != nil {
				continue
			}
			event.Payload = hub.LocationEnvelope{Location: loc, GeoJSON: hub.LocationGeoJSON(loc)}
		case hub.TrackableMotionEnvelope:
			if native && !event.Native || !native && event.Scope != hub.ScopeEPSG4326 {
				continue
			}
			motion := envelope.Motion
			if native && envelope.Original != nil {
				motion = *envelope.Original
			}
			motion, err := service.ProjectMotion(context.Background(), motion, crs, zone)
			if err != nil {
				continue
			}
			event.Payload = hub.TrackableMotionEnvelope{Motion: motion}
		case hub.CollisionEnvelope:
			collision, err := service.ProjectCollision(context.Background(), envelope.Event, crs, zone)
			if err != nil {
				continue
			}
			event.Payload = hub.CollisionEnvelope{Event: collision}
		}
		out = append(out, event)
	}
	return sub, out
}
