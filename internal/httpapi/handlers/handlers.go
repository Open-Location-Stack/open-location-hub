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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(gen.ErrorResponse{
		Code:    "not_implemented",
		Message: endpoint + " is scaffolded but not implemented yet",
	})
}

func (h *Handler) ListZones(w http.ResponseWriter, _ *http.Request)  { notImplemented(w, "ListZones") }
func (h *Handler) CreateZone(w http.ResponseWriter, _ *http.Request) { notImplemented(w, "CreateZone") }
func (h *Handler) GetZone(w http.ResponseWriter, _ *http.Request, _ string) {
	notImplemented(w, "GetZone")
}
func (h *Handler) ListTrackables(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "ListTrackables")
}
func (h *Handler) CreateTrackable(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "CreateTrackable")
}
func (h *Handler) GetTrackable(w http.ResponseWriter, _ *http.Request, _ string) {
	notImplemented(w, "GetTrackable")
}
func (h *Handler) ListProviders(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "ListProviders")
}
func (h *Handler) CreateProvider(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "CreateProvider")
}
func (h *Handler) GetProvider(w http.ResponseWriter, _ *http.Request, _ string) {
	notImplemented(w, "GetProvider")
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
func (h *Handler) GetFence(w http.ResponseWriter, _ *http.Request, _ string) {
	notImplemented(w, "GetFence")
}
func (h *Handler) GetRPCAvailable(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "GetRPCAvailable")
}
func (h *Handler) PutRPC(w http.ResponseWriter, _ *http.Request) { notImplemented(w, "PutRPC") }
