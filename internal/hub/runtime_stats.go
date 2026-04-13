package hub

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/observability"
)

const maxRecentDropSamples = 32

// DropSample captures one recent overload-driven drop with enough context to
// explain what was discarded and where it happened.
type DropSample struct {
	Stage       string    `json:"stage"`
	Reason      string    `json:"reason"`
	Total       uint64    `json:"total"`
	RecordedAt  time.Time `json:"recorded_at"`
	QueueDepth  int64     `json:"queue_depth,omitempty"`
	EventKind   string    `json:"event_kind,omitempty"`
	EventScope  string    `json:"event_scope,omitempty"`
	ProviderID  string    `json:"provider_id,omitempty"`
	TrackableID string    `json:"trackable_id,omitempty"`
	FenceID     string    `json:"fence_id,omitempty"`
	Source      string    `json:"source,omitempty"`
	Topic       string    `json:"topic,omitempty"`
}

// RuntimeStats tracks hot-path overload conditions so callers can distinguish
// ingest bottlenecks from downstream delivery loss.
type RuntimeStats struct {
	eventBusDrops          atomic.Uint64
	nativeQueueDrops       atomic.Uint64
	decisionQueueDrops     atomic.Uint64
	websocketOutboundDrops atomic.Uint64
	nativeQueueDepth       atomic.Int64
	decisionQueueDepth     atomic.Int64
	eventBusSubscribers    atomic.Int64
	websocketConnections   atomic.Int64
	websocketOutboundDepth atomic.Int64
	dropMu                 sync.Mutex
	recentDrops            []DropSample
}

// RuntimeStatsSnapshot is a stable copy of runtime counters.
type RuntimeStatsSnapshot struct {
	EventBusDrops          uint64       `json:"event_bus_drops"`
	NativeQueueDrops       uint64       `json:"native_queue_drops"`
	DecisionQueueDrops     uint64       `json:"decision_queue_drops"`
	WebSocketOutboundDrops uint64       `json:"websocket_outbound_drops"`
	NativeQueueDepth       int64        `json:"native_queue_depth"`
	DecisionQueueDepth     int64        `json:"decision_queue_depth"`
	EventBusSubscribers    int64        `json:"event_bus_subscribers"`
	WebSocketConnections   int64        `json:"websocket_connections"`
	WebSocketOutboundDepth int64        `json:"websocket_outbound_depth"`
	RecentDrops            []DropSample `json:"recent_drops,omitempty"`
}

func newRuntimeStats() *RuntimeStats {
	return &RuntimeStats{}
}

func (s *RuntimeStats) Snapshot() RuntimeStatsSnapshot {
	if s == nil {
		return RuntimeStatsSnapshot{}
	}
	s.dropMu.Lock()
	recent := append([]DropSample(nil), s.recentDrops...)
	s.dropMu.Unlock()
	return RuntimeStatsSnapshot{
		EventBusDrops:          s.eventBusDrops.Load(),
		NativeQueueDrops:       s.nativeQueueDrops.Load(),
		DecisionQueueDrops:     s.decisionQueueDrops.Load(),
		WebSocketOutboundDrops: s.websocketOutboundDrops.Load(),
		NativeQueueDepth:       s.nativeQueueDepth.Load(),
		DecisionQueueDepth:     s.decisionQueueDepth.Load(),
		EventBusSubscribers:    s.eventBusSubscribers.Load(),
		WebSocketConnections:   s.websocketConnections.Load(),
		WebSocketOutboundDepth: s.websocketOutboundDepth.Load(),
		RecentDrops:            recent,
	}
}

// TelemetrySnapshot exposes queue and connection gauges for observability.
func (s *RuntimeStats) TelemetrySnapshot() observability.RuntimeMetricsSnapshot {
	if s == nil {
		return observability.RuntimeMetricsSnapshot{}
	}
	return observability.RuntimeMetricsSnapshot{
		EventBusDrops:          int64(s.eventBusDrops.Load()),
		NativeQueueDrops:       int64(s.nativeQueueDrops.Load()),
		DecisionQueueDrops:     int64(s.decisionQueueDrops.Load()),
		WebSocketOutboundDrops: int64(s.websocketOutboundDrops.Load()),
		NativeQueueDepth:       s.nativeQueueDepth.Load(),
		DecisionQueueDepth:     s.decisionQueueDepth.Load(),
		EventBusSubscribers:    s.eventBusSubscribers.Load(),
		WebSocketConnections:   s.websocketConnections.Load(),
		WebSocketOutboundDepth: s.websocketOutboundDepth.Load(),
	}
}

func (s *RuntimeStats) IncEventBusDrops() uint64 {
	if s == nil {
		return 0
	}
	return s.eventBusDrops.Add(1)
}

func (s *RuntimeStats) IncNativeQueueDrops() uint64 {
	if s == nil {
		return 0
	}
	return s.nativeQueueDrops.Add(1)
}

func (s *RuntimeStats) IncDecisionQueueDrops() uint64 {
	if s == nil {
		return 0
	}
	return s.decisionQueueDrops.Add(1)
}

func (s *RuntimeStats) IncWebSocketOutboundDrops() uint64 {
	if s == nil {
		return 0
	}
	return s.websocketOutboundDrops.Add(1)
}

func (s *RuntimeStats) SetNativeQueueDepth(depth int64) {
	if s != nil {
		s.nativeQueueDepth.Store(depth)
	}
}

func (s *RuntimeStats) SetDecisionQueueDepth(depth int64) {
	if s != nil {
		s.decisionQueueDepth.Store(depth)
	}
}

func (s *RuntimeStats) SetEventBusSubscribers(count int64) {
	if s != nil {
		s.eventBusSubscribers.Store(count)
	}
}

func (s *RuntimeStats) SetWebSocketConnections(count int64) {
	if s != nil {
		s.websocketConnections.Store(count)
	}
}

func (s *RuntimeStats) AddWebSocketOutboundDepth(delta int64) int64 {
	if s == nil {
		return 0
	}
	return s.websocketOutboundDepth.Add(delta)
}

func (s *RuntimeStats) RecordEventBusDrop(event Event) uint64 {
	total := s.IncEventBusDrops()
	s.recordDrop(DropSample{
		Stage:       "event_bus",
		Reason:      "subscriber_channel_full",
		Total:       total,
		RecordedAt:  time.Now().UTC(),
		EventKind:   string(event.Kind),
		EventScope:  string(event.Scope),
		ProviderID:  event.ProviderID,
		TrackableID: event.TrackableID,
		FenceID:     event.FenceID,
	})
	return total
}

func (s *RuntimeStats) RecordNativeQueueDrop(work derivedLocationWork, depth int64) uint64 {
	total := s.IncNativeQueueDrops()
	s.recordDrop(DropSample{
		Stage:      "native_queue",
		Reason:     "queue_full",
		Total:      total,
		RecordedAt: time.Now().UTC(),
		QueueDepth: depth,
		ProviderID: work.Location.ProviderId,
		Source:     work.Location.Source,
	})
	return total
}

func (s *RuntimeStats) RecordDecisionQueueDrop(work derivedLocationWork, depth int64) uint64 {
	total := s.IncDecisionQueueDrops()
	s.recordDrop(DropSample{
		Stage:      "decision_queue",
		Reason:     "queue_full",
		Total:      total,
		RecordedAt: time.Now().UTC(),
		QueueDepth: depth,
		ProviderID: work.Location.ProviderId,
		Source:     work.Location.Source,
	})
	return total
}

func (s *RuntimeStats) RecordWebSocketOutboundDrop(topic string, depth int64) uint64 {
	total := s.IncWebSocketOutboundDrops()
	s.recordDrop(DropSample{
		Stage:      "websocket_outbound",
		Reason:     "outbound_buffer_full",
		Total:      total,
		RecordedAt: time.Now().UTC(),
		QueueDepth: depth,
		Topic:      topic,
	})
	return total
}

func (s *RuntimeStats) recordDrop(sample DropSample) {
	if s == nil {
		return
	}
	s.dropMu.Lock()
	defer s.dropMu.Unlock()
	if len(s.recentDrops) == maxRecentDropSamples {
		copy(s.recentDrops, s.recentDrops[1:])
		s.recentDrops[len(s.recentDrops)-1] = sample
		return
	}
	s.recentDrops = append(s.recentDrops, sample)
}
