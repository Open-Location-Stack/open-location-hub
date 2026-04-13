package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sync"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/formation-res/open-rtls-hub/internal/mqtt"
	"github.com/gorilla/websocket"
)

func TestScenarioConcurrentRESTLocationPublishersFanOutToMQTTAndWebSocket(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("docker/testcontainers unavailable: %v", r)
		}
	}()

	_, appBaseURL, brokerURL := startHubNoAuth(t)
	token := adminToken(t)
	providers := []string{
		scopedID(t, "scenario-rest-1"),
		scopedID(t, "scenario-rest-2"),
		scopedID(t, "scenario-rest-3"),
		scopedID(t, "scenario-rest-4"),
	}

	topics := make([]string, 0, len(providers)*2)
	for _, providerID := range providers {
		topics = append(topics, mqtt.TopicLocationLocal(providerID), mqtt.TopicLocationEPSG4326(providerID))
	}
	subscriber, messages := mqttSubscriber(t, brokerURL, topics...)
	defer subscriber.Disconnect(250)

	wsConn := dialWS(t, appBaseURL)
	defer wsConn.Close()
	writeWSJSON(t, wsConn, map[string]any{
		"event":  "subscribe",
		"topic":  "location_updates",
		"params": map[string]any{"token": token},
	})
	ack := readWSJSON(t, wsConn)
	if ack.Event != "subscribed" || ack.SubscriptionID == nil {
		t.Fatalf("unexpected subscribe ack: %+v", ack)
	}

	zoneID := createScenarioZone(t, appBaseURL, token)

	var wg sync.WaitGroup
	errCh := make(chan error, len(providers))
	for i, providerID := range providers {
		wg.Add(1)
		go func(index int, provider string) {
			defer wg.Done()
			resp, err := requestJSONAuthorizedNoFail(http.MethodPost, appBaseURL+"/v2/providers/locations", token, []map[string]any{{
				"crs":           "local",
				"position":      pointPayload(float64(5+index), float64(7+index)),
				"provider_id":   provider,
				"provider_type": "uwb",
				"source":        zoneID,
			}})
			if err != nil {
				errCh <- fmt.Errorf("provider %s: %w", provider, err)
				return
			}
			if resp.StatusCode != http.StatusAccepted {
				defer resp.Body.Close()
				errCh <- fmt.Errorf("provider %s: unexpected status %d", provider, resp.StatusCode)
				return
			}
			_ = resp.Body.Close()
		}(i, providerID)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, providerID := range providers {
		localPublished := waitForLocation(t, messages[mqtt.TopicLocationLocal(providerID)], 10*time.Second)
		if localPublished.ProviderId != providerID {
			t.Fatalf("unexpected local provider id: got %s want %s", localPublished.ProviderId, providerID)
		}
		wgsPublished := waitForLocation(t, messages[mqtt.TopicLocationEPSG4326(providerID)], 10*time.Second)
		if wgsPublished.ProviderId != providerID {
			t.Fatalf("unexpected wgs84 provider id: got %s want %s", wgsPublished.ProviderId, providerID)
		}
	}

	waitForWebSocketProviders(t, wsConn, providers, 15*time.Second)
}

func TestScenarioMixedRESTAndMQTTIngestShareOneHubSubscribers(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("docker/testcontainers unavailable: %v", r)
		}
	}()

	_, appBaseURL, brokerURL := startHubNoAuth(t)
	token := adminToken(t)
	providers := []string{scopedID(t, "scenario-rest"), scopedID(t, "scenario-mqtt")}

	topics := []string{
		mqtt.TopicLocationLocal(providers[0]),
		mqtt.TopicLocationEPSG4326(providers[0]),
		mqtt.TopicLocationLocal(providers[1]),
		mqtt.TopicLocationEPSG4326(providers[1]),
	}
	subscriber, messages := mqttSubscriber(t, brokerURL, topics...)
	defer subscriber.Disconnect(250)

	wsConn := dialWS(t, appBaseURL)
	defer wsConn.Close()
	writeWSJSON(t, wsConn, map[string]any{
		"event":  "subscribe",
		"topic":  "location_updates",
		"params": map[string]any{"token": token},
	})
	ack := readWSJSON(t, wsConn)
	if ack.Event != "subscribed" || ack.SubscriptionID == nil {
		t.Fatalf("unexpected subscribe ack: %+v", ack)
	}

	zoneID := createScenarioZone(t, appBaseURL, token)

	publisher := mqttPublisher(t, brokerURL)
	defer publisher.Disconnect(250)

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		resp, err := requestJSONAuthorizedNoFail(http.MethodPost, appBaseURL+"/v2/providers/locations", token, []map[string]any{{
			"crs":           "local",
			"position":      pointPayload(5, 7),
			"provider_id":   providers[0],
			"provider_type": "uwb",
			"source":        zoneID,
		}})
		if err != nil {
			errCh <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusAccepted {
			errCh <- fmt.Errorf("rest ingest: unexpected status %d", resp.StatusCode)
		}
	}()
	go func() {
		defer wg.Done()
		payload, err := json.Marshal(map[string]any{
			"crs":           "local",
			"position":      pointPayload(8, 9),
			"provider_id":   providers[1],
			"provider_type": "uwb",
			"source":        zoneID,
		})
		if err != nil {
			errCh <- fmt.Errorf("mqtt payload marshal failed: %w", err)
			return
		}
		if err := publishMQTT(publisher, mqtt.TopicLocationPub(providers[1]), payload); err != nil {
			errCh <- err
		}
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, providerID := range providers {
		localPublished := waitForLocation(t, messages[mqtt.TopicLocationLocal(providerID)], 10*time.Second)
		if localPublished.ProviderId != providerID {
			t.Fatalf("unexpected local provider id: got %s want %s", localPublished.ProviderId, providerID)
		}
		wgsPublished := waitForLocation(t, messages[mqtt.TopicLocationEPSG4326(providerID)], 10*time.Second)
		if wgsPublished.ProviderId != providerID {
			t.Fatalf("unexpected wgs84 provider id: got %s want %s", wgsPublished.ProviderId, providerID)
		}
	}

	waitForWebSocketProviders(t, wsConn, providers, 15*time.Second)
}

func TestScenarioTenObjectsTraverseFenceLayoutAndUpdateMotions(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("docker/testcontainers unavailable: %v", r)
		}
	}()

	_, appBaseURL, brokerURL := startHubNoAuth(t)
	token := adminToken(t)
	zoneID := createScenarioZone(t, appBaseURL, token)

	fences := []scenarioFence{
		createLocalPointFence(t, appBaseURL, token, zoneID, "receiving", 10, 10, 4),
		createLocalPointFence(t, appBaseURL, token, zoneID, "storage", 30, 10, 4),
		createLocalPointFence(t, appBaseURL, token, zoneID, "shipping", 50, 10, 4),
	}

	objects := make([]movingObject, 0, 10)
	topics := make([]string, 0, 20)
	for i := range 10 {
		providerID := scopedID(t, fmt.Sprintf("mover-%02d", i+1))
		trackableID := createTrackable(t, appBaseURL, token, fmt.Sprintf("%s-object-%02d", scopedID(t, "trackable"), i+1))
		laneY := float64(10)
		if i%2 == 1 {
			laneY = 12
		}
		objects = append(objects, movingObject{
			providerID:  providerID,
			trackableID: trackableID,
			laneY:       laneY,
		})
		topics = append(topics, mqtt.TopicTrackableMotionLocal(trackableID), mqtt.TopicFenceEventTrackable(trackableID))
	}

	subscriber, messages := mqttSubscriber(t, brokerURL, topics...)
	defer subscriber.Disconnect(250)

	steps := []movementStep{
		{name: "west outside", x: 0},
		{name: "enter receiving", x: 10, fence: fences[0], eventType: gen.RegionEntry},
		{name: "leave receiving", x: 20, fence: fences[0], eventType: gen.RegionExit},
		{name: "enter storage", x: 30, fence: fences[1], eventType: gen.RegionEntry},
		{name: "leave storage", x: 40, fence: fences[1], eventType: gen.RegionExit},
		{name: "enter shipping", x: 50, fence: fences[2], eventType: gen.RegionEntry},
		{name: "leave shipping", x: 60, fence: fences[2], eventType: gen.RegionExit},
	}

	for _, step := range steps {
		postMovementStep(t, appBaseURL, token, zoneID, objects, step.x)
		for _, object := range objects {
			motion := waitForTrackableMotion(t, messages[mqtt.TopicTrackableMotionLocal(object.trackableID)], 10*time.Second)
			assertMotionLocation(t, motion, object, step.x)
		}
		if step.eventType == "" {
			assertNoFenceEvents(t, messages, objects, 250*time.Millisecond)
			continue
		}
		for _, object := range objects {
			event := waitForFenceEvent(t, messages[mqtt.TopicFenceEventTrackable(object.trackableID)], 10*time.Second)
			assertFenceEvent(t, event, object, step.fence, step.eventType)
		}
	}

	assertNoFenceEvents(t, messages, objects, 250*time.Millisecond)
}

func createScenarioZone(t *testing.T, appBaseURL, token string) string {
	t.Helper()

	payload := georeferencedZonePayload(0.5, 0.5, false)
	payload["name"] = scopedID(t, "scenario-zone")
	createResp := requestJSON(t, http.MethodPost, appBaseURL+"/v2/zones", token, payload)
	assertStatus(t, createResp, http.StatusCreated)
	var zone struct {
		ID string `json:"id"`
	}
	decodeResponse(t, createResp, &zone)
	if zone.ID == "" {
		t.Fatal("expected created zone id")
	}
	return zone.ID
}

func createTrackable(t *testing.T, appBaseURL, token, name string) string {
	t.Helper()

	createResp := requestJSON(t, http.MethodPost, appBaseURL+"/v2/trackables", token, map[string]any{
		"type": "omlox",
		"name": name,
	})
	assertStatus(t, createResp, http.StatusCreated)
	var trackable struct {
		ID string `json:"id"`
	}
	decodeResponse(t, createResp, &trackable)
	if trackable.ID == "" {
		t.Fatal("expected created trackable id")
	}
	return trackable.ID
}

func createLocalPointFence(t *testing.T, appBaseURL, token, zoneID, foreignID string, x, y, radius float64) scenarioFence {
	t.Helper()
	foreignID = scopedID(t, foreignID)

	createResp := requestJSON(t, http.MethodPost, appBaseURL+"/v2/fences", token, map[string]any{
		"crs":        "local",
		"zone_id":    zoneID,
		"foreign_id": foreignID,
		"region":     pointPayload(x, y),
		"radius":     radius,
	})
	assertStatus(t, createResp, http.StatusCreated)
	var fence struct {
		ID string `json:"id"`
	}
	decodeResponse(t, createResp, &fence)
	if fence.ID == "" {
		t.Fatalf("expected created fence id for %s", foreignID)
	}
	return scenarioFence{
		id:        fence.ID,
		foreignID: foreignID,
	}
}

func postMovementStep(t *testing.T, appBaseURL, token, zoneID string, objects []movingObject, x float64) {
	t.Helper()

	var wg sync.WaitGroup
	errCh := make(chan error, len(objects))
	for _, object := range objects {
		wg.Add(1)
		go func(object movingObject) {
			defer wg.Done()
			resp, err := requestJSONAuthorizedNoFail(http.MethodPost, appBaseURL+"/v2/providers/locations", token, []map[string]any{{
				"crs":           "local",
				"position":      pointPayload(x, object.laneY),
				"provider_id":   object.providerID,
				"provider_type": "uwb",
				"source":        zoneID,
				"trackables":    []string{object.trackableID},
			}})
			if err != nil {
				errCh <- fmt.Errorf("%s: %w", object.providerID, err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusAccepted {
				errCh <- fmt.Errorf("%s: unexpected status %d", object.providerID, resp.StatusCode)
			}
		}(object)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func mqttPublisher(t *testing.T, brokerURL string) pahomqtt.Client {
	t.Helper()

	opts := pahomqtt.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID("integration-pub-" + testImageTag()).
		SetCleanSession(true)
	client := pahomqtt.NewClient(opts)
	token := client.Connect()
	if !token.WaitTimeout(15 * time.Second) {
		t.Fatal("mqtt connect timed out")
	}
	if err := token.Error(); err != nil {
		t.Fatalf("mqtt connect failed: %v", err)
	}
	return client
}

func publishMQTT(client pahomqtt.Client, topic string, payload []byte) error {
	token := client.Publish(topic, 1, false, payload)
	if !token.WaitTimeout(15 * time.Second) {
		return fmt.Errorf("mqtt publish timed out for %s", topic)
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("mqtt publish failed for %s: %w", topic, err)
	}
	return nil
}

func waitForWebSocketProviders(t *testing.T, conn *websocket.Conn, providers []string, timeout time.Duration) {
	t.Helper()

	want := make(map[string]struct{}, len(providers))
	for _, providerID := range providers {
		want[providerID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(providers))
	deadline := time.Now().Add(timeout)
	for len(seen) < len(want) && time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		_ = conn.SetReadDeadline(time.Now().Add(minDuration(remaining, 5*time.Second)))
		msg := readWSJSON(t, conn)
		if msg.Event != "message" || msg.Topic != "location_updates" {
			continue
		}
		var body []map[string]any
		if err := json.Unmarshal(msg.Payload, &body); err != nil {
			t.Fatalf("decode websocket location payload failed: %v", err)
		}
		for _, location := range body {
			providerID, _ := location["provider_id"].(string)
			if _, ok := want[providerID]; ok {
				seen[providerID] = struct{}{}
			}
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("timed out waiting for websocket providers: got %v want %v", seen, want)
	}
}

func waitForTrackableMotion(t *testing.T, ch <-chan []byte, timeout time.Duration) gen.TrackableMotion {
	t.Helper()

	select {
	case payload := <-ch:
		var motion gen.TrackableMotion
		if err := json.Unmarshal(payload, &motion); err != nil {
			t.Fatalf("decode trackable motion failed: %v", err)
		}
		return motion
	case <-time.After(timeout):
		t.Fatal("timed out waiting for trackable motion")
		return gen.TrackableMotion{}
	}
}

func waitForFenceEvent(t *testing.T, ch <-chan []byte, timeout time.Duration) gen.FenceEvent {
	t.Helper()

	select {
	case payload := <-ch:
		var event gen.FenceEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatalf("decode fence event failed: %v", err)
		}
		return event
	case <-time.After(timeout):
		t.Fatal("timed out waiting for fence event")
		return gen.FenceEvent{}
	}
}

func assertNoFenceEvents(t *testing.T, messages map[string]chan []byte, objects []movingObject, timeout time.Duration) {
	t.Helper()

	for _, object := range objects {
		select {
		case payload := <-messages[mqtt.TopicFenceEventTrackable(object.trackableID)]:
			t.Fatalf("unexpected fence event for %s: %s", object.trackableID, string(payload))
		case <-time.After(timeout):
		}
	}
}

func assertFenceEvent(t *testing.T, event gen.FenceEvent, object movingObject, fence scenarioFence, wantType gen.FenceEventEventType) {
	t.Helper()

	if event.EventType != wantType {
		t.Fatalf("unexpected fence event type for %s: got %s want %s", object.trackableID, event.EventType, wantType)
	}
	if event.FenceId.String() != fence.id {
		t.Fatalf("unexpected fence id for %s: got %s want %s", object.trackableID, event.FenceId.String(), fence.id)
	}
	if event.TrackableId == nil || *event.TrackableId != object.trackableID {
		t.Fatalf("unexpected trackable id on fence event: %+v", event.TrackableId)
	}
	if event.ProviderId == nil || *event.ProviderId != object.providerID {
		t.Fatalf("unexpected provider id on fence event: %+v", event.ProviderId)
	}
	if wantType == gen.RegionEntry && event.EntryTime == nil {
		t.Fatal("expected entry_time on region_entry")
	}
	if wantType == gen.RegionExit && event.ExitTime == nil {
		t.Fatal("expected exit_time on region_exit")
	}
}

func assertMotionLocation(t *testing.T, motion gen.TrackableMotion, object movingObject, wantX float64) {
	t.Helper()

	if motion.Id != object.trackableID {
		t.Fatalf("unexpected trackable motion id: got %s want %s", motion.Id, object.trackableID)
	}
	if motion.Location.ProviderId != object.providerID {
		t.Fatalf("unexpected provider id on motion: got %s want %s", motion.Location.ProviderId, object.providerID)
	}
	if motion.Location.Crs == nil || *motion.Location.Crs != "local" {
		t.Fatalf("unexpected motion CRS: %+v", motion.Location.Crs)
	}
	coords, err := motion.Location.Position.Coordinates.AsGeoJsonPosition2D()
	if err != nil {
		t.Fatalf("decode motion coordinates failed: %v", err)
	}
	if len(coords) != 2 {
		t.Fatalf("unexpected coordinate length: %d", len(coords))
	}
	if math.Abs(float64(coords[0])-wantX) > 0.01 || math.Abs(float64(coords[1])-object.laneY) > 0.01 {
		t.Fatalf("unexpected motion coordinates: got [%0.3f %0.3f] want [%0.3f %0.3f]", coords[0], coords[1], wantX, object.laneY)
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func requestJSONNoFail(method, target string, body any) (*http.Response, error) {
	return requestJSONAuthorizedNoFail(method, target, "", body)
}

func requestJSONAuthorizedNoFail(method, target, token string, body any) (*http.Response, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("json marshal failed: %w", err)
	}
	req, err := http.NewRequest(method, target, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("request creation failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	return resp, nil
}

type movingObject struct {
	providerID  string
	trackableID string
	laneY       float64
}

type scenarioFence struct {
	id        string
	foreignID string
}

type movementStep struct {
	name      string
	x         float64
	fence     scenarioFence
	eventType gen.FenceEventEventType
}
