package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/hub"
	"github.com/formation-res/open-location-hub/internal/observability"
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

// Dependencies groups the runtime collaborators required by Handler.
type Dependencies struct {
	Logger                *zap.Logger
	Service               Service
	RPC                   RPCBridge
	RequestBodyLimitBytes int64
}

// Service captures the hub operations exposed over the REST adapter.
type Service interface {
	ListZones(ctx context.Context) ([]gen.Zone, error)
	CreateZone(ctx context.Context, body json.RawMessage) (gen.Zone, error)
	GetZone(ctx context.Context, id gen.ZoneId) (gen.Zone, error)
	UpdateZone(ctx context.Context, id gen.ZoneId, body json.RawMessage) (gen.Zone, error)
	DeleteZone(ctx context.Context, id gen.ZoneId) error
	ListTrackables(ctx context.Context) ([]gen.Trackable, error)
	CreateTrackable(ctx context.Context, body gen.TrackableWrite) (gen.Trackable, error)
	GetTrackable(ctx context.Context, id gen.TrackableId) (gen.Trackable, error)
	UpdateTrackable(ctx context.Context, id gen.TrackableId, body gen.TrackableWrite) (gen.Trackable, error)
	DeleteTrackable(ctx context.Context, id gen.TrackableId) error
	ListProviders(ctx context.Context) ([]gen.LocationProvider, error)
	CreateProvider(ctx context.Context, body gen.LocationProviderWrite) (gen.LocationProvider, error)
	GetProvider(ctx context.Context, id gen.ProviderId) (gen.LocationProvider, error)
	UpdateProvider(ctx context.Context, id gen.ProviderId, body gen.LocationProviderWrite) (gen.LocationProvider, error)
	DeleteProvider(ctx context.Context, id gen.ProviderId) error
	ProcessLocations(ctx context.Context, locations []gen.Location) error
	ProcessProximities(ctx context.Context, proximities []gen.Proximity) error
	ListFences(ctx context.Context) ([]gen.Fence, error)
	CreateFence(ctx context.Context, body json.RawMessage) (gen.Fence, error)
	GetFence(ctx context.Context, id gen.FenceId) (gen.Fence, error)
	UpdateFence(ctx context.Context, id gen.FenceId, body json.RawMessage) (gen.Fence, error)
	DeleteFence(ctx context.Context, id gen.FenceId) error
}

type extendedService interface {
	GetZoneCreateFence(ctx context.Context, id gen.ZoneId) (gen.Fence, error)
	PutZoneTransform(ctx context.Context, id gen.ZoneId, body json.RawMessage) (map[string]any, error)
	ListProviderLocations(ctx context.Context) ([]gen.Location, error)
	GetProviderLocation(ctx context.Context, id gen.ProviderId) (gen.Location, error)
	PutProviderLocation(ctx context.Context, id gen.ProviderId, location gen.Location) error
	DeleteProviderLocation(ctx context.Context, id gen.ProviderId) error
	DeleteProviderLocations(ctx context.Context) error
	PutProviderLocations(ctx context.Context, locations []gen.Location) error
	PutProviderProximity(ctx context.Context, id gen.ProviderId, proximity gen.Proximity) error
	PutProviderProximities(ctx context.Context, proximities []gen.Proximity) error
	ListProviderFences(ctx context.Context, id gen.ProviderId) ([]gen.Fence, error)
	GetTrackableLocation(ctx context.Context, id gen.TrackableId) (gen.Location, error)
	ListTrackableLocations(ctx context.Context, id gen.TrackableId) ([]gen.Location, error)
	GetTrackableMotion(ctx context.Context, id gen.TrackableId) (gen.TrackableMotion, error)
	ListTrackableMotions(ctx context.Context) ([]gen.TrackableMotion, error)
	ListTrackableFences(ctx context.Context, id gen.TrackableId) ([]gen.Fence, error)
	ListTrackableProviders(ctx context.Context, id gen.TrackableId) ([]gen.LocationProvider, error)
	ListFenceLocations(ctx context.Context, id gen.FenceId) ([]gen.Location, error)
	ListFenceProviders(ctx context.Context, id gen.FenceId) ([]gen.LocationProvider, error)
}

// RPCBridge captures the JSON-RPC operations exposed over HTTP.
type RPCBridge interface {
	AvailableMethods(ctx context.Context) (gen.RpcAvailableMethods, error)
	Invoke(ctx context.Context, request gen.JsonRpcRequest) (json.RawMessage, bool, error)
}

// Handler implements the generated OpenAPI server interface.
type Handler struct {
	deps Dependencies
}

// New constructs a Handler from the supplied dependencies.
func New(deps Dependencies) *Handler {
	return &Handler{deps: deps}
}

func (h *Handler) ListZones(w http.ResponseWriter, r *http.Request) {
	items, err := h.deps.Service.ListZones(r.Context())
	writeJSONOrError(w, items, err, http.StatusOK)
}

func (h *Handler) DeleteZones(w http.ResponseWriter, r *http.Request) {
	items, err := h.deps.Service.ListZones(r.Context())
	if err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	for _, item := range items {
		if err := h.deps.Service.DeleteZone(r.Context(), item.Id); err != nil {
			writeJSONOrError(w, nil, err, 0)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) CreateZone(w http.ResponseWriter, r *http.Request) {
	body, err := readRawBody(w, r, h.deps.RequestBodyLimitBytes)
	if err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	item, err := h.deps.Service.CreateZone(r.Context(), body)
	writeJSONOrError(w, item, err, http.StatusCreated)
}

func (h *Handler) GetZone(w http.ResponseWriter, r *http.Request, id gen.ZoneId) {
	item, err := h.deps.Service.GetZone(r.Context(), id)
	writeJSONOrError(w, item, err, http.StatusOK)
}

func (h *Handler) UpdateZone(w http.ResponseWriter, r *http.Request, id gen.ZoneId) {
	body, err := readRawBody(w, r, h.deps.RequestBodyLimitBytes)
	if err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	item, err := h.deps.Service.UpdateZone(r.Context(), id, body)
	writeJSONOrError(w, item, err, http.StatusOK)
}

func (h *Handler) DeleteZone(w http.ResponseWriter, r *http.Request, id gen.ZoneId) {
	err := h.deps.Service.DeleteZone(r.Context(), id)
	writeNoContentOrError(w, err)
}

func (h *Handler) GetZonesSummary(w http.ResponseWriter, r *http.Request) {
	items, err := h.deps.Service.ListZones(r.Context())
	writeJSONOrError(w, summaryResponse("zones", len(items)), err, http.StatusOK)
}

func (h *Handler) PutZoneTransform(w http.ResponseWriter, r *http.Request, id gen.ZoneId) {
	body, err := readRawBody(w, r, h.deps.RequestBodyLimitBytes)
	if err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	svc, ok := h.extendedService()
	if !ok {
		writeStubNotImplemented(w, "zone transform is not implemented")
		return
	}
	payload, err := svc.PutZoneTransform(r.Context(), id, body)
	writeJSONOrError(w, payload, err, http.StatusOK)
}

func (h *Handler) GetZoneCreateFence(w http.ResponseWriter, r *http.Request, id gen.ZoneId) {
	svc, ok := h.extendedService()
	if !ok {
		writeStubNotImplemented(w, "zone createfence is not implemented")
		return
	}
	item, err := svc.GetZoneCreateFence(r.Context(), id)
	writeJSONOrError(w, item, err, http.StatusOK)
}

func (h *Handler) ListTrackables(w http.ResponseWriter, r *http.Request) {
	items, err := h.deps.Service.ListTrackables(r.Context())
	writeJSONOrError(w, items, err, http.StatusOK)
}

func (h *Handler) DeleteTrackables(w http.ResponseWriter, r *http.Request) {
	items, err := h.deps.Service.ListTrackables(r.Context())
	if err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	for _, item := range items {
		if err := h.deps.Service.DeleteTrackable(r.Context(), item.Id); err != nil {
			writeJSONOrError(w, nil, err, 0)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) CreateTrackable(w http.ResponseWriter, r *http.Request) {
	var body gen.TrackableWrite
	if err := decodeJSONBody(w, r, h.deps.RequestBodyLimitBytes, &body); err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	item, err := h.deps.Service.CreateTrackable(r.Context(), body)
	writeJSONOrError(w, item, err, http.StatusCreated)
}

func (h *Handler) GetTrackable(w http.ResponseWriter, r *http.Request, id gen.TrackableId) {
	item, err := h.deps.Service.GetTrackable(r.Context(), id)
	writeJSONOrError(w, item, err, http.StatusOK)
}

func (h *Handler) UpdateTrackable(w http.ResponseWriter, r *http.Request, id gen.TrackableId) {
	var body gen.TrackableWrite
	if err := decodeJSONBody(w, r, h.deps.RequestBodyLimitBytes, &body); err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	item, err := h.deps.Service.UpdateTrackable(r.Context(), id, body)
	writeJSONOrError(w, item, err, http.StatusOK)
}

func (h *Handler) DeleteTrackable(w http.ResponseWriter, r *http.Request, id gen.TrackableId) {
	err := h.deps.Service.DeleteTrackable(r.Context(), id)
	writeNoContentOrError(w, err)
}

func (h *Handler) GetTrackablesSummary(w http.ResponseWriter, r *http.Request) {
	items, err := h.deps.Service.ListTrackables(r.Context())
	writeJSONOrError(w, summaryResponse("trackables", len(items)), err, http.StatusOK)
}

func (h *Handler) GetTrackableMotions(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.extendedService()
	if !ok {
		writeStubNotImplemented(w, "trackable motions are not implemented")
		return
	}
	items, err := svc.ListTrackableMotions(r.Context())
	writeJSONOrError(w, items, err, http.StatusOK)
}

func (h *Handler) GetTrackableFences(w http.ResponseWriter, r *http.Request, id gen.TrackableId) {
	svc, ok := h.extendedService()
	if !ok {
		writeStubNotImplemented(w, "trackable fences are not implemented")
		return
	}
	items, err := svc.ListTrackableFences(r.Context(), id)
	writeJSONOrError(w, items, err, http.StatusOK)
}

func (h *Handler) GetTrackableLocation(w http.ResponseWriter, r *http.Request, id gen.TrackableId) {
	svc, ok := h.extendedService()
	if !ok {
		writeStubNotImplemented(w, "trackable location is not implemented")
		return
	}
	item, err := svc.GetTrackableLocation(r.Context(), id)
	writeJSONOrError(w, item, err, http.StatusOK)
}

func (h *Handler) GetTrackableLocations(w http.ResponseWriter, r *http.Request, id gen.TrackableId) {
	svc, ok := h.extendedService()
	if !ok {
		writeStubNotImplemented(w, "trackable locations are not implemented")
		return
	}
	items, err := svc.ListTrackableLocations(r.Context(), id)
	writeJSONOrError(w, items, err, http.StatusOK)
}

func (h *Handler) GetTrackableMotion(w http.ResponseWriter, r *http.Request, id gen.TrackableId) {
	svc, ok := h.extendedService()
	if !ok {
		writeStubNotImplemented(w, "trackable motion is not implemented")
		return
	}
	item, err := svc.GetTrackableMotion(r.Context(), id)
	writeJSONOrError(w, item, err, http.StatusOK)
}

func (h *Handler) GetTrackableProviders(w http.ResponseWriter, r *http.Request, id gen.TrackableId) {
	svc, ok := h.extendedService()
	if !ok {
		writeStubNotImplemented(w, "trackable providers are not implemented")
		return
	}
	items, err := svc.ListTrackableProviders(r.Context(), id)
	writeJSONOrError(w, items, err, http.StatusOK)
}

func (h *Handler) GetTrackableSensors(w http.ResponseWriter, r *http.Request, id gen.TrackableId) {
	svc, ok := h.extendedService()
	if !ok {
		writeStubNotImplemented(w, "trackable sensors are not implemented")
		return
	}
	providers, err := svc.ListTrackableProviders(r.Context(), id)
	if err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	sensors := make(map[string]any, len(providers))
	for _, provider := range providers {
		if provider.Sensors != nil {
			sensors[provider.Id] = *provider.Sensors
		}
	}
	writeJSONOrError(w, sensors, nil, http.StatusOK)
}

func (h *Handler) ListProviders(w http.ResponseWriter, r *http.Request) {
	items, err := h.deps.Service.ListProviders(r.Context())
	writeJSONOrError(w, items, err, http.StatusOK)
}

func (h *Handler) DeleteProviders(w http.ResponseWriter, r *http.Request) {
	items, err := h.deps.Service.ListProviders(r.Context())
	if err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	for _, item := range items {
		if err := h.deps.Service.DeleteProvider(r.Context(), item.Id); err != nil {
			writeJSONOrError(w, nil, err, 0)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) CreateProvider(w http.ResponseWriter, r *http.Request) {
	var body gen.LocationProviderWrite
	if err := decodeJSONBody(w, r, h.deps.RequestBodyLimitBytes, &body); err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	item, err := h.deps.Service.CreateProvider(r.Context(), body)
	writeJSONOrError(w, item, err, http.StatusCreated)
}

func (h *Handler) GetProvider(w http.ResponseWriter, r *http.Request, id gen.ProviderId) {
	item, err := h.deps.Service.GetProvider(r.Context(), id)
	writeJSONOrError(w, item, err, http.StatusOK)
}

func (h *Handler) UpdateProvider(w http.ResponseWriter, r *http.Request, id gen.ProviderId) {
	var body gen.LocationProviderWrite
	if err := decodeJSONBody(w, r, h.deps.RequestBodyLimitBytes, &body); err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	item, err := h.deps.Service.UpdateProvider(r.Context(), id, body)
	writeJSONOrError(w, item, err, http.StatusOK)
}

func (h *Handler) DeleteProvider(w http.ResponseWriter, r *http.Request, id gen.ProviderId) {
	err := h.deps.Service.DeleteProvider(r.Context(), id)
	writeNoContentOrError(w, err)
}

func (h *Handler) GetProvidersSummary(w http.ResponseWriter, r *http.Request) {
	items, err := h.deps.Service.ListProviders(r.Context())
	writeJSONOrError(w, summaryResponse("providers", len(items)), err, http.StatusOK)
}

func (h *Handler) GetProviderLocations(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.extendedService()
	if !ok {
		writeStubNotImplemented(w, "provider locations are not implemented")
		return
	}
	items, err := svc.ListProviderLocations(r.Context())
	writeJSONOrError(w, items, err, http.StatusOK)
}

func (h *Handler) PostProviderLocations(w http.ResponseWriter, r *http.Request) {
	var body []gen.Location
	if err := decodeJSONBody(w, r, h.deps.RequestBodyLimitBytes, &body); err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	err := h.deps.Service.ProcessLocations(observability.WithIngestTransport(r.Context(), "http"), body)
	writeAcceptedOrError(w, err)
}

func (h *Handler) PutProviderLocations(w http.ResponseWriter, r *http.Request) {
	var body []gen.Location
	if err := decodeJSONBody(w, r, h.deps.RequestBodyLimitBytes, &body); err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	svc, ok := h.extendedService()
	if !ok {
		writeStubNotImplemented(w, "bulk provider location replace is not implemented")
		return
	}
	err := svc.PutProviderLocations(observability.WithIngestTransport(r.Context(), "http"), body)
	writeAcceptedOrError(w, err)
}

func (h *Handler) DeleteProviderLocations(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.extendedService()
	if !ok {
		writeStubNotImplemented(w, "delete provider locations is not implemented")
		return
	}
	err := svc.DeleteProviderLocations(r.Context())
	writeNoContentOrError(w, err)
}

func (h *Handler) PostProviderProximities(w http.ResponseWriter, r *http.Request) {
	var body []gen.Proximity
	if err := decodeJSONBody(w, r, h.deps.RequestBodyLimitBytes, &body); err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	err := h.deps.Service.ProcessProximities(observability.WithIngestTransport(r.Context(), "http"), body)
	writeAcceptedOrError(w, err)
}

func (h *Handler) PutProviderProximities(w http.ResponseWriter, r *http.Request) {
	var body []gen.Proximity
	if err := decodeJSONBody(w, r, h.deps.RequestBodyLimitBytes, &body); err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	svc, ok := h.extendedService()
	if !ok {
		writeStubNotImplemented(w, "bulk provider proximity replace is not implemented")
		return
	}
	err := svc.PutProviderProximities(observability.WithIngestTransport(r.Context(), "http"), body)
	writeAcceptedOrError(w, err)
}

func (h *Handler) GetProviderFences(w http.ResponseWriter, r *http.Request, id gen.ProviderId) {
	svc, ok := h.extendedService()
	if !ok {
		writeStubNotImplemented(w, "provider fences are not implemented")
		return
	}
	items, err := svc.ListProviderFences(r.Context(), id)
	writeJSONOrError(w, items, err, http.StatusOK)
}

func (h *Handler) GetProviderLocation(w http.ResponseWriter, r *http.Request, id gen.ProviderId) {
	svc, ok := h.extendedService()
	if !ok {
		writeStubNotImplemented(w, "provider location is not implemented")
		return
	}
	item, err := svc.GetProviderLocation(r.Context(), id)
	writeJSONOrError(w, item, err, http.StatusOK)
}

func (h *Handler) PutProviderLocation(w http.ResponseWriter, r *http.Request, id gen.ProviderId) {
	var body gen.Location
	if err := decodeJSONBody(w, r, h.deps.RequestBodyLimitBytes, &body); err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	svc, ok := h.extendedService()
	if !ok {
		writeStubNotImplemented(w, "single provider location update is not implemented")
		return
	}
	err := svc.PutProviderLocation(observability.WithIngestTransport(r.Context(), "http"), id, body)
	writeAcceptedOrError(w, err)
}

func (h *Handler) DeleteProviderLocation(w http.ResponseWriter, r *http.Request, id gen.ProviderId) {
	svc, ok := h.extendedService()
	if !ok {
		writeStubNotImplemented(w, "delete provider location is not implemented")
		return
	}
	err := svc.DeleteProviderLocation(r.Context(), id)
	writeNoContentOrError(w, err)
}

func (h *Handler) PutProviderProximity(w http.ResponseWriter, r *http.Request, id gen.ProviderId) {
	var body gen.Proximity
	if err := decodeJSONBody(w, r, h.deps.RequestBodyLimitBytes, &body); err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	svc, ok := h.extendedService()
	if !ok {
		writeStubNotImplemented(w, "single provider proximity update is not implemented")
		return
	}
	err := svc.PutProviderProximity(observability.WithIngestTransport(r.Context(), "http"), id, body)
	writeAcceptedOrError(w, err)
}

func (h *Handler) GetProviderSensors(w http.ResponseWriter, r *http.Request, id gen.ProviderId) {
	provider, err := h.deps.Service.GetProvider(r.Context(), id)
	if err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	if provider.Sensors == nil {
		writeJSONOrError(w, map[string]any{}, nil, http.StatusOK)
		return
	}
	writeJSONOrError(w, *provider.Sensors, nil, http.StatusOK)
}

func (h *Handler) PutProviderSensors(w http.ResponseWriter, r *http.Request, id gen.ProviderId) {
	var body map[string]any
	if err := decodeJSONBody(w, r, h.deps.RequestBodyLimitBytes, &body); err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	provider, err := h.deps.Service.GetProvider(r.Context(), id)
	if err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	props := gen.ExtensionProperties(body)
	writeBody := gen.LocationProviderWrite(provider)
	writeBody.Sensors = &props
	item, err := h.deps.Service.UpdateProvider(r.Context(), id, writeBody)
	writeJSONOrError(w, item, err, http.StatusOK)
}

func (h *Handler) ListFences(w http.ResponseWriter, r *http.Request) {
	items, err := h.deps.Service.ListFences(r.Context())
	writeJSONOrError(w, items, err, http.StatusOK)
}

func (h *Handler) DeleteFences(w http.ResponseWriter, r *http.Request) {
	items, err := h.deps.Service.ListFences(r.Context())
	if err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	for _, item := range items {
		if err := h.deps.Service.DeleteFence(r.Context(), item.Id); err != nil {
			writeJSONOrError(w, nil, err, 0)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) CreateFence(w http.ResponseWriter, r *http.Request) {
	body, err := readRawBody(w, r, h.deps.RequestBodyLimitBytes)
	if err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	item, err := h.deps.Service.CreateFence(r.Context(), body)
	writeJSONOrError(w, item, err, http.StatusCreated)
}

func (h *Handler) GetFence(w http.ResponseWriter, r *http.Request, id gen.FenceId) {
	item, err := h.deps.Service.GetFence(r.Context(), id)
	writeJSONOrError(w, item, err, http.StatusOK)
}

func (h *Handler) UpdateFence(w http.ResponseWriter, r *http.Request, id gen.FenceId) {
	body, err := readRawBody(w, r, h.deps.RequestBodyLimitBytes)
	if err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	item, err := h.deps.Service.UpdateFence(r.Context(), id, body)
	writeJSONOrError(w, item, err, http.StatusOK)
}

func (h *Handler) DeleteFence(w http.ResponseWriter, r *http.Request, id gen.FenceId) {
	err := h.deps.Service.DeleteFence(r.Context(), id)
	writeNoContentOrError(w, err)
}

func (h *Handler) GetFencesSummary(w http.ResponseWriter, r *http.Request) {
	items, err := h.deps.Service.ListFences(r.Context())
	writeJSONOrError(w, summaryResponse("fences", len(items)), err, http.StatusOK)
}

func (h *Handler) GetFenceProviders(w http.ResponseWriter, r *http.Request, id gen.FenceId) {
	svc, ok := h.extendedService()
	if !ok {
		writeStubNotImplemented(w, "fence providers are not implemented")
		return
	}
	items, err := svc.ListFenceProviders(r.Context(), id)
	writeJSONOrError(w, items, err, http.StatusOK)
}

func (h *Handler) GetFenceLocations(w http.ResponseWriter, r *http.Request, id gen.FenceId) {
	svc, ok := h.extendedService()
	if !ok {
		writeStubNotImplemented(w, "fence locations are not implemented")
		return
	}
	items, err := svc.ListFenceLocations(r.Context(), id)
	writeJSONOrError(w, items, err, http.StatusOK)
}

func (h *Handler) GetRPCAvailable(w http.ResponseWriter, r *http.Request) {
	items, err := h.deps.RPC.AvailableMethods(r.Context())
	writeJSONOrError(w, items, err, http.StatusOK)
}

func (h *Handler) PutRPC(w http.ResponseWriter, r *http.Request) {
	var body gen.JsonRpcRequest
	if err := decodeJSONBody(w, r, h.deps.RequestBodyLimitBytes, &body); err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	response, notifyOnly, err := h.deps.RPC.Invoke(r.Context(), body)
	if err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	if notifyOnly {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(response)
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, limit int64, dst any) error {
	if err := decodeSingleJSONDocument(w, r, limit, dst); err != nil {
		return badRequestError("invalid request body", describeBodyDecodeError(err, limit))
	}
	return nil
}

func readRawBody(w http.ResponseWriter, r *http.Request, limit int64) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := decodeSingleJSONDocument(w, r, limit, &raw); err != nil {
		return nil, badRequestError("invalid request body", describeBodyDecodeError(err, limit))
	}
	return raw, nil
}

func decodeSingleJSONDocument(w http.ResponseWriter, r *http.Request, limit int64, dst any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON content")
		}
		return err
	}
	return nil
}

func writeAcceptedOrError(w http.ResponseWriter, err error) {
	if err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func writeNoContentOrError(w http.ResponseWriter, err error) {
	if err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSONOrError(w http.ResponseWriter, payload any, err error, successStatus int) {
	if err != nil {
		var httpErr *hub.HTTPError
		if errors.As(err, &httpErr) {
			writeErrorResponse(w, httpErr.Status, httpErr.Type, httpErr.Message, httpErr.Details)
			return
		}
		var authErr interface {
			Status() int
			Type() string
			Message() string
		}
		if errors.As(err, &authErr) {
			var detailed interface {
				Details() []string
			}
			var details []string
			if errors.As(err, &detailed) {
				details = detailed.Details()
			}
			writeErrorResponse(w, authErr.Status(), authErr.Type(), authErr.Message(), details)
			return
		}
		message := err.Error()
		writeErrorResponse(w, http.StatusInternalServerError, "internal_error", message, nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(successStatus)
	if payload != nil {
		_ = json.NewEncoder(w).Encode(payload)
	}
}

func writeStubNotImplemented(w http.ResponseWriter, message string) {
	writeJSONOrError(w, nil, &hub.HTTPError{
		Status:  http.StatusNotImplemented,
		Type:    "not_implemented",
		Message: message,
	}, 0)
}

// WriteRequestError renders OpenAPI path and parameter validation failures as
// the standard JSON error envelope.
func WriteRequestError(w http.ResponseWriter, r *http.Request, err error) {
	detail := err.Error()
	var invalidParam *gen.InvalidParamFormatError
	if errors.As(err, &invalidParam) {
		detail = fmt.Sprintf("path parameter %q has invalid value %q: expected %s", invalidParam.ParamName, chiURLParam(r, invalidParam.ParamName), invalidParam.Err.Error())
	}
	writeErrorResponse(w, http.StatusBadRequest, "bad_request", "invalid request parameters", []string{detail})
}

// WriteNotFound renders unmatched routes as a JSON 404 that identifies the
// requested method and path.
func WriteNotFound(w http.ResponseWriter, r *http.Request) {
	message := fmt.Sprintf("path %s does not exist", r.URL.Path)
	details := []string{fmt.Sprintf("%s %s did not match any Open Location Hub REST route", r.Method, r.URL.Path)}
	writeErrorResponse(w, http.StatusNotFound, "not_found", message, details)
}

// WriteMethodNotAllowed renders known paths with unsupported methods as JSON.
func WriteMethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	message := fmt.Sprintf("method %s is not allowed for path %s", r.Method, r.URL.Path)
	details := []string{"use one of the HTTP methods documented for this Open Location Hub REST path"}
	writeErrorResponse(w, http.StatusMethodNotAllowed, "method_not_allowed", message, details)
}

func badRequestError(message string, details []string) error {
	return &hub.HTTPError{Status: http.StatusBadRequest, Type: "bad_request", Message: message, Details: details}
}

func describeBodyDecodeError(err error, limit int64) []string {
	switch {
	case errors.Is(err, io.EOF):
		return []string{"request body is empty; send a single JSON document"}
	case strings.Contains(err.Error(), "http: request body too large"):
		return []string{fmt.Sprintf("request body exceeds the configured limit of %d bytes", limit)}
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return []string{fmt.Sprintf("JSON syntax error at byte offset %d: %s", syntaxErr.Offset, syntaxErr.Error())}
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := typeErr.Field
		if field == "" {
			field = typeErr.Value
		}
		return []string{fmt.Sprintf("JSON field %q has value type %q but must be %s", field, typeErr.Value, typeErr.Type.String())}
	}
	if err.Error() == "unexpected trailing JSON content" {
		return []string{"request body must contain exactly one JSON document; remove trailing content after the first document"}
	}
	return []string{err.Error()}
}

func writeErrorResponse(w http.ResponseWriter, status int, typ, message string, details []string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := gen.ErrorResponse{
		Type:    typ,
		Code:    status,
		Message: &message,
	}
	if len(details) > 0 {
		body.Details = &details
	}
	_ = json.NewEncoder(w).Encode(body)
}

func chiURLParam(r *http.Request, name string) string {
	value := chi.URLParam(r, name)
	if value == "" {
		return "<empty>"
	}
	return value
}

func (h *Handler) extendedService() (extendedService, bool) {
	svc, ok := h.deps.Service.(extendedService)
	return svc, ok
}

func summaryResponse(kind string, count int) map[string]any {
	return map[string]any{
		"type":  kind,
		"count": count,
	}
}
