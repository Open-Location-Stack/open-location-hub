package hub

import (
	"context"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
)

// ApplyStreamLocation projects a committed provider observation and publishes it.
func (s *Service) ApplyStreamLocation(ctx context.Context, location gen.Location, observed time.Time, proximity, publish bool) error {
	ttl := s.cfg.LocationTTL
	if proximity {
		ttl = s.cfg.ProximityTTL
	}
	if remaining := observed.Add(ttl).Sub(s.now()); remaining > 0 {
		s.processingState().SetLatestLocation(latestLocationKey(location.ProviderId, location.Source), location, remaining)
	}
	if !publish {
		return nil
	}
	return s.publishNativeLocation(ctx, location)
}

// ApplyStreamProximity publishes a committed raw proximity reading.
func (s *Service) ApplyStreamProximity(proximity gen.Proximity) error {
	return s.publishProximity(proximity)
}

// ApplyStreamMotion projects the estimate of one asset and publishes that motion.
func (s *Service) ApplyStreamMotion(ctx context.Context, id string, location gen.Location, appended time.Time, publish bool) error {
	if remaining := appended.Add(s.cfg.LocationTTL).Sub(s.now()); remaining > 0 {
		s.processingState().SetTrackableLocation(latestTrackableLocationKey(id), location, remaining)
	}
	if !publish {
		return nil
	}
	motion, err := s.trackableMotionBase(ctx, id)
	if err != nil {
		return err
	}
	motion.Location = location
	return s.publishTrackableMotionEvents(location.ProviderId, nativeLocationScope(location), []gen.TrackableMotion{motion}, []gen.TrackableMotion{motion})
}

// ApplyStreamFence publishes a committed fence transition and updates membership.
func (s *Service) ApplyStreamFence(ctx context.Context, fence gen.Fence, event gen.FenceEvent, location gen.Location) error {
	if event.TrackableId != nil {
		if event.EventType == gen.RegionExit {
			s.processingState().ClearInsideFence(*event.TrackableId, fence.Id.String())
		} else {
			s.processingState().putFenceMembership(*event.TrackableId, fence.Id.String(), expiringFenceMembership{
				fence: fence, location: location, entryTime: fenceEventTime(event),
			})
		}
	}
	return s.publishFenceEvent(ctx, fence, event)
}

// ApplyStreamCollision publishes a committed collision decision.
func (s *Service) ApplyStreamCollision(event gen.CollisionEvent, leading gen.TrackableMotion) error {
	return s.emitCollision(event, leading)
}
