package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/observability"
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
	Motion   gen.TrackableMotion  `json:"motion"`
	Original *gen.TrackableMotion `json:"-"`

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
	Native      bool         `json:"-"`
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
	return zero, fmt.Errorf("event payload type mismatch: have %T want %T", event.Payload, zero)
}

// EventBus fans out normalized hub events to multiple consumers.
type EventBus struct {
	mu          sync.RWMutex
	nextID      int
	subscribers map[int]*eventSubscriber
	stats       *RuntimeStats
}

const (
	subscriberFlushSignalBuffer = 1
	subscriberPendingLimit      = 1024
)

type eventSubscriber struct {
	ch           chan Event
	done         chan struct{}
	flushSignal  chan struct{}
	mu           sync.Mutex
	closed       bool
	version      uint64
	pending      map[string]pendingEvent
	pendingOrder []string
}

type pendingEvent struct {
	event   Event
	version uint64
}

// NewEventBus constructs an EventBus.
func NewEventBus() *EventBus {
	return &EventBus{subscribers: map[int]*eventSubscriber{}, stats: newRuntimeStats()}
}

// Subscribe registers a buffered event subscriber.
func (b *EventBus) Subscribe(buffer int) (<-chan Event, func()) {
	if buffer <= 0 {
		buffer = 1
	}
	sub := newEventSubscriber(buffer)
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.subscribers[id] = sub
	if b.stats != nil {
		b.stats.SetEventBusSubscribers(int64(len(b.subscribers)))
	}
	b.mu.Unlock()

	return sub.ch, func() {
		b.mu.Lock()
		if existing, ok := b.subscribers[id]; ok {
			delete(b.subscribers, id)
			existing.close()
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
	b.EmitBatch([]Event{event})
}

// EmitBatch publishes multiple events to all subscribers in a single pass.
func (b *EventBus) EmitBatch(events []Event) {
	if len(events) == 0 {
		return
	}
	start := time.Now()
	b.mu.RLock()
	subs := make([]*eventSubscriber, 0, len(b.subscribers))
	for _, sub := range b.subscribers {
		subs = append(subs, sub)
	}
	b.mu.RUnlock()

	for _, event := range events {
		for _, sub := range subs {
			if !sub.deliver(event) && b.stats != nil {
				b.stats.RecordEventBusDrop(event)
			}
		}
	}
	kind := string(events[0].Kind)
	if len(events) > 1 {
		kind = "batch"
	}
	observability.Global().RecordEventBusEmit(context.Background(), kind, time.Since(start))
}

func newEventSubscriber(buffer int) *eventSubscriber {
	sub := &eventSubscriber{
		ch:          make(chan Event, buffer),
		done:        make(chan struct{}),
		flushSignal: make(chan struct{}, subscriberFlushSignalBuffer),
		pending:     make(map[string]pendingEvent),
	}
	go sub.flushLoop()
	return sub
}

func (s *eventSubscriber) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.done)
	}
}

func (s *eventSubscriber) deliver(event Event) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	key, coalescible := eventCoalescingKey(event)
	if coalescible && len(s.pendingOrder) > 0 {
		return s.enqueuePendingLocked(key, event)
	}

	select {
	case s.ch <- event:
		return true
	default:
		if !coalescible {
			return false
		}
		return s.enqueuePendingLocked(key, event)
	}
}

func (s *eventSubscriber) enqueuePendingLocked(key string, event Event) bool {
	if _, exists := s.pending[key]; !exists {
		if len(s.pendingOrder) >= subscriberPendingLimit {
			return false
		}
		s.pendingOrder = append(s.pendingOrder, key)
	}
	s.version++
	s.pending[key] = pendingEvent{event: event, version: s.version}
	s.signalFlushLocked()
	return true
}

func (s *eventSubscriber) flushLoop() {
	defer func() {
		// Synchronize channel closure with deliver's direct send path.
		s.mu.Lock()
		defer s.mu.Unlock()
		close(s.ch)
	}()
	for {
		select {
		case <-s.done:
			return
		case <-s.flushSignal:
			s.flushPending()
		}
	}
}

func (s *eventSubscriber) flushPending() {
	for {
		key, event, ok := s.peekPending()
		if !ok {
			return
		}
		select {
		case <-s.done:
			return
		case s.ch <- event.event:
			s.removePending(key, event.version)
		case <-s.flushSignal:
			// Refresh the pending value while waiting for the reader to catch up.
		}
	}
}

func (s *eventSubscriber) peekPending() (string, pendingEvent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.pendingOrder) > 0 {
		key := s.pendingOrder[0]
		event, ok := s.pending[key]
		if ok {
			return key, event, true
		}
		s.pendingOrder = s.pendingOrder[1:]
	}
	return "", pendingEvent{}, false
}

func (s *eventSubscriber) removePending(key string, version uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending[key].version != version {
		// A newer value arrived while the previous value was being sent.
		return
	}
	delete(s.pending, key)
	if len(s.pendingOrder) == 0 {
		return
	}
	if s.pendingOrder[0] == key {
		s.pendingOrder = s.pendingOrder[1:]
		return
	}
	for i, pendingKey := range s.pendingOrder {
		if pendingKey == key {
			s.pendingOrder = append(s.pendingOrder[:i], s.pendingOrder[i+1:]...)
			return
		}
	}
}

func (s *eventSubscriber) signalFlushLocked() {
	select {
	case s.flushSignal <- struct{}{}:
	default:
	}
}

func eventCoalescingKey(event Event) (string, bool) {
	switch event.Kind {
	case EventLocation:
		if payload, ok := event.Payload.(LocationEnvelope); ok {
			return string(event.Kind) + "|" + string(event.Scope) + "|" + payload.Location.ProviderId + "|" + payload.Location.Source, true
		}
	case EventTrackableMotion:
		if payload, ok := event.Payload.(TrackableMotionEnvelope); ok {
			return string(event.Kind) + "|" + string(event.Scope) + "|" + payload.Motion.Location.ProviderId + "|" + payload.Motion.Id, true
		}
	}
	return "", false
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
