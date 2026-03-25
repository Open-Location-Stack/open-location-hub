package mqtt

import (
	"context"

	"github.com/formation-res/open-rtls-hub/internal/hub"
)

// EventPublisher republishes normalized hub events to MQTT topics.
type EventPublisher struct {
	client *Client
}

// NewEventPublisher constructs an MQTT bridge that republishes hub events.
func NewEventPublisher(client *Client) *EventPublisher {
	return &EventPublisher{client: client}
}

// Handle republishes the supplied hub event to the MQTT topics expected by the
// OMLOX MQTT mapping used by this repository.
func (p *EventPublisher) Handle(ctx context.Context, event hub.Event) error {
	if p == nil || p.client == nil {
		return nil
	}
	switch event.Kind {
	case hub.EventLocation:
		location, err := hub.Decode[hub.LocationEnvelope](event)
		if err != nil {
			return err
		}
		switch event.Scope {
		case hub.ScopeLocal:
			return p.client.PublishJSON(ctx, TopicLocationLocal(location.Location.ProviderId), location.Location, false)
		case hub.ScopeEPSG4326:
			return p.client.PublishJSON(ctx, TopicLocationEPSG4326(location.Location.ProviderId), location.Location, false)
		}
	case hub.EventTrackableMotion:
		motion, err := hub.Decode[hub.TrackableMotionEnvelope](event)
		if err != nil {
			return err
		}
		switch event.Scope {
		case hub.ScopeLocal:
			return p.client.PublishJSON(ctx, TopicTrackableMotionLocal(motion.Motion.Id), motion.Motion, false)
		case hub.ScopeEPSG4326:
			return p.client.PublishJSON(ctx, TopicTrackableMotionEPSG4326(motion.Motion.Id), motion.Motion, false)
		}
	case hub.EventFenceEvent:
		envelope, err := hub.Decode[hub.FenceEventEnvelope](event)
		if err != nil {
			return err
		}
		if err := p.client.PublishJSON(ctx, TopicFenceEvent(envelope.Event.FenceId.String()), envelope.Event, false); err != nil {
			return err
		}
		if envelope.Event.TrackableId != nil {
			if err := p.client.PublishJSON(ctx, TopicFenceEventTrackable(*envelope.Event.TrackableId), envelope.Event, false); err != nil {
				return err
			}
		}
		if envelope.Event.ProviderId != nil {
			if err := p.client.PublishJSON(ctx, TopicFenceEventProvider(*envelope.Event.ProviderId), envelope.Event, false); err != nil {
				return err
			}
		}
		return nil
	case hub.EventCollisionEvent:
		if event.Scope != hub.ScopeEPSG4326 {
			return nil
		}
		collision, err := hub.Decode[hub.CollisionEnvelope](event)
		if err != nil {
			return err
		}
		return p.client.PublishJSON(ctx, TopicCollisionEventEPSG4326(), collision.Event, false)
	}
	return nil
}
