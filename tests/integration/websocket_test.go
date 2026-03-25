package integration

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/gorilla/websocket"
)

type wsMessage struct {
	Event          string          `json:"event"`
	Topic          string          `json:"topic,omitempty"`
	SubscriptionID *int            `json:"subscription_id,omitempty"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	Code           *int            `json:"code,omitempty"`
	Description    *string         `json:"description,omitempty"`
}

func TestWebSocketReceivesLocationUpdatesFromREST(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("docker/testcontainers unavailable: %v", r)
		}
	}()
	_, appBaseURL, _ := startHubNoAuth(t)
	conn := dialWS(t, appBaseURL)
	defer conn.Close()

	writeWSJSON(t, conn, map[string]any{"event": "subscribe", "topic": "location_updates"})
	ack := readWSJSON(t, conn)
	if ack.Event != "subscribed" || ack.SubscriptionID == nil {
		t.Fatalf("unexpected subscribe ack: %+v", ack)
	}

	zonePayload := georeferencedZonePayload(0.5, 0.5, false)
	createResp := requestJSON(t, http.MethodPost, appBaseURL+"/v2/zones", "", zonePayload)
	assertStatus(t, createResp, http.StatusCreated)
	var zone struct {
		ID string `json:"id"`
	}
	decodeResponse(t, createResp, &zone)

	createLocalResp := requestJSON(t, http.MethodPost, appBaseURL+"/v2/providers/locations", "", []map[string]any{{
		"crs":           "local",
		"position":      pointPayload(5, 7),
		"provider_id":   "provider-ws",
		"provider_type": "uwb",
		"source":        zone.ID,
	}})
	assertStatus(t, createLocalResp, http.StatusAccepted)

	msg := readWSJSON(t, conn)
	if msg.Event != "message" || msg.Topic != "location_updates" {
		t.Fatalf("unexpected ws location message: %+v", msg)
	}
	var body []gen.Location
	if err := json.Unmarshal(msg.Payload, &body); err != nil {
		t.Fatalf("decode ws payload failed: %v", err)
	}
	if len(body) != 1 || body[0].ProviderId != "provider-ws" {
		t.Fatalf("unexpected ws body: %+v", body)
	}
}

func TestWebSocketRejectsDisabledCollisionTopic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("docker/testcontainers unavailable: %v", r)
		}
	}()
	_, appBaseURL, _ := startHubNoAuth(t)
	conn := dialWS(t, appBaseURL)
	defer conn.Close()

	writeWSJSON(t, conn, map[string]any{"event": "subscribe", "topic": "collision_events"})
	msg := readWSJSON(t, conn)
	if msg.Event != "error" || msg.Code == nil || *msg.Code != 10002 {
		t.Fatalf("unexpected collision disabled response: %+v", msg)
	}
	if msg.Description == nil || !strings.Contains(*msg.Description, "disabled") {
		t.Fatalf("expected disabled description, got %+v", msg)
	}
}

func dialWS(t *testing.T, appBaseURL string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(appBaseURL, "http") + "/v2/ws/socket"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket failed: %v", err)
	}
	return conn
}

func writeWSJSON(t *testing.T, conn *websocket.Conn, value any) {
	t.Helper()
	if err := conn.WriteJSON(value); err != nil {
		t.Fatalf("write websocket failed: %v", err)
	}
}

func readWSJSON(t *testing.T, conn *websocket.Conn) wsMessage {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var msg wsMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read websocket failed: %v", err)
	}
	return msg
}
