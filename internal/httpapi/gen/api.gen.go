package gen

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Zone struct {
	ID                      string         `json:"id"`
	Type                    string         `json:"type"`
	ForeignID               string         `json:"foreign_id,omitempty"`
	IncompleteConfiguration bool           `json:"incomplete_configuration,omitempty"`
	Properties              map[string]any `json:"properties,omitempty"`
}

type Trackable struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Name       string         `json:"name,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
}

type LocationProvider struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Name       string         `json:"name,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
}

type Location struct {
	Source       string         `json:"source"`
	ProviderType string         `json:"provider_type"`
	ProviderID   string         `json:"provider_id"`
	Position     map[string]any `json:"position,omitempty"`
	Properties   map[string]any `json:"properties,omitempty"`
}

type Proximity struct {
	Source       string         `json:"source"`
	ProviderType string         `json:"provider_type"`
	ProviderID   string         `json:"provider_id"`
	Properties   map[string]any `json:"properties,omitempty"`
}

type Fence struct {
	ID         string         `json:"id"`
	Name       string         `json:"name,omitempty"`
	ForeignID  string         `json:"foreign_id,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
}

type StrictServerInterface interface {
	ListZones(http.ResponseWriter, *http.Request)
	CreateZone(http.ResponseWriter, *http.Request)
	GetZone(http.ResponseWriter, *http.Request, string)
	ListTrackables(http.ResponseWriter, *http.Request)
	CreateTrackable(http.ResponseWriter, *http.Request)
	GetTrackable(http.ResponseWriter, *http.Request, string)
	ListProviders(http.ResponseWriter, *http.Request)
	CreateProvider(http.ResponseWriter, *http.Request)
	GetProvider(http.ResponseWriter, *http.Request, string)
	PostProviderLocations(http.ResponseWriter, *http.Request)
	PostProviderProximities(http.ResponseWriter, *http.Request)
	ListFences(http.ResponseWriter, *http.Request)
	CreateFence(http.ResponseWriter, *http.Request)
	GetFence(http.ResponseWriter, *http.Request, string)
	GetRPCAvailable(http.ResponseWriter, *http.Request)
	PutRPC(http.ResponseWriter, *http.Request)
}

func RegisterHandlers(r chi.Router, si StrictServerInterface) {
	r.Route("/v2", func(r chi.Router) {
		r.Get("/zones", si.ListZones)
		r.Post("/zones", si.CreateZone)
		r.Get("/zones/{zoneId}", func(w http.ResponseWriter, req *http.Request) {
			si.GetZone(w, req, chi.URLParam(req, "zoneId"))
		})

		r.Get("/trackables", si.ListTrackables)
		r.Post("/trackables", si.CreateTrackable)
		r.Get("/trackables/{trackableId}", func(w http.ResponseWriter, req *http.Request) {
			si.GetTrackable(w, req, chi.URLParam(req, "trackableId"))
		})

		r.Get("/providers", si.ListProviders)
		r.Post("/providers", si.CreateProvider)
		r.Get("/providers/{providerId}", func(w http.ResponseWriter, req *http.Request) {
			si.GetProvider(w, req, chi.URLParam(req, "providerId"))
		})
		r.Post("/providers/locations", si.PostProviderLocations)
		r.Post("/providers/proximities", si.PostProviderProximities)

		r.Get("/fences", si.ListFences)
		r.Post("/fences", si.CreateFence)
		r.Get("/fences/{fenceId}", func(w http.ResponseWriter, req *http.Request) {
			si.GetFence(w, req, chi.URLParam(req, "fenceId"))
		})

		r.Get("/rpc/available", si.GetRPCAvailable)
		r.Put("/rpc", si.PutRPC)
	})
}

func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
