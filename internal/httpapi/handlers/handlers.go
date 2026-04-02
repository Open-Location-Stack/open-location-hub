package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/formation-res/open-rtls-hub/internal/hub"
	"github.com/formation-res/open-rtls-hub/internal/observability"
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

func (h *Handler) ListTrackables(w http.ResponseWriter, r *http.Request) {
	items, err := h.deps.Service.ListTrackables(r.Context())
	writeJSONOrError(w, items, err, http.StatusOK)
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

func (h *Handler) ListProviders(w http.ResponseWriter, r *http.Request) {
	items, err := h.deps.Service.ListProviders(r.Context())
	writeJSONOrError(w, items, err, http.StatusOK)
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

func (h *Handler) PostProviderLocations(w http.ResponseWriter, r *http.Request) {
	var body []gen.Location
	if err := decodeJSONBody(w, r, h.deps.RequestBodyLimitBytes, &body); err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	err := h.deps.Service.ProcessLocations(observability.WithIngestTransport(r.Context(), "http"), body)
	writeAcceptedOrError(w, err)
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

func (h *Handler) ListFences(w http.ResponseWriter, r *http.Request) {
	items, err := h.deps.Service.ListFences(r.Context())
	writeJSONOrError(w, items, err, http.StatusOK)
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
		return &hub.HTTPError{Status: 400, Type: "bad_request", Message: "invalid request body"}
	}
	return nil
}

func readRawBody(w http.ResponseWriter, r *http.Request, limit int64) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := decodeSingleJSONDocument(w, r, limit, &raw); err != nil {
		return nil, &hub.HTTPError{Status: 400, Type: "bad_request", Message: "invalid request body"}
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
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(httpErr.Status)
			_ = json.NewEncoder(w).Encode(gen.ErrorResponse{
				Type:    httpErr.Type,
				Code:    httpErr.Status,
				Message: &httpErr.Message,
			})
			return
		}
		var authErr interface {
			Status() int
			Type() string
			Message() string
		}
		if errors.As(err, &authErr) {
			message := authErr.Message()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(authErr.Status())
			_ = json.NewEncoder(w).Encode(gen.ErrorResponse{
				Type:    authErr.Type(),
				Code:    authErr.Status(),
				Message: &message,
			})
			return
		}
		message := err.Error()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(gen.ErrorResponse{
			Type:    "internal_error",
			Code:    http.StatusInternalServerError,
			Message: &message,
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(successStatus)
	if payload != nil {
		_ = json.NewEncoder(w).Encode(payload)
	}
}
