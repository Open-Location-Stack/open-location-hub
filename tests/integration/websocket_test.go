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
	token := adminToken(t)
	providerID := scopedID(t, "provider-ws")
	conn := dialWS(t, appBaseURL)
	defer conn.Close()

	writeWSJSON(t, conn, map[string]any{
		"event":  "subscribe",
		"topic":  "location_updates",
		"params": map[string]any{"token": token},
	})
	ack := readWSJSON(t, conn)
	if ack.Event != "subscribed" || ack.SubscriptionID == nil {
		t.Fatalf("unexpected subscribe ack: %+v", ack)
	}

	zonePayload := georeferencedZonePayload(0.5, 0.5, false)
	zonePayload["name"] = scopedID(t, "websocket-zone")
	createResp := requestJSON(t, http.MethodPost, appBaseURL+"/v2/zones", token, zonePayload)
	assertStatus(t, createResp, http.StatusCreated)
	var zone struct {
		ID string `json:"id"`
	}
	decodeResponse(t, createResp, &zone)

	createLocalResp := requestJSON(t, http.MethodPost, appBaseURL+"/v2/providers/locations", token, []map[string]any{{
		"crs":           "local",
		"position":      pointPayload(5, 7),
		"provider_id":   providerID,
		"provider_type": "uwb",
		"source":        zone.ID,
	}})
	assertStatusAndClose(t, createLocalResp, http.StatusAccepted)

	body := waitForWSProvider(t, conn, providerID, 10*time.Second)
	seenCRS := map[string]bool{}
	seenProvider := false
	for _, location := range body {
		if location.ProviderId != providerID {
			continue
		}
		seenProvider = true
		if location.Crs != nil {
			seenCRS[*location.Crs] = true
		}
	}
	if !seenProvider || !seenCRS["local"] {
		t.Fatalf("unexpected ws body for %s: %+v", providerID, body)
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

func waitForWSProvider(t *testing.T, conn *websocket.Conn, providerID string, timeout time.Duration) []gen.Location {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		msg := readWSJSON(t, conn)
		if msg.Event != "message" || msg.Topic != "location_updates" {
			continue
		}
		var body []gen.Location
		if err := json.Unmarshal(msg.Payload, &body); err != nil {
			t.Fatalf("decode ws payload failed: %v", err)
		}
		for _, item := range body {
			if item.ProviderId == providerID {
				return body
			}
		}
	}
	t.Fatalf("timed out waiting for websocket provider %s", providerID)
	return nil
}
