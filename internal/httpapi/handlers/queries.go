package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/hub"
)

type projectionService interface {
	ProjectLocation(context.Context, gen.Location, string, string) (gen.Location, error)
	ProjectFence(context.Context, gen.Fence, string, string) (gen.Fence, error)
}

type spatialQueryService interface {
	ListTrackableFencesQuery(context.Context, gen.TrackableId, bool) ([]gen.Fence, error)
	ListProviderFencesQuery(context.Context, gen.ProviderId, bool) ([]gen.Fence, error)
	ListFenceLocationsQuery(context.Context, gen.FenceId, bool) ([]gen.Location, error)
	ListFenceProvidersQuery(context.Context, gen.FenceId, bool) ([]gen.LocationProvider, error)
}

type relationService interface {
	ListProviderTrackables(context.Context, gen.ProviderId) ([]gen.Trackable, error)
	ListFenceTrackables(context.Context, gen.FenceId, bool) ([]gen.Trackable, error)
	ListFenceMotions(context.Context, gen.FenceId, bool) ([]gen.TrackableMotion, error)
}

func resourceIDs[T any](items []T) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		switch v := any(item).(type) {
		case gen.Zone:
			ids = append(ids, v.Id.String())
		case gen.Trackable:
			ids = append(ids, v.Id.String())
		case gen.LocationProvider:
			ids = append(ids, v.Id)
		case gen.Fence:
			ids = append(ids, v.Id.String())
		}
	}
	return ids
}

func filterZones(items []gen.Zone, foreignID string) []gen.Zone {
	out := make([]gen.Zone, 0, len(items))
	for _, z := range items {
		if foreignID == "" || z.ForeignId != nil && *z.ForeignId == foreignID {
			out = append(out, z)
		}
	}
	return out
}

func (h *Handler) GetProviderTrackables(w http.ResponseWriter, r *http.Request, id gen.ProviderId) {
	svc, ok := h.deps.Service.(relationService)
	if !ok {
		writeStubNotImplemented(w, "provider associations unavailable")
		return
	}
	items, err := svc.ListProviderTrackables(r.Context(), id)
	writeJSONOrError(w, items, err, http.StatusOK)
}

func (h *Handler) GetFenceTrackables(w http.ResponseWriter, r *http.Request, id gen.FenceId, params gen.GetFenceTrackablesParams) {
	svc, ok := h.deps.Service.(relationService)
	if !ok {
		writeStubNotImplemented(w, "fence associations unavailable")
		return
	}
	items, err := svc.ListFenceTrackables(r.Context(), id, params.SpatialQuery != nil && *params.SpatialQuery)
	writeJSONOrError(w, items, err, http.StatusOK)
}

func (h *Handler) GetFenceMotions(w http.ResponseWriter, r *http.Request, id gen.FenceId, params gen.GetFenceMotionsParams) {
	svc, ok := h.deps.Service.(relationService)
	if !ok {
		writeStubNotImplemented(w, "fence motions unavailable")
		return
	}
	items, err := svc.ListFenceMotions(r.Context(), id, params.SpatialQuery != nil && *params.SpatialQuery)
	h.writeProjected(w, r, items, err)
}

func (h *Handler) writeProjected(w http.ResponseWriter, r *http.Request, value any, err error) {
	if err != nil {
		writeJSONOrError(w, nil, err, 0)
		return
	}
	svc, ok := h.deps.Service.(projectionService)
	if !ok {
		writeStubNotImplemented(w, "coordinate projection unavailable")
		return
	}
	crs, zoneID := r.URL.Query().Get("crs"), r.URL.Query().Get("zone_id")
	if validator, ok := h.deps.Service.(interface {
		ValidateProjection(context.Context, string, string) error
	}); ok {
		if err := validator.ValidateProjection(r.Context(), crs, zoneID); err != nil {
			writeJSONOrError(w, nil, err, 0)
			return
		}
	}
	geo, _ := strconv.ParseBool(r.URL.Query().Get("geojson"))
	location := func(v gen.Location) (any, error) {
		projected, e := svc.ProjectLocation(r.Context(), v, crs, zoneID)
		if e != nil {
			return nil, e
		}
		if geo {
			return geoJSON(projected, "position")
		}
		return projected, nil
	}
	motion := func(v gen.TrackableMotion) (any, error) {
		originalLocation := v.Location
		projected, e := svc.ProjectLocation(r.Context(), v.Location, crs, zoneID)
		if e != nil {
			return nil, e
		}
		v.Location = projected
		if v.Geometry != nil {
			inputCRS := "local"
			if originalLocation.Crs != nil {
				inputCRS = *originalLocation.Crs
			}
			f := gen.Fence{Crs: &inputCRS}
			if originalLocation.Crs == nil || *originalLocation.Crs == "local" {
				f.ZoneId = &originalLocation.Source
			}
			if e = f.Region.FromPolygon(*v.Geometry); e != nil {
				return nil, e
			}
			f, e = svc.ProjectFence(r.Context(), f, crs, zoneID)
			if e != nil {
				return nil, e
			}
			polygon, e := f.Region.AsPolygon()
			if e != nil {
				return nil, e
			}
			v.Geometry = &polygon
		}
		if geo {
			return geoJSONMotion(v)
		}
		return v, nil
	}
	fence := func(v gen.Fence) (any, error) {
		projected, e := svc.ProjectFence(r.Context(), v, crs, zoneID)
		if e != nil {
			return nil, e
		}
		if geo {
			return geoJSON(projected, "region")
		}
		return projected, nil
	}
	var result any
	switch v := value.(type) {
	case gen.Location:
		result, err = location(v)
	case gen.TrackableMotion:
		result, err = motion(v)
	case gen.Fence:
		result, err = fence(v)
	case []gen.Location:
		result, err = mapResults(v, location)
	case []gen.TrackableMotion:
		result, err = mapResults(v, motion)
	case []gen.Fence:
		result, err = mapResults(v, fence)
	default:
		result = value
	}
	writeJSONOrError(w, result, err, http.StatusOK)
}

func mapResults[T any](values []T, fn func(T) (any, error)) ([]any, error) {
	out := make([]any, 0, len(values))
	for _, v := range values {
		p, e := fn(v)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, nil
}

func geoJSON(value any, geometryKey string) (any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var props map[string]any
	if err = json.Unmarshal(raw, &props); err != nil {
		return nil, err
	}
	geometry := props[geometryKey]
	delete(props, geometryKey)
	return map[string]any{"type": "FeatureCollection", "features": []any{map[string]any{"type": "Feature", "geometry": geometry, "properties": props}}}, nil
}

func geoJSONMotion(motion gen.TrackableMotion) (any, error) {
	raw, err := json.Marshal(motion)
	if err != nil {
		return nil, err
	}
	var obj map[string]any
	if err = json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	if motion.Geometry != nil {
		return geoJSON(obj, "geometry")
	}
	obj["position"] = motion.Location.Position
	return geoJSON(obj, "position")
}

func (h *Handler) reprocessTrackable(ctx context.Context, id gen.TrackableId) error {
	svc, ok := h.deps.Service.(interface {
		ReprocessTrackable(context.Context, gen.TrackableId) error
	})
	if !ok {
		return &hub.HTTPError{Status: http.StatusNotImplemented, Type: "not_implemented", Message: "trackable reprocessing unavailable"}
	}
	return svc.ReprocessTrackable(ctx, id)
}
