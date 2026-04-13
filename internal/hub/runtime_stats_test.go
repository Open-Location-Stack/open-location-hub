package hub

import "testing"

func TestRuntimeStatsTelemetrySnapshotIncludesDropCounters(t *testing.T) {
	stats := newRuntimeStats()

	stats.IncEventBusDrops()
	stats.IncNativeQueueDrops()
	stats.IncDecisionQueueDrops()
	stats.IncWebSocketOutboundDrops()
	stats.SetNativeQueueDepth(11)
	stats.SetDecisionQueueDepth(7)
	stats.SetEventBusSubscribers(3)
	stats.SetWebSocketConnections(5)
	stats.AddWebSocketOutboundDepth(13)

	snapshot := stats.TelemetrySnapshot()

	if snapshot.EventBusDrops != 1 {
		t.Fatalf("expected event bus drops to be 1, got %d", snapshot.EventBusDrops)
	}
	if snapshot.NativeQueueDrops != 1 {
		t.Fatalf("expected native queue drops to be 1, got %d", snapshot.NativeQueueDrops)
	}
	if snapshot.DecisionQueueDrops != 1 {
		t.Fatalf("expected decision queue drops to be 1, got %d", snapshot.DecisionQueueDrops)
	}
	if snapshot.WebSocketOutboundDrops != 1 {
		t.Fatalf("expected websocket outbound drops to be 1, got %d", snapshot.WebSocketOutboundDrops)
	}
	if snapshot.NativeQueueDepth != 11 {
		t.Fatalf("expected native queue depth to be 11, got %d", snapshot.NativeQueueDepth)
	}
	if snapshot.DecisionQueueDepth != 7 {
		t.Fatalf("expected decision queue depth to be 7, got %d", snapshot.DecisionQueueDepth)
	}
	if snapshot.EventBusSubscribers != 3 {
		t.Fatalf("expected event bus subscribers to be 3, got %d", snapshot.EventBusSubscribers)
	}
	if snapshot.WebSocketConnections != 5 {
		t.Fatalf("expected websocket connections to be 5, got %d", snapshot.WebSocketConnections)
	}
	if snapshot.WebSocketOutboundDepth != 13 {
		t.Fatalf("expected websocket outbound depth to be 13, got %d", snapshot.WebSocketOutboundDepth)
	}
}
