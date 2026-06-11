package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/hub"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const testRequestBodyLimitBytes int64 = 64

func TestHandlerRoutesExerciseSuccessAndFailurePaths(t *testing.T) {
	t.Parallel()

	zoneID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	trackableID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	fenceID := uuid.MustParse("33333333-3333-3333-3333-333333333333")

	svc := &fakeService{
		listZonesFn: func(context.Context) ([]gen.Zone, error) {
			return []gen.Zone{{Id: zoneID, Type: "rfid"}}, nil
		},
		createZoneFn: func(_ context.Context, body json.RawMessage) (gen.Zone, error) {
			if !bytes.Equal(body, []byte(`{"type":"rfid"}`)) {
				t.Fatalf("unexpected zone body: %s", string(body))
			}
			return gen.Zone{Id: zoneID, Type: "rfid"}, nil
		},
		getZoneFn: func(context.Context, gen.ZoneId) (gen.Zone, error) {
			return gen.Zone{Id: zoneID, Type: "rfid"}, nil
		},
		updateZoneFn: func(_ context.Context, id gen.ZoneId, body json.RawMessage) (gen.Zone, error) {
			if id != zoneID {
				t.Fatalf("unexpected zone id: %s", id)
			}
			if !bytes.Equal(body, []byte(`{"id":"11111111-1111-1111-1111-111111111111","type":"uwb"}`)) {
				t.Fatalf("unexpected updated zone body: %s", string(body))
			}
			return gen.Zone{Id: zoneID, Type: "uwb"}, nil
		},
		deleteZoneFn: func(context.Context, gen.ZoneId) error {
			return &hub.HTTPError{Status: http.StatusNotFound, Type: "not_found", Message: "zone not found"}
		},
		listTrackablesFn: func(context.Context) ([]gen.Trackable, error) {
			return []gen.Trackable{{Id: trackableID, Type: "asset"}}, nil
		},
		createTrackableFn: func(_ context.Context, body gen.TrackableWrite) (gen.Trackable, error) {
			if body.Type != "asset" {
				t.Fatalf("unexpected trackable type: %s", body.Type)
			}
			return gen.Trackable{Id: trackableID, Type: "asset"}, nil
		},
		getTrackableFn: func(context.Context, gen.TrackableId) (gen.Trackable, error) {
			return gen.Trackable{}, errors.New("db offline")
		},
		updateTrackableFn: func(context.Context, gen.TrackableId, gen.TrackableWrite) (gen.Trackable, error) {
			t.Fatal("update trackable should not run for invalid JSON")
			return gen.Trackable{}, nil
		},
		deleteTrackableFn: func(context.Context, gen.TrackableId) error {
			return nil
		},
		listProvidersFn: func(context.Context) ([]gen.LocationProvider, error) {
			return []gen.LocationProvider{{Id: "provider-a", Type: "uwb"}}, nil
		},
		createProviderFn: func(context.Context, gen.LocationProviderWrite) (gen.LocationProvider, error) {
			return gen.LocationProvider{}, errors.New("write failed")
		},
		getProviderFn: func(context.Context, gen.ProviderId) (gen.LocationProvider, error) {
			return gen.LocationProvider{Id: "provider-a", Type: "uwb"}, nil
		},
		updateProviderFn: func(_ context.Context, id gen.ProviderId, body gen.LocationProviderWrite) (gen.LocationProvider, error) {
			if id != "provider-a" || body.Type != "uwb" {
				t.Fatalf("unexpected provider update payload: id=%s body=%+v", id, body)
			}
			return gen.LocationProvider{Id: "provider-a", Type: "uwb"}, nil
		},
		deleteProviderFn: func(context.Context, gen.ProviderId) error {
			return nil
		},
		processLocationsFn: func(context.Context, []gen.Location) error {
			return &hub.HTTPError{Status: http.StatusBadRequest, Type: "bad_request", Message: "provider_id is required"}
		},
		processProximitiesFn: func(context.Context, []gen.Proximity) error {
			return nil
		},
		listFencesFn: func(context.Context) ([]gen.Fence, error) {
			return []gen.Fence{{Id: fenceID}}, nil
		},
		createFenceFn: func(context.Context, json.RawMessage) (gen.Fence, error) {
			return gen.Fence{Id: fenceID}, nil
		},
		getFenceFn: func(context.Context, gen.FenceId) (gen.Fence, error) {
			return gen.Fence{Id: fenceID}, nil
		},
		updateFenceFn: func(_ context.Context, id gen.FenceId, body json.RawMessage) (gen.Fence, error) {
			if id != fenceID {
				t.Fatalf("unexpected fence id: %s", id)
			}
			if len(body) == 0 {
				t.Fatal("expected raw fence body")
			}
			return gen.Fence{Id: fenceID}, nil
		},
		deleteFenceFn: func(context.Context, gen.FenceId) error {
			return nil
		},
	}
	rpc := &fakeRPC{
		availableMethodsFn: func(context.Context) (gen.RpcAvailableMethods, error) {
			return nil, fakeAuthError{status: http.StatusForbidden, typ: "forbidden", message: "rpc discover denied"}
		},
		invokeFn: func(_ context.Context, request gen.JsonRpcRequest) (json.RawMessage, bool, error) {
			if request.Method == "notify.only" {
				return nil, true, nil
			}
			if request.Method != "com.omlox.ping" {
				t.Fatalf("unexpected rpc method: %s", request.Method)
			}
			return json.RawMessage(`{"jsonrpc":"2.0","id":"req-1","result":{"message":"pong"}}`), false, nil
		},
	}

	handler := newTestServer(svc, rpc)

	tests := []struct {
		name         string
		method       string
		path         string
		body         string
		wantStatus   int
		wantContains string
	}{
		{name: "list zones", method: http.MethodGet, path: "/v2/zones", wantStatus: http.StatusOK, wantContains: `"type":"rfid"`},
		{name: "create zone", method: http.MethodPost, path: "/v2/zones", body: `{"type":"rfid"}`, wantStatus: http.StatusCreated, wantContains: zoneID.String()},
		{name: "get zone", method: http.MethodGet, path: "/v2/zones/" + zoneID.String(), wantStatus: http.StatusOK, wantContains: zoneID.String()},
		{name: "update zone", method: http.MethodPut, path: "/v2/zones/" + zoneID.String(), body: `{"id":"11111111-1111-1111-1111-111111111111","type":"uwb"}`, wantStatus: http.StatusOK, wantContains: `"type":"uwb"`},
		{name: "delete zone not found", method: http.MethodDelete, path: "/v2/zones/" + zoneID.String(), wantStatus: http.StatusNotFound, wantContains: `"zone not found"`},
		{name: "list trackables", method: http.MethodGet, path: "/v2/trackables", wantStatus: http.StatusOK, wantContains: `"type":"asset"`},
		{name: "create trackable", method: http.MethodPost, path: "/v2/trackables", body: `{"type":"asset"}`, wantStatus: http.StatusCreated, wantContains: trackableID.String()},
		{name: "get trackable internal error", method: http.MethodGet, path: "/v2/trackables/" + trackableID.String(), wantStatus: http.StatusInternalServerError, wantContains: `"type":"internal_error"`},
		{name: "update trackable invalid body", method: http.MethodPut, path: "/v2/trackables/" + trackableID.String(), body: `{"type":"asset"} {"extra":true}`, wantStatus: http.StatusBadRequest, wantContains: `"invalid request body"`},
		{name: "delete trackable", method: http.MethodDelete, path: "/v2/trackables/" + trackableID.String(), wantStatus: http.StatusNoContent},
		{name: "list providers", method: http.MethodGet, path: "/v2/providers", wantStatus: http.StatusOK, wantContains: `"provider-a"`},
		{name: "create provider internal error", method: http.MethodPost, path: "/v2/providers", body: `{"type":"uwb"}`, wantStatus: http.StatusInternalServerError, wantContains: `"write failed"`},
		{name: "get provider", method: http.MethodGet, path: "/v2/providers/provider-a", wantStatus: http.StatusOK, wantContains: `"provider-a"`},
		{name: "update provider", method: http.MethodPut, path: "/v2/providers/provider-a", body: `{"type":"uwb"}`, wantStatus: http.StatusOK, wantContains: `"provider-a"`},
		{name: "delete provider", method: http.MethodDelete, path: "/v2/providers/provider-a", wantStatus: http.StatusNoContent},
		{name: "list fences", method: http.MethodGet, path: "/v2/fences", wantStatus: http.StatusOK, wantContains: fenceID.String()},
		{name: "create fence oversized body", method: http.MethodPost, path: "/v2/fences", body: `{"geometry":{"type":"Polygon","coordinates":[[[1,2],[3,4],[1,2]]]},"extension":{"payload":"abcdefghijklmnopqrstuvwxyz"}}`, wantStatus: http.StatusBadRequest, wantContains: `"invalid request body"`},
		{name: "get fence", method: http.MethodGet, path: "/v2/fences/" + fenceID.String(), wantStatus: http.StatusOK, wantContains: fenceID.String()},
		{name: "delete fence", method: http.MethodDelete, path: "/v2/fences/" + fenceID.String(), wantStatus: http.StatusNoContent},
		{name: "rpc available auth error", method: http.MethodGet, path: "/v2/rpc/available", wantStatus: http.StatusForbidden, wantContains: `"rpc discover denied"`},
		{name: "unknown path", method: http.MethodGet, path: "/v2/does-not-exist", wantStatus: http.StatusNotFound, wantContains: `"path /v2/does-not-exist does not exist"`},
		{name: "invalid path parameter", method: http.MethodGet, path: "/v2/zones/not-a-uuid", wantStatus: http.StatusBadRequest, wantContains: `"path parameter \"zoneId\" has invalid value \"not-a-uuid\"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("unexpected status: got %d want %d body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantContains != "" && !strings.Contains(rec.Body.String(), tc.wantContains) {
				t.Fatalf("response %q does not contain %q", rec.Body.String(), tc.wantContains)
			}
		})
	}
}

func TestHandlerDirectBodiesCoverRawAndTypedEndpoints(t *testing.T) {
	t.Parallel()

	fenceID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	svc := &fakeService{
		processLocationsFn: func(context.Context, []gen.Location) error {
			return &hub.HTTPError{Status: http.StatusBadRequest, Type: "bad_request", Message: "provider_id is required"}
		},
		processProximitiesFn: func(context.Context, []gen.Proximity) error {
			return nil
		},
		createFenceFn: func(context.Context, json.RawMessage) (gen.Fence, error) {
			return gen.Fence{Id: fenceID}, nil
		},
		updateFenceFn: func(context.Context, gen.FenceId, json.RawMessage) (gen.Fence, error) {
			return gen.Fence{Id: fenceID}, nil
		},
	}
	rpcBridge := &fakeRPC{
		availableMethodsFn: func(context.Context) (gen.RpcAvailableMethods, error) { return nil, nil },
		invokeFn: func(_ context.Context, request gen.JsonRpcRequest) (json.RawMessage, bool, error) {
			if request.Method == "notify.only" {
				return nil, true, nil
			}
			return json.RawMessage(`{"jsonrpc":"2.0","id":"req-1","result":{"message":"pong"}}`), false, nil
		},
	}
	h := New(Dependencies{Service: svc, RPC: rpcBridge, RequestBodyLimitBytes: 512})

	point := gen.Point{Type: "Point"}
	if err := point.Coordinates.FromGeoJsonPosition2D(gen.GeoJsonPosition2D{1, 2}); err != nil {
		t.Fatalf("point setup failed: %v", err)
	}
	locationBody, err := json.Marshal([]gen.Location{{
		ProviderId:   "provider-a",
		ProviderType: "uwb",
		Source:       "pub",
		Position:     point,
	}})
	if err != nil {
		t.Fatalf("marshal locations failed: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v2/providers/locations", bytes.NewReader(locationBody))
	rec := httptest.NewRecorder()
	h.PostProviderLocations(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "provider_id is required") {
		t.Fatalf("unexpected locations response: %d %s", rec.Code, rec.Body.String())
	}

	proximityBody, err := json.Marshal([]gen.Proximity{{ProviderId: "provider-a", ProviderType: "uwb", Source: "pub"}})
	if err != nil {
		t.Fatalf("marshal proximities failed: %v", err)
	}
	req = httptest.NewRequest(http.MethodPost, "/v2/providers/proximities", bytes.NewReader(proximityBody))
	rec = httptest.NewRecorder()
	h.PostProviderProximities(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("unexpected proximity status: %d body=%s", rec.Code, rec.Body.String())
	}

	fenceBody := `{"crs":"local","zone_id":"zone-a","region":{"type":"Point","coordinates":[1,2]},"radius":5}`
	req = httptest.NewRequest(http.MethodPost, "/v2/fences", strings.NewReader(fenceBody))
	rec = httptest.NewRecorder()
	h.CreateFence(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("unexpected create fence status: %d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/v2/fences/"+fenceID.String(), strings.NewReader(`{"id":"33333333-3333-3333-3333-333333333333","crs":"local","zone_id":"zone-a","region":{"type":"Point","coordinates":[1,2]},"radius":5}`))
	rec = httptest.NewRecorder()
	h.UpdateFence(rec, req, fenceID)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected update fence status: %d body=%s", rec.Code, rec.Body.String())
	}

	var notifyID gen.JsonRpcRequest_Id
	if err := notifyID.FromJsonRpcRequestId0("req-1"); err != nil {
		t.Fatalf("rpc id setup failed: %v", err)
	}
	notifyBody, err := json.Marshal(gen.JsonRpcRequest{
		Jsonrpc: "2.0",
		Method:  "notify.only",
		Params: &gen.JsonRpcRequest_Params{
			UnderscoreCallerId: stringPtr("caller-1"),
		},
	})
	if err != nil {
		t.Fatalf("marshal notify body failed: %v", err)
	}
	req = httptest.NewRequest(http.MethodPut, "/v2/rpc", bytes.NewReader(notifyBody))
	rec = httptest.NewRecorder()
	h.PutRPC(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unexpected notify status: %d body=%s", rec.Code, rec.Body.String())
	}

	requestBody, err := json.Marshal(gen.JsonRpcRequest{
		Jsonrpc: "2.0",
		Id:      &notifyID,
		Method:  "com.omlox.ping",
	})
	if err != nil {
		t.Fatalf("marshal rpc body failed: %v", err)
	}
	req = httptest.NewRequest(http.MethodPut, "/v2/rpc", bytes.NewReader(requestBody))
	rec = httptest.NewRecorder()
	h.PutRPC(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"message":"pong"`) {
		t.Fatalf("unexpected rpc response: %d %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerStubbedOmloxRoutesReturnNotImplemented(t *testing.T) {
	t.Parallel()

	zoneID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	trackableID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	fenceID := uuid.MustParse("33333333-3333-3333-3333-333333333333")

	handler := newTestServer(&fakeService{}, &fakeRPC{})
	tests := []struct {
		method string
		path   string
		body   string
		status int
	}{
		{method: http.MethodDelete, path: "/v2/zones", status: http.StatusNoContent},
		{method: http.MethodGet, path: "/v2/zones/summary", status: http.StatusOK},
		{method: http.MethodPut, path: "/v2/zones/" + zoneID.String() + "/transform", body: `{}`, status: http.StatusNotImplemented},
		{method: http.MethodGet, path: "/v2/zones/" + zoneID.String() + "/createfence", status: http.StatusNotImplemented},
		{method: http.MethodDelete, path: "/v2/trackables", status: http.StatusNoContent},
		{method: http.MethodGet, path: "/v2/trackables/summary", status: http.StatusOK},
		{method: http.MethodGet, path: "/v2/trackables/motions", status: http.StatusNotImplemented},
		{method: http.MethodGet, path: "/v2/trackables/" + trackableID.String() + "/fences", status: http.StatusNotImplemented},
		{method: http.MethodGet, path: "/v2/trackables/" + trackableID.String() + "/location", status: http.StatusNotImplemented},
		{method: http.MethodGet, path: "/v2/trackables/" + trackableID.String() + "/locations", status: http.StatusNotImplemented},
		{method: http.MethodGet, path: "/v2/trackables/" + trackableID.String() + "/motion", status: http.StatusNotImplemented},
		{method: http.MethodGet, path: "/v2/trackables/" + trackableID.String() + "/providers", status: http.StatusNotImplemented},
		{method: http.MethodGet, path: "/v2/trackables/" + trackableID.String() + "/sensors", status: http.StatusNotImplemented},
		{method: http.MethodDelete, path: "/v2/providers", status: http.StatusNoContent},
		{method: http.MethodGet, path: "/v2/providers/summary", status: http.StatusOK},
		{method: http.MethodGet, path: "/v2/providers/locations", status: http.StatusNotImplemented},
		{method: http.MethodPut, path: "/v2/providers/locations", body: `[]`, status: http.StatusNotImplemented},
		{method: http.MethodDelete, path: "/v2/providers/locations", status: http.StatusNotImplemented},
		{method: http.MethodPut, path: "/v2/providers/proximities", body: `[]`, status: http.StatusNotImplemented},
		{method: http.MethodGet, path: "/v2/providers/provider-a/fences", status: http.StatusNotImplemented},
		{method: http.MethodGet, path: "/v2/providers/provider-a/location", status: http.StatusNotImplemented},
		{method: http.MethodPut, path: "/v2/providers/provider-a/location", body: `{}`, status: http.StatusNotImplemented},
		{method: http.MethodDelete, path: "/v2/providers/provider-a/location", status: http.StatusNotImplemented},
		{method: http.MethodPut, path: "/v2/providers/provider-a/proximity", body: `{}`, status: http.StatusNotImplemented},
		{method: http.MethodGet, path: "/v2/providers/provider-a/sensors", status: http.StatusOK},
		{method: http.MethodPut, path: "/v2/providers/provider-a/sensors", body: `{}`, status: http.StatusOK},
		{method: http.MethodDelete, path: "/v2/fences", status: http.StatusNoContent},
		{method: http.MethodGet, path: "/v2/fences/summary", status: http.StatusOK},
		{method: http.MethodGet, path: "/v2/fences/" + fenceID.String() + "/providers", status: http.StatusNotImplemented},
		{method: http.MethodGet, path: "/v2/fences/" + fenceID.String() + "/locations", status: http.StatusNotImplemented},
	}

	for _, tc := range tests {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("%s %s: got %d want %d body=%s", tc.method, tc.path, rec.Code, tc.status, rec.Body.String())
		}
		if tc.status == http.StatusNotImplemented && !strings.Contains(rec.Body.String(), `"type":"not_implemented"`) {
			t.Fatalf("%s %s: expected not_implemented body, got %s", tc.method, tc.path, rec.Body.String())
		}
	}
}

func TestDecodeJSONBodyAcceptsSingleDocument(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/v2/trackables", strings.NewReader(`{"type":"asset"}`))
	rec := httptest.NewRecorder()

	var body gen.TrackableWrite
	err := decodeJSONBody(rec, req, testRequestBodyLimitBytes, &body)
	if err != nil {
		t.Fatalf("decodeJSONBody returned error: %v", err)
	}
	if body.Type != "asset" {
		t.Fatalf("unexpected type: %q", body.Type)
	}
}

func TestReadRawBodyAcceptsArbitraryJSON(t *testing.T) {
	t.Parallel()

	input := `{"type":"Polygon","coordinates":[[[1,2],[3,4],[1,2]]],"extension":{"unknown":true}}`
	req := httptest.NewRequest(http.MethodPost, "/v2/fences", strings.NewReader(input))
	rec := httptest.NewRecorder()

	raw, err := readRawBody(rec, req, 256)
	if err != nil {
		t.Fatalf("readRawBody returned error: %v", err)
	}

	var got any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal raw body failed: %v", err)
	}
	var want any
	if err := json.Unmarshal([]byte(input), &want); err != nil {
		t.Fatalf("unmarshal input failed: %v", err)
	}
	if !jsonEqual(got, want) {
		t.Fatal("raw JSON body did not round-trip")
	}
}

func newTestServer(svc Service, rpcBridge RPCBridge) http.Handler {
	router := chi.NewRouter()
	router.NotFound(WriteNotFound)
	router.MethodNotAllowed(WriteMethodNotAllowed)
	return gen.HandlerWithOptions(New(Dependencies{
		Service:               svc,
		RPC:                   rpcBridge,
		RequestBodyLimitBytes: testRequestBodyLimitBytes,
	}), gen.ChiServerOptions{
		BaseRouter:       router,
		ErrorHandlerFunc: WriteRequestError,
	})
}

func jsonEqual(a, b any) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(left) == string(right)
}

func stringPtr(v string) *string { return &v }

type fakeService struct {
	listZonesFn          func(context.Context) ([]gen.Zone, error)
	createZoneFn         func(context.Context, json.RawMessage) (gen.Zone, error)
	getZoneFn            func(context.Context, gen.ZoneId) (gen.Zone, error)
	updateZoneFn         func(context.Context, gen.ZoneId, json.RawMessage) (gen.Zone, error)
	deleteZoneFn         func(context.Context, gen.ZoneId) error
	listTrackablesFn     func(context.Context) ([]gen.Trackable, error)
	createTrackableFn    func(context.Context, gen.TrackableWrite) (gen.Trackable, error)
	getTrackableFn       func(context.Context, gen.TrackableId) (gen.Trackable, error)
	updateTrackableFn    func(context.Context, gen.TrackableId, gen.TrackableWrite) (gen.Trackable, error)
	deleteTrackableFn    func(context.Context, gen.TrackableId) error
	listProvidersFn      func(context.Context) ([]gen.LocationProvider, error)
	createProviderFn     func(context.Context, gen.LocationProviderWrite) (gen.LocationProvider, error)
	getProviderFn        func(context.Context, gen.ProviderId) (gen.LocationProvider, error)
	updateProviderFn     func(context.Context, gen.ProviderId, gen.LocationProviderWrite) (gen.LocationProvider, error)
	deleteProviderFn     func(context.Context, gen.ProviderId) error
	processLocationsFn   func(context.Context, []gen.Location) error
	processProximitiesFn func(context.Context, []gen.Proximity) error
	listFencesFn         func(context.Context) ([]gen.Fence, error)
	createFenceFn        func(context.Context, json.RawMessage) (gen.Fence, error)
	getFenceFn           func(context.Context, gen.FenceId) (gen.Fence, error)
	updateFenceFn        func(context.Context, gen.FenceId, json.RawMessage) (gen.Fence, error)
	deleteFenceFn        func(context.Context, gen.FenceId) error
}

func (f *fakeService) ListZones(ctx context.Context) ([]gen.Zone, error) {
	if f.listZonesFn == nil {
		return nil, nil
	}
	return f.listZonesFn(ctx)
}
func (f *fakeService) CreateZone(ctx context.Context, body json.RawMessage) (gen.Zone, error) {
	if f.createZoneFn == nil {
		return gen.Zone{}, nil
	}
	return f.createZoneFn(ctx, body)
}
func (f *fakeService) GetZone(ctx context.Context, id gen.ZoneId) (gen.Zone, error) {
	if f.getZoneFn == nil {
		return gen.Zone{}, nil
	}
	return f.getZoneFn(ctx, id)
}
func (f *fakeService) UpdateZone(ctx context.Context, id gen.ZoneId, body json.RawMessage) (gen.Zone, error) {
	if f.updateZoneFn == nil {
		return gen.Zone{}, nil
	}
	return f.updateZoneFn(ctx, id, body)
}
func (f *fakeService) DeleteZone(ctx context.Context, id gen.ZoneId) error {
	if f.deleteZoneFn == nil {
		return nil
	}
	return f.deleteZoneFn(ctx, id)
}
func (f *fakeService) ListTrackables(ctx context.Context) ([]gen.Trackable, error) {
	if f.listTrackablesFn == nil {
		return nil, nil
	}
	return f.listTrackablesFn(ctx)
}
func (f *fakeService) CreateTrackable(ctx context.Context, body gen.TrackableWrite) (gen.Trackable, error) {
	if f.createTrackableFn == nil {
		return gen.Trackable{}, nil
	}
	return f.createTrackableFn(ctx, body)
}
func (f *fakeService) GetTrackable(ctx context.Context, id gen.TrackableId) (gen.Trackable, error) {
	if f.getTrackableFn == nil {
		return gen.Trackable{}, nil
	}
	return f.getTrackableFn(ctx, id)
}
func (f *fakeService) UpdateTrackable(ctx context.Context, id gen.TrackableId, body gen.TrackableWrite) (gen.Trackable, error) {
	if f.updateTrackableFn == nil {
		return gen.Trackable{}, nil
	}
	return f.updateTrackableFn(ctx, id, body)
}
func (f *fakeService) DeleteTrackable(ctx context.Context, id gen.TrackableId) error {
	if f.deleteTrackableFn == nil {
		return nil
	}
	return f.deleteTrackableFn(ctx, id)
}
func (f *fakeService) ListProviders(ctx context.Context) ([]gen.LocationProvider, error) {
	if f.listProvidersFn == nil {
		return nil, nil
	}
	return f.listProvidersFn(ctx)
}
func (f *fakeService) CreateProvider(ctx context.Context, body gen.LocationProviderWrite) (gen.LocationProvider, error) {
	if f.createProviderFn == nil {
		return gen.LocationProvider{}, nil
	}
	return f.createProviderFn(ctx, body)
}
func (f *fakeService) GetProvider(ctx context.Context, id gen.ProviderId) (gen.LocationProvider, error) {
	if f.getProviderFn == nil {
		return gen.LocationProvider{}, nil
	}
	return f.getProviderFn(ctx, id)
}
func (f *fakeService) UpdateProvider(ctx context.Context, id gen.ProviderId, body gen.LocationProviderWrite) (gen.LocationProvider, error) {
	if f.updateProviderFn == nil {
		return gen.LocationProvider{}, nil
	}
	return f.updateProviderFn(ctx, id, body)
}
func (f *fakeService) DeleteProvider(ctx context.Context, id gen.ProviderId) error {
	if f.deleteProviderFn == nil {
		return nil
	}
	return f.deleteProviderFn(ctx, id)
}
func (f *fakeService) ProcessLocations(ctx context.Context, locations []gen.Location) error {
	if f.processLocationsFn == nil {
		return nil
	}
	return f.processLocationsFn(ctx, locations)
}
func (f *fakeService) ProcessProximities(ctx context.Context, proximities []gen.Proximity) error {
	if f.processProximitiesFn == nil {
		return nil
	}
	return f.processProximitiesFn(ctx, proximities)
}
func (f *fakeService) ListFences(ctx context.Context) ([]gen.Fence, error) {
	if f.listFencesFn == nil {
		return nil, nil
	}
	return f.listFencesFn(ctx)
}
func (f *fakeService) CreateFence(ctx context.Context, body json.RawMessage) (gen.Fence, error) {
	if f.createFenceFn == nil {
		return gen.Fence{}, nil
	}
	return f.createFenceFn(ctx, body)
}
func (f *fakeService) GetFence(ctx context.Context, id gen.FenceId) (gen.Fence, error) {
	if f.getFenceFn == nil {
		return gen.Fence{}, nil
	}
	return f.getFenceFn(ctx, id)
}
func (f *fakeService) UpdateFence(ctx context.Context, id gen.FenceId, body json.RawMessage) (gen.Fence, error) {
	if f.updateFenceFn == nil {
		return gen.Fence{}, nil
	}
	return f.updateFenceFn(ctx, id, body)
}
func (f *fakeService) DeleteFence(ctx context.Context, id gen.FenceId) error {
	if f.deleteFenceFn == nil {
		return nil
	}
	return f.deleteFenceFn(ctx, id)
}

type fakeRPC struct {
	availableMethodsFn func(context.Context) (gen.RpcAvailableMethods, error)
	invokeFn           func(context.Context, gen.JsonRpcRequest) (json.RawMessage, bool, error)
}

func (f *fakeRPC) AvailableMethods(ctx context.Context) (gen.RpcAvailableMethods, error) {
	if f.availableMethodsFn == nil {
		return nil, nil
	}
	return f.availableMethodsFn(ctx)
}

func (f *fakeRPC) Invoke(ctx context.Context, request gen.JsonRpcRequest) (json.RawMessage, bool, error) {
	if f.invokeFn == nil {
		return nil, false, nil
	}
	return f.invokeFn(ctx, request)
}

type fakeAuthError struct {
	status  int
	typ     string
	message string
}

func (e fakeAuthError) Error() string   { return e.message }
func (e fakeAuthError) Status() int     { return e.status }
func (e fakeAuthError) Type() string    { return e.typ }
func (e fakeAuthError) Message() string { return e.message }
