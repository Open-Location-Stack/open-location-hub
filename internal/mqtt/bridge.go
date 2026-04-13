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
		location, ok := event.Payload.(hub.LocationEnvelope)
		if !ok {
			return nil
		}
		switch event.Scope {
		case hub.ScopeLocal:
			return p.client.PublishRawJSON(ctx, TopicLocationLocal(location.Location.ProviderId), location.LocationItemJSON(), false)
		case hub.ScopeEPSG4326:
			return p.client.PublishRawJSON(ctx, TopicLocationEPSG4326(location.Location.ProviderId), location.LocationItemJSON(), false)
		}
	case hub.EventTrackableMotion:
		motion, ok := event.Payload.(hub.TrackableMotionEnvelope)
		if !ok {
			return nil
		}
		switch event.Scope {
		case hub.ScopeLocal:
			return p.client.PublishRawJSON(ctx, TopicTrackableMotionLocal(motion.Motion.Id), motion.ItemJSON(), false)
		case hub.ScopeEPSG4326:
			return p.client.PublishRawJSON(ctx, TopicTrackableMotionEPSG4326(motion.Motion.Id), motion.ItemJSON(), false)
		}
	case hub.EventFenceEvent:
		envelope, ok := event.Payload.(hub.FenceEventEnvelope)
		if !ok {
			return nil
		}
		eventJSON := envelope.EventItemJSON()
		if err := p.client.PublishRawJSON(ctx, TopicFenceEvent(envelope.Event.FenceId.String()), eventJSON, false); err != nil {
			return err
		}
		if envelope.Event.TrackableId != nil {
			if err := p.client.PublishRawJSON(ctx, TopicFenceEventTrackable(*envelope.Event.TrackableId), eventJSON, false); err != nil {
				return err
			}
		}
		if envelope.Event.ProviderId != nil {
			if err := p.client.PublishRawJSON(ctx, TopicFenceEventProvider(*envelope.Event.ProviderId), eventJSON, false); err != nil {
				return err
			}
		}
		return nil
	case hub.EventCollisionEvent:
		if event.Scope != hub.ScopeEPSG4326 {
			return nil
		}
		collision, ok := event.Payload.(hub.CollisionEnvelope)
		if !ok {
			return nil
		}
		return p.client.PublishRawJSON(ctx, TopicCollisionEventEPSG4326(), collision.ItemJSON(), false)
	}
	return nil
}
