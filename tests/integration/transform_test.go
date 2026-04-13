package integration

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/formation-res/open-rtls-hub/internal/mqtt"
	"github.com/formation-res/open-rtls-hub/internal/transform"
	"github.com/google/uuid"
)

func TestCRSTransformationMQTTEndToEnd(t *testing.T) {
	ctx, appBaseURL, brokerURL := startHubNoAuth(t)
	token := adminToken(t)
	providerLocalID := scopedID(t, "provider-local")
	providerWGSID := scopedID(t, "provider-wgs")
	subscriber, messages := mqttSubscriber(t, brokerURL,
		mqtt.TopicLocationLocal(providerLocalID),
		mqtt.TopicLocationEPSG4326(providerLocalID),
		mqtt.TopicLocationLocal(providerWGSID),
		mqtt.TopicLocationEPSG4326(providerWGSID),
	)
	defer subscriber.Disconnect(250)

	zonePayload := georeferencedZonePayload(0.5, 0.5, false)
	zonePayload["name"] = scopedID(t, "transform-zone")
	createResp := requestJSON(t, http.MethodPost, appBaseURL+"/v2/zones", token, zonePayload)
	assertStatus(t, createResp, http.StatusCreated)
	var zone struct {
		ID string `json:"id"`
	}
	decodeResponse(t, createResp, &zone)

	createLocalResp := requestJSON(t, http.MethodPost, appBaseURL+"/v2/providers/locations", token, []map[string]any{{
		"crs":           "local",
		"position":      pointPayload(5, 7),
		"provider_id":   providerLocalID,
		"provider_type": "uwb",
		"source":        zone.ID,
	}})
	assertStatusAndClose(t, createLocalResp, http.StatusAccepted)

	localPublished := waitForLocation(t, messages[mqtt.TopicLocationLocal(providerLocalID)], 10*time.Second)
	wgsPublished := waitForLocation(t, messages[mqtt.TopicLocationEPSG4326(providerLocalID)], 10*time.Second)
	if localPublished.Crs == nil || *localPublished.Crs != "local" {
		t.Fatal("expected local publication to remain local")
	}
	if wgsPublished.Crs == nil || *wgsPublished.Crs != "EPSG:4326" {
		t.Fatal("expected WGS84 publication to use EPSG:4326")
	}
	if samePoint(localPublished.Position, wgsPublished.Position) {
		t.Fatal("expected transformed local and wgs84 publications to differ")
	}

	localTransformer, err := transform.NewLocalTransformer(zoneFromPayload(t, zonePayload, zone.ID))
	if err != nil {
		t.Fatalf("local transformer setup failed: %v", err)
	}
	roundTrip, err := localTransformer.WGS84ToLocal(wgsPublished.Position)
	if err != nil {
		t.Fatalf("roundtrip to local failed: %v", err)
	}
	assertPointClose(t, localPublished.Position, roundTrip, 0.5)

	createWGSResp := requestJSON(t, http.MethodPost, appBaseURL+"/v2/providers/locations", token, []map[string]any{{
		"crs":           "EPSG:4326",
		"position":      pointPayload(0.50005, 0.50004),
		"provider_id":   providerWGSID,
		"provider_type": "uwb",
		"source":        zone.ID,
	}})
	assertStatusAndClose(t, createWGSResp, http.StatusAccepted)

	localFromWGS := waitForLocation(t, messages[mqtt.TopicLocationLocal(providerWGSID)], 10*time.Second)
	wgsFromWGS := waitForLocation(t, messages[mqtt.TopicLocationEPSG4326(providerWGSID)], 10*time.Second)
	if localFromWGS.Crs == nil || *localFromWGS.Crs != "local" {
		t.Fatal("expected local publication for WGS84 input")
	}
	if wgsFromWGS.Crs == nil || *wgsFromWGS.Crs != "EPSG:4326" {
		t.Fatal("expected WGS84 publication for WGS84 input")
	}
	if samePoint(localFromWGS.Position, wgsFromWGS.Position) {
		t.Fatal("expected local and WGS84 variants to differ")
	}
	backToWGS, err := localTransformer.LocalToWGS84(localFromWGS.Position)
	if err != nil {
		t.Fatalf("roundtrip to wgs84 failed: %v", err)
	}
	assertPointClose(t, wgsFromWGS.Position, backToWGS, 1e-4)

	_ = ctx
}

func TestCRSTransformationSuppressesUnavailableDerivedMQTTVariant(t *testing.T) {
	_, appBaseURL, brokerURL := startHubNoAuth(t)
	token := adminToken(t)
	providerID := scopedID(t, "provider-missing")
	subscriber, messages := mqttSubscriber(t, brokerURL,
		mqtt.TopicLocationLocal(providerID),
		mqtt.TopicLocationEPSG4326(providerID),
	)
	defer subscriber.Disconnect(250)

	createResp := requestJSON(t, http.MethodPost, appBaseURL+"/v2/zones", token, map[string]any{
		"type":                     "uwb",
		"incomplete_configuration": true,
		"name":                     scopedID(t, "incomplete-zone"),
	})
	assertStatus(t, createResp, http.StatusCreated)
	var zone struct {
		ID string `json:"id"`
	}
	decodeResponse(t, createResp, &zone)

	publishResp := requestJSON(t, http.MethodPost, appBaseURL+"/v2/providers/locations", token, []map[string]any{{
		"crs":           "local",
		"position":      pointPayload(3, 4),
		"provider_id":   providerID,
		"provider_type": "uwb",
		"source":        zone.ID,
	}})
	assertStatusAndClose(t, publishResp, http.StatusAccepted)

	localPublished := waitForLocation(t, messages[mqtt.TopicLocationLocal(providerID)], 10*time.Second)
	if localPublished.Crs == nil || *localPublished.Crs != "local" {
		t.Fatal("expected local publication to remain available")
	}
	select {
	case payload := <-messages[mqtt.TopicLocationEPSG4326(providerID)]:
		t.Fatalf("unexpected WGS84 payload: %s", string(payload))
	case <-time.After(2 * time.Second):
	}
}

func startHubNoAuth(t *testing.T) (context.Context, string, string) {
	t.Helper()
	return sharedHub(t)
}

func mqttSubscriber(t *testing.T, brokerURL string, topics ...string) (pahomqtt.Client, map[string]chan []byte) {
	t.Helper()
	opts := pahomqtt.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID("integration-" + uuid.NewString()).
		SetCleanSession(true)
	client := pahomqtt.NewClient(opts)
	token := client.Connect()
	if !token.WaitTimeout(15 * time.Second) {
		t.Fatal("mqtt connect timed out")
	}
	if err := token.Error(); err != nil {
		t.Fatalf("mqtt connect failed: %v", err)
	}

	out := make(map[string]chan []byte, len(topics))
	for _, topic := range topics {
		ch := make(chan []byte, 8)
		out[topic] = ch
		token := client.Subscribe(topic, 1, func(_ pahomqtt.Client, msg pahomqtt.Message) {
			ch <- append([]byte(nil), msg.Payload()...)
		})
		if !token.WaitTimeout(15 * time.Second) {
			t.Fatalf("mqtt subscribe timed out for %s", topic)
		}
		if err := token.Error(); err != nil {
			t.Fatalf("mqtt subscribe failed for %s: %v", topic, err)
		}
	}
	return client, out
}

func waitForLocation(t *testing.T, ch <-chan []byte, timeout time.Duration) gen.Location {
	t.Helper()
	select {
	case payload := <-ch:
		var location gen.Location
		if err := json.Unmarshal(payload, &location); err != nil {
			t.Fatalf("decode location failed: %v", err)
		}
		return location
	case <-time.After(timeout):
		t.Fatal("timed out waiting for mqtt location")
		return gen.Location{}
	}
}

func georeferencedZonePayload(lat, lon float64, incomplete bool) map[string]any {
	payload := map[string]any{
		"type": "uwb",
		"name": "Transform Zone",
	}
	if incomplete {
		payload["incomplete_configuration"] = true
		return payload
	}
	payload["ground_control_points"] = []map[string]any{
		{"local": pointPayload(0, 0), "wgs84": pointPayload(lon, lat)},
		{"local": pointPayload(10, 0), "wgs84": pointPayload(lon+0.0001, lat)},
		{"local": pointPayload(0, 10), "wgs84": pointPayload(lon, lat+0.0001)},
	}
	return payload
}

func pointPayload(x, y float64) map[string]any {
	return map[string]any{
		"type":        "Point",
		"coordinates": []float64{x, y},
	}
}

func zoneFromPayload(t *testing.T, payload map[string]any, id string) gen.Zone {
	t.Helper()
	clone := map[string]any{}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload failed: %v", err)
	}
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatalf("unmarshal payload failed: %v", err)
	}
	clone["id"] = id
	raw, err = json.Marshal(clone)
	if err != nil {
		t.Fatalf("marshal zone payload failed: %v", err)
	}
	var zone gen.Zone
	if err := json.Unmarshal(raw, &zone); err != nil {
		t.Fatalf("decode zone failed: %v", err)
	}
	return zone
}

func samePoint(a, b gen.Point) bool {
	coordsA, errA := a.Coordinates.AsGeoJsonPosition2D()
	coordsB, errB := b.Coordinates.AsGeoJsonPosition2D()
	return errA == nil && errB == nil && len(coordsA) == 2 && len(coordsB) == 2 &&
		coordsA[0] == coordsB[0] && coordsA[1] == coordsB[1]
}

func assertPointClose(t *testing.T, want, got gen.Point, tolerance float64) {
	t.Helper()
	a, err := want.Coordinates.AsGeoJsonPosition2D()
	if err != nil {
		t.Fatalf("decode want failed: %v", err)
	}
	b, err := got.Coordinates.AsGeoJsonPosition2D()
	if err != nil {
		t.Fatalf("decode got failed: %v", err)
	}
	if math.Abs(float64(a[0]-b[0])) > tolerance || math.Abs(float64(a[1]-b[1])) > tolerance {
		t.Fatalf("points not close enough: want=%v got=%v tolerance=%v", a, b, tolerance)
	}
}
