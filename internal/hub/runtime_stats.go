package hub

import "sync/atomic"

// RuntimeStats tracks hot-path overload conditions so callers can distinguish
// ingest bottlenecks from downstream delivery loss.
type RuntimeStats struct {
	eventBusDrops          atomic.Uint64
	nativeQueueDrops       atomic.Uint64
	decisionQueueDrops     atomic.Uint64
	websocketOutboundDrops atomic.Uint64
}

// RuntimeStatsSnapshot is a stable copy of runtime counters.
type RuntimeStatsSnapshot struct {
	EventBusDrops          uint64 `json:"event_bus_drops"`
	NativeQueueDrops       uint64 `json:"native_queue_drops"`
	DecisionQueueDrops     uint64 `json:"decision_queue_drops"`
	WebSocketOutboundDrops uint64 `json:"websocket_outbound_drops"`
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
