package hub

import (
	"context"
	"strings"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
)

// ListTrackableFencesQuery distinguishes logical membership retained by timers
// from the purely geometric query explicitly requested by spatial_query=true.
func (s *Service) ListTrackableFencesQuery(ctx context.Context, id gen.TrackableId, spatial bool) ([]gen.Fence, error) {
	if !spatial {
		return s.ListTrackableFences(ctx, id)
	}
	trackable, err := s.GetTrackable(ctx, id)
	if err != nil {
		return nil, err
	}
	location, err := s.GetTrackableLocation(ctx, id)
	if err != nil {
		return []gen.Fence{}, nil
	}
	fences, err := s.ListFences(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Fence, 0)
	for _, fence := range fences {
		if s.fenceIntersectsLocation(ctx, fence, location, trackableRadius(trackable), trackable.Extrusion) {
			out = append(out, fence)
		}
	}
	return out, nil
}

func (s *Service) ListProviderFencesQuery(ctx context.Context, id gen.ProviderId, spatial bool) ([]gen.Fence, error) {
	location, err := s.GetProviderLocation(ctx, id)
	if err != nil {
		if _, e := s.GetProvider(ctx, id); e != nil {
			return nil, err
		}
	}
	out := make([]gen.Fence, 0)
	if spatial {
		if err != nil {
			return out, nil
		}
		fences, e := s.ListFences(ctx)
		if e != nil {
			return nil, e
		}
		for _, fence := range fences {
			if s.fenceIntersectsLocation(ctx, fence, location, 0) {
				out = append(out, fence)
			}
		}
	} else {
		for _, id := range s.processingState().ListInsideFences(providerMembershipKey(id)) {
			if fence, ok := s.fenceByID(ctx, id); ok {
				out = append(out, fence)
			}
		}
	}
	return out, nil
}

func (s *Service) ListFenceLocationsQuery(ctx context.Context, id gen.FenceId, spatial bool) ([]gen.Location, error) {
	fence, err := s.GetFence(ctx, id)
	if err != nil {
		return nil, err
	}
	if !spatial {
		return s.processingState().providerLocationsInFence(id.String()), nil
	}
	out := make([]gen.Location, 0)
	for _, location := range s.processingState().ListLatestLocations() {
		if s.fenceIntersectsLocation(ctx, fence, location, 0) {
			out = append(out, location)
		}
	}
	return out, nil
}

func (s *Service) ListFenceProvidersQuery(ctx context.Context, id gen.FenceId, spatial bool) ([]gen.LocationProvider, error) {
	locations, err := s.ListFenceLocationsQuery(ctx, id, spatial)
	if err != nil {
		return nil, err
	}
	out := make([]gen.LocationProvider, 0, len(locations))
	seen := map[string]bool{}
	for _, location := range locations {
		if seen[location.ProviderId] {
			continue
		}
		seen[location.ProviderId] = true
		if provider, ok := s.providerByID(ctx, location.ProviderId); ok {
			out = append(out, provider)
		}
	}
	return out, nil
}

func (s *ProcessingState) providerLocationsInFence(id string) []gen.Location {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]gen.Location, 0)
	for member, fences := range s.fenceMembership {
		if !strings.HasPrefix(member, "provider:") {
			continue
		}
		membership, ok := fences[id]
		if !ok {
			continue
		}
		if !membership.expiresAt.IsZero() && !membership.expiresAt.After(s.nowUTC()) {
			continue
		}
		out = append(out, membership.location)
	}
	return out
}
