package ws

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/config"
	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/formation-res/open-rtls-hub/internal/hub"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

func TestUnknownEventReturnsProtocolError(t *testing.T) {
	t.Parallel()

	client, cleanup := startTestHub(t, hub.NewEventBus(), false)
	defer cleanup()

	writeWS(t, client, map[string]any{"event": "wat"})
	msg := readWS(t, client)
	if msg.Event != "error" || msg.Code == nil || *msg.Code != errUnknownEvent {
		t.Fatalf("unexpected error response: %+v", msg)
	}
}

func TestCollisionSubscribeReturnsDisabledError(t *testing.T) {
	t.Parallel()

	client, cleanup := startTestHub(t, hub.NewEventBus(), false)
	defer cleanup()

	writeWS(t, client, map[string]any{"event": "subscribe", "topic": topicCollisionEvents})
	msg := readWS(t, client)
	if msg.Event != "error" || msg.Code == nil || *msg.Code != errSubscribeFailed {
		t.Fatalf("unexpected collision disabled response: %+v", msg)
	}
	if msg.Description == nil || !strings.Contains(*msg.Description, "disabled") {
		t.Fatalf("expected disabled description, got %+v", msg)
	}
}

func TestBusEventReachesSubscribedClient(t *testing.T) {
	t.Parallel()

	bus := hub.NewEventBus()
	client, cleanup := startTestHub(t, bus, true)
	defer cleanup()

	writeWS(t, client, map[string]any{"event": "subscribe", "topic": topicLocationUpdates})
	ack := readWS(t, client)
	if ack.Event != "subscribed" || ack.SubscriptionID == nil {
		t.Fatalf("unexpected subscribe ack: %+v", ack)
	}

	location := testLocation(t)
	bus.Emit(hub.Event{Kind: hub.EventLocation, Scope: hub.ScopeLocal, Payload: hub.LocationEnvelope{Location: location}})

	msg := readWS(t, client)
	if msg.Event != "message" || msg.Topic != topicLocationUpdates {
		t.Fatalf("unexpected bus delivery: %+v", msg)
	}
	var body []gen.Location
	if err := json.Unmarshal(msg.Payload, &body); err != nil {
		t.Fatalf("decode delivered payload failed: %v", err)
	}
	if len(body) != 1 || body[0].ProviderId != location.ProviderId {
		t.Fatalf("unexpected payload: %+v", body)
	}
}

func TestMetadataChangeEventReachesSubscribedClient(t *testing.T) {
	t.Parallel()

	bus := hub.NewEventBus()
	client, cleanup := startTestHub(t, bus, true)
	defer cleanup()

	writeWS(t, client, map[string]any{"event": "subscribe", "topic": topicMetadataChanges, "params": map[string]any{"type": "zone"}})
	ack := readWS(t, client)
	if ack.Event != "subscribed" || ack.SubscriptionID == nil {
		t.Fatalf("unexpected subscribe ack: %+v", ack)
	}

	bus.Emit(hub.Event{Kind: hub.EventMetadataChange, Scope: hub.ScopeMetadata, Payload: hub.MetadataChange{
		ID:        "zone-1",
		Type:      "zone",
		Operation: "update",
		Timestamp: time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC),
	}})

	msg := readWS(t, client)
	if msg.Event != "message" || msg.Topic != topicMetadataChanges {
		t.Fatalf("unexpected metadata delivery: %+v", msg)
	}
	var body []hub.MetadataChange
	if err := json.Unmarshal(msg.Payload, &body); err != nil {
		t.Fatalf("decode metadata payload failed: %v", err)
	}
	if len(body) != 1 || body[0].ID != "zone-1" || body[0].Type != "zone" {
		t.Fatalf("unexpected metadata payload: %+v", body)
	}
}

func TestPayloadBatchForSubscriptionBuildsJSONArrayFromCachedItems(t *testing.T) {
	t.Parallel()

	location := testLocation(t)
	raw, ok := payloadBatchForSubscription(subscription{
		id:     1,
		topic:  topicLocationUpdates,
		filter: locationFilter{},
	}, []hub.Event{{
		Kind:    hub.EventLocation,
		Scope:   hub.ScopeLocal,
		Payload: hub.LocationEnvelope{Location: location},
	}})
	if !ok {
		t.Fatal("expected payload batch")
	}

	var body []gen.Location
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode batch payload failed: %v", err)
	}
	if len(body) != 1 || body[0].ProviderId != location.ProviderId {
		t.Fatalf("unexpected payload: %+v", body)
	}
}

func TestBroadcastAfterClientDisconnectDoesNotBreakHub(t *testing.T) {
	t.Parallel()

	bus := hub.NewEventBus()
	client, cleanup := startTestHub(t, bus, true)
	writeWS(t, client, map[string]any{"event": "subscribe", "topic": topicLocationUpdates})
	_ = readWS(t, client)
	cleanup()

	location := testLocation(t)
	locationEvent := hub.Event{Kind: hub.EventLocation, Scope: hub.ScopeLocal, Payload: hub.LocationEnvelope{Location: location}}
	bus.Emit(locationEvent)

	secondClient, secondCleanup := startTestHub(t, bus, true)
	defer secondCleanup()
	writeWS(t, secondClient, map[string]any{"event": "subscribe", "topic": topicLocationUpdates})
	_ = readWS(t, secondClient)
	bus.Emit(locationEvent)
	msg := readWS(t, secondClient)
	if msg.Event != "message" {
		t.Fatalf("expected message after prior disconnect, got %+v", msg)
	}
}

func TestSendWrapperSafeDuringConcurrentClose(t *testing.T) {
	t.Parallel()

	bus := hub.NewEventBus()
	h := New(zap.NewNop(), nil, bus, nil, nil, config.AuthConfig{Enabled: false, Mode: "none"}, time.Second, 3*time.Second, time.Second, 1, 32, true)
	server2 := httptest.NewServer(http.HandlerFunc(h.Handle))
	defer server2.Close()
	wsURL2 := "ws" + strings.TrimPrefix(server2.URL, "http") + "/v2/ws/socket"
	connClient, _, err := websocket.DefaultDialer.Dial(wsURL2, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	defer connClient.Close()

	time.Sleep(50 * time.Millisecond)
	h.mu.RLock()
	var conn *connection
	for candidate := range h.connections {
		conn = candidate
		break
	}
	h.mu.RUnlock()
	if conn == nil {
		t.Fatal("expected test connection")
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		conn.close()
	}()
	go func() {
		defer wg.Done()
		conn.sendWrapper(wrapper{Event: "error", Description: stringPtr("test")})
	}()
	wg.Wait()
}

func TestSendWrapperCoalescesHotTopicsWhenOutboundBufferIsFull(t *testing.T) {
	t.Parallel()

	bus := hub.NewEventBus()
	h := New(zap.NewNop(), nil, bus, nil, nil, config.AuthConfig{Enabled: false, Mode: "none"}, time.Second, 3*time.Second, time.Second, 1, 32, true)
	conn := &connection{
		hub:     h,
		send:    make(chan []byte, 1),
		pending: map[string][]byte{},
		done:    make(chan struct{}),
	}
	subscriptionID := 7

	conn.send <- []byte(`{"buffered":true}`)
	conn.sendWrapper(wrapper{Event: "message", Topic: topicLocationUpdates, SubscriptionID: &subscriptionID, Payload: json.RawMessage(`[{"source":"old"}]`)})
	conn.sendWrapper(wrapper{Event: "message", Topic: topicLocationUpdates, SubscriptionID: &subscriptionID, Payload: json.RawMessage(`[{"source":"new"}]`)})

	if len(conn.pending) != 1 {
		t.Fatalf("expected one pending coalesced payload, got %d", len(conn.pending))
	}
	conn.flushPending()
	if len(conn.pending) != 1 {
		t.Fatal("expected pending payload to remain while channel is still full")
	}

	<-conn.send
	conn.flushPending()
	if len(conn.pending) != 0 {
		t.Fatal("expected pending payload to flush once channel has room")
	}

	raw := <-conn.send
	var msg wrapper
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("decode coalesced wrapper failed: %v", err)
	}
	if string(msg.Payload) != `[{"source":"new"}]` {
		t.Fatalf("expected latest payload to win, got %s", string(msg.Payload))
	}
}

func TestSendWrapperStillDropsNonCoalescibleTopicsWhenOutboundBufferIsFull(t *testing.T) {
	t.Parallel()

	bus := hub.NewEventBus()
	h := New(zap.NewNop(), nil, bus, nil, nil, config.AuthConfig{Enabled: false, Mode: "none"}, time.Second, 3*time.Second, time.Second, 1, 32, true)
	conn := &connection{
		hub:     h,
		send:    make(chan []byte, 1),
		pending: map[string][]byte{},
		done:    make(chan struct{}),
	}
	subscriptionID := 9

	conn.send <- []byte(`{"buffered":true}`)
	conn.sendWrapper(wrapper{Event: "message", Topic: topicFenceEvents, SubscriptionID: &subscriptionID, Payload: json.RawMessage(`[{"id":"fence-1"}]`)})

	if len(conn.pending) != 0 {
		t.Fatal("expected discrete topic not to be coalesced")
	}
	if got := h.stats.Snapshot().WebSocketOutboundDrops; got != 1 {
		t.Fatalf("expected one outbound drop, got %d", got)
	}
}

func startTestHub(t *testing.T, bus *hub.EventBus, collisionsEnabled bool) (*websocket.Conn, func()) {
	t.Helper()
	server := httptest.NewServer(httpHandler(t, bus, collisionsEnabled))
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v2/ws/socket"
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	return client, func() { _ = client.Close() }
}

func httpHandler(t *testing.T, bus *hub.EventBus, collisionsEnabled bool) http.Handler {
	t.Helper()
	return http.HandlerFunc(New(zap.NewNop(), nil, bus, nil, nil, config.AuthConfig{Enabled: false, Mode: "none"}, time.Second, 3*time.Second, time.Second, 8, 128, collisionsEnabled).Handle)
}

func TestReadDeadlineExtendsOnIncomingMessages(t *testing.T) {
	t.Parallel()

	bus := hub.NewEventBus()
	h := New(
		zap.NewNop(),
		nil,
		bus,
		nil,
		nil,
		config.AuthConfig{Enabled: false, Mode: "none"},
		time.Second,
		80*time.Millisecond,
		time.Hour,
		8,
		128,
		true,
	)
	server := httptest.NewServer(http.HandlerFunc(h.Handle))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v2/ws/socket"
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	defer client.Close()

	writeWS(t, client, map[string]any{"event": "wat"})
	time.Sleep(60 * time.Millisecond)
	writeWS(t, client, map[string]any{"event": "wat"})
	time.Sleep(60 * time.Millisecond)
	writeWS(t, client, map[string]any{"event": "wat"})
}

func writeWS(t *testing.T, conn *websocket.Conn, value any) {
	t.Helper()
	if err := conn.WriteJSON(value); err != nil {
		t.Fatalf("write websocket failed: %v", err)
	}
}

func readWS(t *testing.T, conn *websocket.Conn) wrapper {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg wrapper
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read websocket failed: %v", err)
	}
	return msg
}

func testLocation(t *testing.T) gen.Location {
	t.Helper()
	point := gen.Point{Type: "Point"}
	if err := point.Coordinates.FromGeoJsonPosition2D([]float32{1, 2}); err != nil {
		t.Fatalf("set point failed: %v", err)
	}
	crs := "local"
	return gen.Location{Crs: &crs, Position: point, ProviderId: "provider-a", ProviderType: "uwb", Source: "zone-a"}
}

func stringPtr(value string) *string {
	return &value
}
