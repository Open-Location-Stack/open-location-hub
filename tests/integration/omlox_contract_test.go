package integration

import (
	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"net/http"
	"testing"
	"time"
)

func TestPublishedZoneMappingAndLocationDefaults(t *testing.T) {
	_, base, _ := startHubNoAuth(t)
	token := adminToken(t)
	zonePayload := georeferencedZonePayload(52, 13, false)
	foreign := scopedID(t, "foreign-zone")
	zonePayload["foreign_id"] = foreign
	zonePayload["floor"] = 4
	response := requestJSON(t, http.MethodPost, base+"/v2/zones", token, zonePayload)
	assertStatus(t, response, http.StatusCreated)
	var zone gen.Zone
	decodeResponse(t, response, &zone)
	duplicate := requestJSON(t, http.MethodPost, base+"/v2/zones", token, zonePayload)
	assertStatusAndClose(t, duplicate, http.StatusConflict)
	provider := scopedID(t, "defaults-provider")
	conn := dialWS(t, base)
	defer conn.Close()
	writeWSJSON(t, conn, map[string]any{"event": "subscribe", "topic": "location_updates", "params": map[string]any{"token": token, "provider_id": provider, "crs": "local"}})
	ack := readWSJSON(t, conn)
	if ack.Event != "subscribed" {
		t.Fatalf("subscribe: %+v", ack)
	}
	before := time.Now().UTC().Add(-time.Second)
	ingest := requestJSON(t, http.MethodPut, base+"/v2/providers/locations", token, []map[string]any{{"provider_id": provider, "provider_type": "uwb", "source": foreign, "position": pointPayload(2, 3)}})
	assertStatusAndClose(t, ingest, http.StatusNoContent)
	locations := waitForWSProvider(t, conn, provider, 5*time.Second)
	if len(locations) != 1 {
		t.Fatalf("wanted one native observation, got %d", len(locations))
	}
	location := locations[0]
	if location.TimestampGenerated == nil || location.TimestampGenerated.Before(before) || location.Floor == nil || *location.Floor != 4 || location.Crs == nil || *location.Crs != "local" || location.ElevationRef == nil || *location.ElevationRef != "floor" {
		t.Fatalf("defaults not applied: %+v", location)
	}
	unknown := requestJSON(t, http.MethodPut, base+"/v2/providers/locations", token, []map[string]any{{"provider_id": provider, "provider_type": "uwb", "source": "unknown-local-zone", "position": pointPayload(2, 3)}})
	assertStatusAndClose(t, unknown, http.StatusBadRequest)
	invalidProjection := requestJSON(t, http.MethodGet, base+"/v2/fences/summary?crs=EPSG:4979", token, nil)
	assertStatusAndClose(t, invalidProjection, http.StatusBadRequest)
}

func TestWebSocketSubscriptionIDsAreUniqueAcrossConnections(t *testing.T) {
	_, base, _ := startHubNoAuth(t)
	token := adminToken(t)
	first, second := dialWS(t, base), dialWS(t, base)
	defer first.Close()
	defer second.Close()
	message := map[string]any{"event": "subscribe", "topic": "location_updates", "params": map[string]any{"token": token}}
	writeWSJSON(t, first, message)
	a := readWSJSON(t, first)
	writeWSJSON(t, second, message)
	b := readWSJSON(t, second)
	if a.SubscriptionID == nil || b.SubscriptionID == nil || *a.SubscriptionID == *b.SubscriptionID {
		t.Fatalf("nonunique subscription IDs: %+v %+v", a, b)
	}
}
