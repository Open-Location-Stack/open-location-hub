package hub

import (
	"sync/atomic"

	"github.com/formation-res/open-rtls-hub/internal/observability"
)

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
}

// RuntimeStatsSnapshot is a stable copy of runtime counters.
type RuntimeStatsSnapshot struct {
	EventBusDrops          uint64 `json:"event_bus_drops"`
	NativeQueueDrops       uint64 `json:"native_queue_drops"`
	DecisionQueueDrops     uint64 `json:"decision_queue_drops"`
	WebSocketOutboundDrops uint64 `json:"websocket_outbound_drops"`
	NativeQueueDepth       int64  `json:"native_queue_depth"`
	DecisionQueueDepth     int64  `json:"decision_queue_depth"`
	EventBusSubscribers    int64  `json:"event_bus_subscribers"`
	WebSocketConnections   int64  `json:"websocket_connections"`
	WebSocketOutboundDepth int64  `json:"websocket_outbound_depth"`
}

func newRuntimeStats() *RuntimeStats {
	return &RuntimeStats{}
}

func (s *RuntimeStats) Snapshot() RuntimeStatsSnapshot {
	if s == nil {
		return RuntimeStatsSnapshot{}
	}
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
