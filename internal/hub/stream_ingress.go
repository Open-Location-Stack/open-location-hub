package hub

import (
	"context"
	"net/http"
	"strings"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/ingest"
)

const maxObservationBatch = 1024

// UsesDurableIngress reports whether observation acknowledgements follow Kafka commits.
func (s *Service) UsesDurableIngress() bool { return s.cfg.AppendObservations != nil }

func (s *Service) appendLocations(ctx context.Context, locations []gen.Location) error {
	if len(locations) > maxObservationBatch {
		return badRequest("observation batches may contain at most 1024 items")
	}
	batch := make([]ingest.Observation, 0, len(locations))
	for _, input := range locations {
		location := s.applyLocationDefaults(ctx, input)
		if err := validateLocation(location); err != nil {
			return err
		}
		if locationCRS(location) == "local" {
			if _, err := s.zoneForLocationSource(ctx, location); err != nil {
				return err
			}
		}
		location, err := s.resolveLocationTrackables(ctx, location)
		if err != nil {
			return err
		}
		batch = append(batch, ingest.Observation{
			Kind: ingest.Location, ProviderID: input.ProviderId, Source: input.Source,
			ObservedAt: input.TimestampGenerated, Payload: input, NormalizedLocation: location,
			ResolvedTrackableIDs: stringSliceValue(location.Trackables),
		})
	}
	return s.appendValidated(ctx, batch, "location")
}

func (s *Service) appendProximities(ctx context.Context, proximities []gen.Proximity) error {
	if len(proximities) > maxObservationBatch {
		return badRequest("observation batches may contain at most 1024 items")
	}
	batch := make([]ingest.Observation, 0, len(proximities))
	for _, input := range proximities {
		if strings.TrimSpace(input.ProviderId) == "" || !validTechnology(input.ProviderType) || strings.TrimSpace(input.Source) == "" {
			return badRequest("proximity entries require provider_id, provider_type, and source")
		}
		zone, err := s.zoneForLocationSource(ctx, gen.Location{Source: input.Source})
		if err != nil {
			return err
		}
		candidate, err := resolveProximityCandidate(input, []gen.Zone{zone}, s.defaultProximityResolutionPolicy())
		if err != nil {
			return err
		}
		if minimum := candidate.Policy.EntryConfidenceMin; minimum > 0 {
			if confidence, ok := proximityConfidence(input.Properties); !ok || confidence < minimum {
				return badRequest("proximity confidence below configured minimum")
			}
		}
		resolved := input
		if resolved.TimestampGenerated == nil {
			now := s.now().UTC()
			resolved.TimestampGenerated = &now
		}
		location, err := s.resolveLocationTrackables(ctx, deriveLocationFromProximity(resolved, candidate.Zone, false))
		if err != nil {
			return err
		}
		batch = append(batch, ingest.Observation{
			Kind: ingest.Proximity, ProviderID: input.ProviderId, Source: input.Source,
			ObservedAt: input.TimestampGenerated, Payload: input, ResolvedProximityLocation: location,
			ResolvedTrackableIDs: stringSliceValue(location.Trackables),
		})
	}
	return s.appendValidated(ctx, batch, "proximity")
}

func (s *Service) appendValidated(ctx context.Context, batch []ingest.Observation, operation string) error {
	if err := s.cfg.AppendObservations(ctx, batch); err != nil {
		for range batch {
			s.telemetry().RecordIngestRecord(ctx, operation, "unconfirmed")
		}
		return &HTTPError{Status: http.StatusServiceUnavailable, Type: "ingest_unavailable",
			Message: "Durable acceptance is unconfirmed. Retry with the same observation payload."}
	}
	for range batch {
		s.telemetry().RecordIngestRecord(ctx, operation, "accepted")
	}
	return nil
}
