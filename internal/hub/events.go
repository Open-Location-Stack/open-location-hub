package hub

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/formation-res/open-rtls-hub/internal/observability"
)

// EventKind identifies a normalized hub event emitted onto the internal bus.
type EventKind string

const (
	EventLocation        EventKind = "location"
	EventProximity       EventKind = "proximity"
	EventTrackableMotion EventKind = "trackable_motion"
	EventFenceEvent      EventKind = "fence_event"
	EventCollisionEvent  EventKind = "collision_event"
	EventMetadataChange  EventKind = "metadata_change"
)

// EventScope describes the payload variant carried by an event.
type EventScope string

const (
	ScopeLocal         EventScope = "local"
	ScopeEPSG4326      EventScope = "epsg4326"
	ScopeGeoJSON       EventScope = "geojson"
	ScopeRaw           EventScope = "raw"
	ScopeDerived       EventScope = "derived"
	ScopeCollisionOnly EventScope = "collision"
	ScopeMetadata      EventScope = "metadata"
)

const (
	metadataOperationCreate = "create"
	metadataOperationUpdate = "update"
	metadataOperationDelete = "delete"
)

// GeoJSONFeatureCollection is the WebSocket/MQTT-friendly GeoJSON container
// emitted for OMLOX geojson topics.
type GeoJSONFeatureCollection struct {
	Type     string           `json:"type"`
	Features []GeoJSONFeature `json:"features"`
}

// GeoJSONFeature is a lightweight GeoJSON feature wrapper.
type GeoJSONFeature struct {
	Type       string         `json:"type"`
	Geometry   any            `json:"geometry"`
	Properties map[string]any `json:"properties,omitempty"`
}

// FenceEventEnvelope keeps the fence event together with its source fence so
// downstream consumers can build GeoJSON payloads without reloading state.
type FenceEventEnvelope struct {
	Event   gen.FenceEvent           `json:"event"`
	Fence   gen.Fence                `json:"fence"`
	GeoJSON GeoJSONFeatureCollection `json:"geojson,omitempty"`

	eventJSON   json.RawMessage `json:"-"`
	geoJSONJSON json.RawMessage `json:"-"`
}

// LocationEnvelope keeps a location payload together with its original input
// and derived GeoJSON representation.
type LocationEnvelope struct {
	Location gen.Location             `json:"location"`
	GeoJSON  GeoJSONFeatureCollection `json:"geojson,omitempty"`

	locationJSON json.RawMessage `json:"-"`
	geoJSONJSON  json.RawMessage `json:"-"`
}

// ProximityEnvelope is the outbound bus representation for raw proximities.
type ProximityEnvelope struct {
	Proximity gen.Proximity `json:"proximity"`

	proximityJSON json.RawMessage `json:"-"`
}

// TrackableMotionEnvelope keeps a motion payload together with its GeoJSON
// representation when useful for downstream consumers.
type TrackableMotionEnvelope struct {
	Motion gen.TrackableMotion `json:"motion"`

	motionJSON json.RawMessage `json:"-"`
}

// CollisionEnvelope wraps a collision event.
type CollisionEnvelope struct {
	Event gen.CollisionEvent `json:"event"`

	eventJSON json.RawMessage `json:"-"`
}

// MetadataChange is the lightweight resource-change notification emitted for
// metadata replication and subscription surfaces.
type MetadataChange struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Operation string    `json:"operation"`
	Timestamp time.Time `json:"timestamp"`

	changeJSON json.RawMessage `json:"-"`
}

type EventPayload interface {
	eventPayload()
}

func (LocationEnvelope) eventPayload()        {}
func (ProximityEnvelope) eventPayload()       {}
func (TrackableMotionEnvelope) eventPayload() {}
func (FenceEventEnvelope) eventPayload()      {}
func (CollisionEnvelope) eventPayload()       {}
func (MetadataChange) eventPayload()          {}

func (e LocationEnvelope) LocationItemJSON() json.RawMessage {
	return itemJSON(e.locationJSON, e.Location)
}

func (e LocationEnvelope) GeoJSONItemJSON() json.RawMessage {
	return itemJSON(e.geoJSONJSON, e.GeoJSON)
}

func (e ProximityEnvelope) ItemJSON() json.RawMessage {
	return itemJSON(e.proximityJSON, e.Proximity)
}

func (e TrackableMotionEnvelope) ItemJSON() json.RawMessage {
	return itemJSON(e.motionJSON, e.Motion)
}

func (e FenceEventEnvelope) EventItemJSON() json.RawMessage {
	return itemJSON(e.eventJSON, e.Event)
}

func (e FenceEventEnvelope) GeoJSONItemJSON() json.RawMessage {
	return itemJSON(e.geoJSONJSON, e.GeoJSON)
}

func (e CollisionEnvelope) ItemJSON() json.RawMessage {
	return itemJSON(e.eventJSON, e.Event)
}

func (e MetadataChange) ItemJSON() json.RawMessage {
	return itemJSON(e.changeJSON, metadataChangePublic(e))
}

// Event is the normalized hub event emitted once and then consumed by
// transport-specific publishers such as MQTT and WebSocket.
type Event struct {
	Kind        EventKind    `json:"kind"`
	Scope       EventScope   `json:"scope"`
	EventTime   time.Time    `json:"event_time"`
	ProcessedAt time.Time    `json:"processed_at"`
	ProviderID  string       `json:"provider_id,omitempty"`
	TrackableID string       `json:"trackable_id,omitempty"`
	FenceID     string       `json:"fence_id,omitempty"`
	OriginHubID string       `json:"origin_hub_id,omitempty"`
	Payload     EventPayload `json:"-"`
}

// Decode decodes the event payload into the requested type.
func Decode[T any](event Event) (T, error) {
	var zero T
	if event.Payload == nil {
		return zero, errors.New("event payload is nil")
	}
	if out, ok := any(event.Payload).(T); ok {
		return out, nil
	}
	raw, err := json.Marshal(event.Payload)
	if err != nil {
		return zero, err
	}
	var out T
	err = json.Unmarshal(raw, &out)
	return out, err
}

// EventBus fans out normalized hub events to multiple consumers.
type EventBus struct {
	mu          sync.RWMutex
	nextID      int
	subscribers map[int]chan Event
	stats       *RuntimeStats
}

// NewEventBus constructs an EventBus.
func NewEventBus() *EventBus {
	return &EventBus{subscribers: map[int]chan Event{}, stats: newRuntimeStats()}
}

// Subscribe registers a buffered event subscriber.
func (b *EventBus) Subscribe(buffer int) (<-chan Event, func()) {
	if buffer <= 0 {
		buffer = 1
	}
	ch := make(chan Event, buffer)
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.subscribers[id] = ch
	b.mu.Unlock()
	if b.stats != nil {
		b.stats.SetEventBusSubscribers(int64(len(b.subscribers)))
	}

	return ch, func() {
		b.mu.Lock()
		if existing, ok := b.subscribers[id]; ok {
			delete(b.subscribers, id)
			close(existing)
		}
		if b.stats != nil {
			b.stats.SetEventBusSubscribers(int64(len(b.subscribers)))
		}
		b.mu.Unlock()
	}
}

// Stats returns the counters associated with this bus.
func (b *EventBus) Stats() *RuntimeStats {
	if b == nil {
		return nil
	}
	return b.stats
}

// Emit publishes an event to all subscribers.
func (b *EventBus) Emit(event Event) {
	start := time.Now()
	b.mu.RLock()
	subs := make([]chan Event, 0, len(b.subscribers))
	for _, ch := range b.subscribers {
		subs = append(subs, ch)
	}
	b.mu.RUnlock()

	for _, ch := range subs {
		select {
		case ch <- event:
		default:
			if b.stats != nil {
				b.stats.RecordEventBusDrop(event)
			}
		}
	}
	observability.Global().RecordEventBusEmit(context.Background(), string(event.Kind), time.Since(start))
}

func newEvent[P EventPayload](kind EventKind, scope EventScope, eventTime time.Time, providerID, trackableID, fenceID, originHubID string, payload P) (Event, error) {
	if eventTime.IsZero() {
		eventTime = time.Now().UTC()
	}
	prepared, err := preparePayload(payload)
	if err != nil {
		return Event{}, err
	}
	event := Event{
		Kind:        kind,
		Scope:       scope,
		EventTime:   eventTime,
		ProcessedAt: time.Now().UTC(),
		ProviderID:  providerID,
		TrackableID: trackableID,
		FenceID:     fenceID,
		OriginHubID: originHubID,
		Payload:     prepared,
	}
	observability.Global().RecordEndToEnd(context.Background(), string(kind), string(scope), event.ProcessedAt.Sub(event.EventTime))
	return event, nil
}

func itemJSON(raw json.RawMessage, value any) json.RawMessage {
	if len(raw) != 0 {
		return raw
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return encoded
}

func preparePayload(payload EventPayload) (EventPayload, error) {
	switch p := payload.(type) {
	case LocationEnvelope:
		if len(p.locationJSON) == 0 {
			raw, err := json.Marshal(p.Location)
			if err != nil {
				return nil, err
			}
			p.locationJSON = raw
		}
		if len(p.geoJSONJSON) == 0 {
			raw, err := json.Marshal(p.GeoJSON)
			if err != nil {
				return nil, err
			}
			p.geoJSONJSON = raw
		}
		return p, nil
	case ProximityEnvelope:
		if len(p.proximityJSON) == 0 {
			raw, err := json.Marshal(p.Proximity)
			if err != nil {
				return nil, err
			}
			p.proximityJSON = raw
		}
		return p, nil
	case TrackableMotionEnvelope:
		if len(p.motionJSON) == 0 {
			raw, err := json.Marshal(p.Motion)
			if err != nil {
				return nil, err
			}
			p.motionJSON = raw
		}
		return p, nil
	case FenceEventEnvelope:
		if len(p.eventJSON) == 0 {
			raw, err := json.Marshal(p.Event)
			if err != nil {
				return nil, err
			}
			p.eventJSON = raw
		}
		if len(p.geoJSONJSON) == 0 {
			raw, err := json.Marshal(p.GeoJSON)
			if err != nil {
				return nil, err
			}
			p.geoJSONJSON = raw
		}
		return p, nil
	case CollisionEnvelope:
		if len(p.eventJSON) == 0 {
			raw, err := json.Marshal(p.Event)
			if err != nil {
				return nil, err
			}
			p.eventJSON = raw
		}
		return p, nil
	case MetadataChange:
		if len(p.changeJSON) == 0 {
			raw, err := json.Marshal(metadataChangePublic(p))
			if err != nil {
				return nil, err
			}
			p.changeJSON = raw
		}
		return p, nil
	default:
		return payload, nil
	}
}

func metadataChangePublic(change MetadataChange) MetadataChange {
	change.changeJSON = nil
	return change
}
