package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/formation-res/open-rtls-hub/internal/mqtt"
	"github.com/formation-res/open-rtls-hub/internal/state/valkey"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type Dependencies struct {
	Logger *zap.Logger
	DB     *pgxpool.Pool
	Cache  *valkey.Client
	MQTT   *mqtt.Client
}

type Handler struct {
	deps Dependencies
}

func New(deps Dependencies) *Handler {
	return &Handler{deps: deps}
}

func notImplemented(w http.ResponseWriter, endpoint string) {
	message := endpoint + " is scaffolded but not implemented yet"
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(gen.ErrorResponse{
		Type:    "not_implemented",
		Code:    http.StatusNotImplemented,
		Message: &message,
	})
}

func (h *Handler) ListZones(w http.ResponseWriter, _ *http.Request)  { notImplemented(w, "ListZones") }
func (h *Handler) CreateZone(w http.ResponseWriter, _ *http.Request) { notImplemented(w, "CreateZone") }
func (h *Handler) GetZone(w http.ResponseWriter, _ *http.Request, _ gen.ZoneId) {
	notImplemented(w, "GetZone")
}
func (h *Handler) UpdateZone(w http.ResponseWriter, _ *http.Request, _ gen.ZoneId) {
	notImplemented(w, "UpdateZone")
}
func (h *Handler) DeleteZone(w http.ResponseWriter, _ *http.Request, _ gen.ZoneId) {
	notImplemented(w, "DeleteZone")
}
func (h *Handler) ListTrackables(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "ListTrackables")
}
func (h *Handler) CreateTrackable(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "CreateTrackable")
}
func (h *Handler) GetTrackable(w http.ResponseWriter, _ *http.Request, _ gen.TrackableId) {
	notImplemented(w, "GetTrackable")
}
func (h *Handler) UpdateTrackable(w http.ResponseWriter, _ *http.Request, _ gen.TrackableId) {
	notImplemented(w, "UpdateTrackable")
}
func (h *Handler) DeleteTrackable(w http.ResponseWriter, _ *http.Request, _ gen.TrackableId) {
	notImplemented(w, "DeleteTrackable")
}
func (h *Handler) ListProviders(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "ListProviders")
}
func (h *Handler) CreateProvider(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "CreateProvider")
}
func (h *Handler) GetProvider(w http.ResponseWriter, _ *http.Request, _ gen.ProviderId) {
	notImplemented(w, "GetProvider")
}
func (h *Handler) UpdateProvider(w http.ResponseWriter, _ *http.Request, _ gen.ProviderId) {
	notImplemented(w, "UpdateProvider")
}
func (h *Handler) DeleteProvider(w http.ResponseWriter, _ *http.Request, _ gen.ProviderId) {
	notImplemented(w, "DeleteProvider")
}
func (h *Handler) PostProviderLocations(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "PostProviderLocations")
}
func (h *Handler) PostProviderProximities(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "PostProviderProximities")
}
func (h *Handler) ListFences(w http.ResponseWriter, _ *http.Request) { notImplemented(w, "ListFences") }
func (h *Handler) CreateFence(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "CreateFence")
}
func (h *Handler) GetFence(w http.ResponseWriter, _ *http.Request, _ gen.FenceId) {
	notImplemented(w, "GetFence")
}
func (h *Handler) UpdateFence(w http.ResponseWriter, _ *http.Request, _ gen.FenceId) {
	notImplemented(w, "UpdateFence")
}
func (h *Handler) DeleteFence(w http.ResponseWriter, _ *http.Request, _ gen.FenceId) {
	notImplemented(w, "DeleteFence")
}
func (h *Handler) GetRPCAvailable(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "GetRPCAvailable")
}
func (h *Handler) PutRPC(w http.ResponseWriter, _ *http.Request) { notImplemented(w, "PutRPC") }
