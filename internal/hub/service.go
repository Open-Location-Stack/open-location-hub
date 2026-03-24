package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/formation-res/open-rtls-hub/internal/mqtt"
	"github.com/formation-res/open-rtls-hub/internal/state/valkey"
	"github.com/formation-res/open-rtls-hub/internal/storage/postgres/sqlcgen"
	"github.com/formation-res/open-rtls-hub/internal/transform"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.uber.org/zap"
)

type Publisher interface {
	PublishJSON(ctx context.Context, topic string, payload any, retained bool) error
}

type Cache interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)
	Delete(ctx context.Context, key string) error
}

type Config struct {
	LocationTTL                           time.Duration
	ProximityTTL                          time.Duration
	DedupTTL                              time.Duration
	ProximityResolutionEntryConfidenceMin float64
	ProximityResolutionExitGraceDuration  time.Duration
	ProximityResolutionBoundaryGrace      float64
	ProximityResolutionMinDwellDuration   time.Duration
	ProximityResolutionPositionMode       string
	ProximityResolutionFallbackRadius     float64
	ProximityResolutionStaleStateTTL      time.Duration
}

type Service struct {
	logger         *zap.Logger
	queries        sqlcgen.Querier
	cache          Cache
	publisher      Publisher
	cfg            Config
	now            func() time.Time
	crsTransformer *transform.CRSTransformer
	transformCache *transform.Cache
}

type HTTPError struct {
	Status  int
	Type    string
	Message string
}

func (e *HTTPError) Error() string {
	return e.Message
}

func New(logger *zap.Logger, queries sqlcgen.Querier, cache *valkey.Client, publisher Publisher, cfg Config) *Service {
	return &Service{
		logger:         logger,
		queries:        queries,
		cache:          cache,
		publisher:      publisher,
		cfg:            cfg,
		now:            time.Now,
		crsTransformer: transform.NewCRSTransformer(),
		transformCache: transform.NewCache(),
	}
}

type proximityResolutionPolicy struct {
	EntryConfidenceMin float64
	ExitGraceDuration  time.Duration
	BoundaryGrace      float64
	MinDwellDuration   time.Duration
	PositionMode       string
	FallbackRadius     float64
	StaleStateTTL      time.Duration
}

type proximityResolutionOverrides struct {
	EntryConfidenceMin *float64
	ExitGraceDuration  *time.Duration
	BoundaryGrace      *float64
	MinDwellDuration   *time.Duration
	PositionMode       *string
	FallbackRadius     *float64
	StaleStateTTL      *time.Duration
}

type proximityResolutionState struct {
	ResolvedZoneID   string    `json:"resolved_zone_id"`
	EnteredAt        time.Time `json:"entered_at"`
	LastConfirmedAt  time.Time `json:"last_confirmed_at"`
	LastEmittedAt    time.Time `json:"last_emitted_at"`
	LastCandidateID  string    `json:"last_candidate_id,omitempty"`
	LastCandidateAt  time.Time `json:"last_candidate_at,omitempty"`
	ResolutionMethod string    `json:"resolution_method,omitempty"`
}

type resolvedProximity struct {
	Zone   gen.Zone
	Policy proximityResolutionPolicy
	State  proximityResolutionState
	Sticky bool
}

func (s *Service) ListZones(ctx context.Context) ([]gen.Zone, error) {
	items, err := s.queries.ListZones(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Zone, 0, len(items))
	for _, item := range items {
		zone, err := decodePayload[gen.Zone](item.Payload)
		if err != nil {
			return nil, err
		}
		out = append(out, zone)
	}
	return out, nil
}

func (s *Service) CreateZone(ctx context.Context, body json.RawMessage) (gen.Zone, error) {
	zone, payload, err := normalizeZone(body, uuid.Nil)
	if err != nil {
		return gen.Zone{}, err
	}
	row, err := s.queries.CreateZone(ctx, sqlcgen.CreateZoneParams{
		ID:        uuidParam(zone.Id),
		Type:      zone.Type,
		ForeignID: textParam(zone.ForeignId),
		Payload:   payload,
	})
	if err != nil {
		return gen.Zone{}, translateDBError(err)
	}
	zone, err := decodePayload[gen.Zone](row.Payload)
	if err == nil {
		s.invalidateZoneTransform(zone.Id.String())
	}
	return zone, err
}

func (s *Service) GetZone(ctx context.Context, id openapi_types.UUID) (gen.Zone, error) {
	row, err := s.queries.GetZone(ctx, uuidParam(id))
	if err != nil {
		return gen.Zone{}, translateDBError(err)
	}
	return decodePayload[gen.Zone](row.Payload)
}

func (s *Service) UpdateZone(ctx context.Context, id openapi_types.UUID, body json.RawMessage) (gen.Zone, error) {
	zone, payload, err := normalizeZone(body, uuid.UUID(id))
	if err != nil {
		return gen.Zone{}, err
	}
	row, err := s.queries.UpdateZone(ctx, sqlcgen.UpdateZoneParams{
		ID:        uuidParam(id),
		Type:      zone.Type,
		ForeignID: textParam(zone.ForeignId),
		Payload:   payload,
	})
	if err != nil {
		return gen.Zone{}, translateDBError(err)
	}
	zone, err := decodePayload[gen.Zone](row.Payload)
	if err == nil {
		s.invalidateZoneTransform(zone.Id.String())
	}
	return zone, err
}

func (s *Service) DeleteZone(ctx context.Context, id openapi_types.UUID) error {
	rows, err := s.queries.DeleteZone(ctx, uuidParam(id))
	if err != nil {
		return translateDBError(err)
	}
	if rows == 0 {
		return notFound("zone not found")
	}
	s.invalidateZoneTransform(id.String())
	return nil
}

func (s *Service) ListProviders(ctx context.Context) ([]gen.LocationProvider, error) {
	items, err := s.queries.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gen.LocationProvider, 0, len(items))
	for _, item := range items {
		provider, err := decodePayload[gen.LocationProvider](item.Payload)
		if err != nil {
			return nil, err
		}
		out = append(out, provider)
	}
	return out, nil
}

func (s *Service) CreateProvider(ctx context.Context, body gen.LocationProviderWrite) (gen.LocationProvider, error) {
	provider, payload, err := normalizeProvider(body, "")
	if err != nil {
		return gen.LocationProvider{}, err
	}
	row, err := s.queries.CreateProvider(ctx, sqlcgen.CreateProviderParams{
		ID:      provider.Id,
		Type:    provider.Type,
		Name:    textParam(provider.Name),
		Payload: payload,
	})
	if err != nil {
		return gen.LocationProvider{}, translateDBError(err)
	}
	return decodePayload[gen.LocationProvider](row.Payload)
}

func (s *Service) GetProvider(ctx context.Context, id string) (gen.LocationProvider, error) {
	row, err := s.queries.GetProvider(ctx, id)
	if err != nil {
		return gen.LocationProvider{}, translateDBError(err)
	}
	return decodePayload[gen.LocationProvider](row.Payload)
}

func (s *Service) UpdateProvider(ctx context.Context, id string, body gen.LocationProviderWrite) (gen.LocationProvider, error) {
	provider, payload, err := normalizeProvider(body, id)
	if err != nil {
		return gen.LocationProvider{}, err
	}
	row, err := s.queries.UpdateProvider(ctx, sqlcgen.UpdateProviderParams{
		ID:      id,
		Type:    provider.Type,
		Name:    textParam(provider.Name),
		Payload: payload,
	})
	if err != nil {
		return gen.LocationProvider{}, translateDBError(err)
	}
	return decodePayload[gen.LocationProvider](row.Payload)
}

func (s *Service) DeleteProvider(ctx context.Context, id string) error {
	rows, err := s.queries.DeleteProvider(ctx, id)
	if err != nil {
		return translateDBError(err)
	}
	if rows == 0 {
		return notFound("provider not found")
	}
	return nil
}

func (s *Service) ListTrackables(ctx context.Context) ([]gen.Trackable, error) {
	items, err := s.queries.ListTrackables(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Trackable, 0, len(items))
	for _, item := range items {
		trackable, err := decodePayload[gen.Trackable](item.Payload)
		if err != nil {
			return nil, err
		}
		out = append(out, trackable)
	}
	return out, nil
}

func (s *Service) CreateTrackable(ctx context.Context, body gen.TrackableWrite) (gen.Trackable, error) {
	trackable, payload, err := normalizeTrackable(body, uuid.Nil)
	if err != nil {
		return gen.Trackable{}, err
	}
	row, err := s.queries.CreateTrackable(ctx, sqlcgen.CreateTrackableParams{
		ID:      uuidParam(trackable.Id),
		Type:    string(trackable.Type),
		Name:    textParam(trackable.Name),
		Payload: payload,
	})
	if err != nil {
		return gen.Trackable{}, translateDBError(err)
	}
	return decodePayload[gen.Trackable](row.Payload)
}

func (s *Service) GetTrackable(ctx context.Context, id openapi_types.UUID) (gen.Trackable, error) {
	row, err := s.queries.GetTrackable(ctx, uuidParam(id))
	if err != nil {
		return gen.Trackable{}, translateDBError(err)
	}
	return decodePayload[gen.Trackable](row.Payload)
}

func (s *Service) UpdateTrackable(ctx context.Context, id openapi_types.UUID, body gen.TrackableWrite) (gen.Trackable, error) {
	trackable, payload, err := normalizeTrackable(body, uuid.UUID(id))
	if err != nil {
		return gen.Trackable{}, err
	}
	row, err := s.queries.UpdateTrackable(ctx, sqlcgen.UpdateTrackableParams{
		ID:      uuidParam(id),
		Type:    string(trackable.Type),
		Name:    textParam(trackable.Name),
		Payload: payload,
	})
	if err != nil {
		return gen.Trackable{}, translateDBError(err)
	}
	return decodePayload[gen.Trackable](row.Payload)
}

func (s *Service) DeleteTrackable(ctx context.Context, id openapi_types.UUID) error {
	rows, err := s.queries.DeleteTrackable(ctx, uuidParam(id))
	if err != nil {
		return translateDBError(err)
	}
	if rows == 0 {
		return notFound("trackable not found")
	}
	return nil
}

func (s *Service) ListFences(ctx context.Context) ([]gen.Fence, error) {
	items, err := s.queries.ListFences(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Fence, 0, len(items))
	for _, item := range items {
		fence, err := decodePayload[gen.Fence](item.Payload)
		if err != nil {
			return nil, err
		}
		out = append(out, fence)
	}
	return out, nil
}

func (s *Service) CreateFence(ctx context.Context, body json.RawMessage) (gen.Fence, error) {
	fence, payload, err := normalizeFence(body, uuid.Nil)
	if err != nil {
		return gen.Fence{}, err
	}
	row, err := s.queries.CreateFence(ctx, sqlcgen.CreateFenceParams{
		ID:        uuidParam(fence.Id),
		Name:      textParam(fence.Name),
		ForeignID: textParam(fence.ForeignId),
		Payload:   payload,
	})
	if err != nil {
		return gen.Fence{}, translateDBError(err)
	}
	return decodePayload[gen.Fence](row.Payload)
}

func (s *Service) GetFence(ctx context.Context, id openapi_types.UUID) (gen.Fence, error) {
	row, err := s.queries.GetFence(ctx, uuidParam(id))
	if err != nil {
		return gen.Fence{}, translateDBError(err)
	}
	return decodePayload[gen.Fence](row.Payload)
}

func (s *Service) UpdateFence(ctx context.Context, id openapi_types.UUID, body json.RawMessage) (gen.Fence, error) {
	fence, payload, err := normalizeFence(body, uuid.UUID(id))
	if err != nil {
		return gen.Fence{}, err
	}
	row, err := s.queries.UpdateFence(ctx, sqlcgen.UpdateFenceParams{
		ID:        uuidParam(id),
		Name:      textParam(fence.Name),
		ForeignID: textParam(fence.ForeignId),
		Payload:   payload,
	})
	if err != nil {
		return gen.Fence{}, translateDBError(err)
	}
	return decodePayload[gen.Fence](row.Payload)
}

func (s *Service) DeleteFence(ctx context.Context, id openapi_types.UUID) error {
	rows, err := s.queries.DeleteFence(ctx, uuidParam(id))
	if err != nil {
		return translateDBError(err)
	}
	if rows == 0 {
		return notFound("fence not found")
	}
	return nil
}

func (s *Service) ProcessLocations(ctx context.Context, locations []gen.Location) error {
	for _, location := range locations {
		if err := validateLocation(location); err != nil {
			return err
		}
		if err := s.recordLocation(ctx, location, s.cfg.LocationTTL); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ProcessProximities(ctx context.Context, proximities []gen.Proximity) error {
	for _, proximity := range proximities {
		if strings.TrimSpace(proximity.ProviderId) == "" || strings.TrimSpace(proximity.ProviderType) == "" || strings.TrimSpace(proximity.Source) == "" {
			return badRequest("proximity entries require provider_id, provider_type, and source")
		}
		location, err := s.locationFromProximity(ctx, proximity)
		if err != nil {
			return err
		}
		if err := s.recordLocation(ctx, location, s.cfg.ProximityTTL); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) locationFromProximity(ctx context.Context, proximity gen.Proximity) (gen.Location, error) {
	zones, err := s.ListZones(ctx)
	if err != nil {
		return gen.Location{}, err
	}
	now := s.now()
	policyDefaults := s.defaultProximityResolutionPolicy()
	candidate, err := resolveProximityCandidate(proximity, zones, policyDefaults)
	if err != nil {
		return gen.Location{}, err
	}
	stateKey := proximityResolutionStateKey(proximity.ProviderType, proximity.ProviderId)
	state, err := s.loadProximityState(ctx, stateKey)
	if err != nil {
		return gen.Location{}, err
	}
	currentZone := findZoneByID(zones, state.ResolvedZoneID)
	resolution, clearState, err := resolveProximity(proximity, candidate, currentZone, state, policyDefaults, now)
	if err != nil {
		return gen.Location{}, err
	}
	if clearState {
		if err := s.cache.Delete(ctx, stateKey); err != nil {
			return gen.Location{}, err
		}
	} else if err := s.storeProximityState(ctx, stateKey, resolution.State, resolution.Policy.StaleStateTTL); err != nil {
		return gen.Location{}, err
	}
	return deriveLocationFromProximity(proximity, resolution.Zone, resolution.Sticky), nil
}

func (s *Service) defaultProximityResolutionPolicy() proximityResolutionPolicy {
	positionMode := strings.TrimSpace(s.cfg.ProximityResolutionPositionMode)
	if positionMode == "" {
		positionMode = "zone_position"
	}
	return proximityResolutionPolicy{
		EntryConfidenceMin: s.cfg.ProximityResolutionEntryConfidenceMin,
		ExitGraceDuration:  s.cfg.ProximityResolutionExitGraceDuration,
		BoundaryGrace:      s.cfg.ProximityResolutionBoundaryGrace,
		MinDwellDuration:   s.cfg.ProximityResolutionMinDwellDuration,
		PositionMode:       positionMode,
		FallbackRadius:     s.cfg.ProximityResolutionFallbackRadius,
		StaleStateTTL:      s.cfg.ProximityResolutionStaleStateTTL,
	}
}

func (s *Service) loadProximityState(ctx context.Context, key string) (proximityResolutionState, error) {
	payload, err := s.cache.Get(ctx, key)
	if err != nil || len(payload) == 0 {
		return proximityResolutionState{}, err
	}
	var state proximityResolutionState
	if err := json.Unmarshal(payload, &state); err != nil {
		return proximityResolutionState{}, err
	}
	return state, nil
}

func (s *Service) storeProximityState(ctx context.Context, key string, state proximityResolutionState, ttl time.Duration) error {
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return s.cache.Set(ctx, key, payload, ttl)
}

func resolveProximityCandidate(proximity gen.Proximity, zones []gen.Zone, defaults proximityResolutionPolicy) (resolvedProximity, error) {
	for _, zone := range zones {
		if zone.Id.String() != proximity.Source && (zone.ForeignId == nil || *zone.ForeignId != proximity.Source) {
			continue
		}
		if !isProximityZoneType(zone.Type) {
			return resolvedProximity{}, badRequest("proximity source zone must be an rfid or ibeacon zone")
		}
		if zone.Position == nil {
			return resolvedProximity{}, badRequest("proximity source zone does not expose a position")
		}
		policy, err := proximityPolicyForZone(zone, defaults)
		if err != nil {
			return resolvedProximity{}, err
		}
		return resolvedProximity{Zone: zone, Policy: policy}, nil
	}
	return resolvedProximity{}, badRequest("proximity source did not match a known zone id or foreign_id")
}

func resolveProximity(proximity gen.Proximity, candidate resolvedProximity, currentZone *gen.Zone, state proximityResolutionState, defaults proximityResolutionPolicy, now time.Time) (resolvedProximity, bool, error) {
	if candidate.Policy.EntryConfidenceMin > 0 {
		confidence, ok := proximityConfidence(proximity.Properties)
		if !ok || confidence < candidate.Policy.EntryConfidenceMin {
			return resolvedProximity{}, false, badRequest("proximity confidence below configured minimum")
		}
	}
	if currentZone == nil || strings.TrimSpace(state.ResolvedZoneID) == "" {
		return enterResolvedZone(candidate, proximity, now), false, nil
	}
	current, err := resolvedZoneFromCurrent(*currentZone, defaults)
	if err != nil {
		return resolvedProximity{}, false, err
	}
	if proximityStateExpired(state, current.Policy, now) {
		return enterResolvedZone(candidate, proximity, now), false, nil
	}
	current.State = state
	current.State.LastCandidateID = candidate.Zone.Id.String()
	current.State.LastCandidateAt = now
	current.State.ResolutionMethod = "proximity_zone"
	if current.Zone.Id == candidate.Zone.Id {
		current.State.LastConfirmedAt = now
		current.State.LastEmittedAt = now
		return current, false, nil
	}
	if now.Sub(state.EnteredAt) < current.Policy.MinDwellDuration {
		current.Sticky = true
		current.State.LastEmittedAt = now
		return current, false, nil
	}
	if zonesWithinBoundaryGrace(current.Zone, current.Policy, candidate.Zone, candidate.Policy) && now.Sub(state.LastConfirmedAt) <= current.Policy.ExitGraceDuration {
		current.Sticky = true
		current.State.LastEmittedAt = now
		return current, false, nil
	}
	return enterResolvedZone(candidate, proximity, now), false, nil
}

func enterResolvedZone(candidate resolvedProximity, proximity gen.Proximity, now time.Time) resolvedProximity {
	state := proximityResolutionState{
		ResolvedZoneID:   candidate.Zone.Id.String(),
		EnteredAt:        now,
		LastConfirmedAt:  now,
		LastEmittedAt:    now,
		LastCandidateID:  proximity.Source,
		LastCandidateAt:  now,
		ResolutionMethod: "proximity_zone",
	}
	candidate.State = state
	candidate.Sticky = false
	return candidate
}

func resolvedZoneFromCurrent(zone gen.Zone, defaults proximityResolutionPolicy) (resolvedProximity, error) {
	if zone.Position == nil {
		return resolvedProximity{}, badRequest("resolved proximity zone no longer exposes a position")
	}
	policy, err := proximityPolicyForZone(zone, defaults)
	if err != nil {
		return resolvedProximity{}, err
	}
	return resolvedProximity{Zone: zone, Policy: policy}, nil
}

func deriveLocationFromProximity(proximity gen.Proximity, zone gen.Zone, sticky bool) gen.Location {
	crs := "local"
	properties := mergeProximityResolutionProperties(proximity.Properties, zone.Id.String(), sticky)
	return gen.Location{
		Accuracy:           proximity.Accuracy,
		Crs:                &crs,
		Position:           *zone.Position,
		Properties:         properties,
		ProviderId:         proximity.ProviderId,
		ProviderType:       proximity.ProviderType,
		Source:             zone.Id.String(),
		TimestampGenerated: proximity.TimestampGenerated,
		TimestampSent:      proximity.TimestampSent,
	}
}

func (s *Service) recordLocation(ctx context.Context, location gen.Location, ttl time.Duration) error {
	payload, err := json.Marshal(location)
	if err != nil {
		return err
	}
	ok, err := s.cache.SetNX(ctx, dedupKey(payload), payload, s.cfg.DedupTTL)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := s.cache.Set(ctx, latestLocationKey(location.ProviderId, location.Source), payload, ttl); err != nil {
		return err
	}
	if location.Trackables != nil {
		for _, trackableID := range *location.Trackables {
			if err := s.cache.Set(ctx, latestTrackableLocationKey(trackableID), payload, ttl); err != nil {
				return err
			}
		}
	}
	if err := s.publishLocation(ctx, location); err != nil {
		s.logger.Warn("mqtt location publish failed", zap.Error(err), zap.String("provider_id", location.ProviderId))
	}
	if err := s.publishTrackableMotions(ctx, location); err != nil {
		s.logger.Warn("mqtt trackable motion publish failed", zap.Error(err), zap.String("provider_id", location.ProviderId))
	}
	if err := s.publishFenceEvents(ctx, location); err != nil {
		s.logger.Warn("mqtt fence event publish failed", zap.Error(err), zap.String("provider_id", location.ProviderId))
	}
	return nil
}

func (s *Service) publishLocation(ctx context.Context, location gen.Location) error {
	if s.publisher == nil {
		return nil
	}
	variants, err := s.locationVariants(ctx, location)
	if err != nil {
		return err
	}
	if variants.Local != nil {
		if err := s.publisher.PublishJSON(ctx, mqtt.TopicLocationLocal(location.ProviderId), *variants.Local, false); err != nil {
			return err
		}
	}
	if variants.WGS84 != nil {
		if err := s.publisher.PublishJSON(ctx, mqtt.TopicLocationEPSG4326(location.ProviderId), *variants.WGS84, false); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) publishTrackableMotions(ctx context.Context, location gen.Location) error {
	if s.publisher == nil || location.Trackables == nil {
		return nil
	}
	variants, err := s.locationVariants(ctx, location)
	if err != nil {
		return err
	}
	for _, id := range *location.Trackables {
		baseMotion := gen.TrackableMotion{Id: id}
		if parsed, parseErr := uuid.Parse(id); parseErr == nil {
			trackable, getErr := s.GetTrackable(ctx, openapi_types.UUID(parsed))
			if getErr == nil {
				baseMotion.Name = trackable.Name
				baseMotion.Geometry = trackable.Geometry
				baseMotion.Extrusion = trackable.Extrusion
				baseMotion.Properties = trackable.Properties
			}
		}
		if variants.Local != nil {
			motion := baseMotion
			motion.Location = *variants.Local
			if err := s.publisher.PublishJSON(ctx, mqtt.TopicTrackableMotionLocal(id), motion, false); err != nil {
				return err
			}
		}
		if variants.WGS84 != nil {
			motion := baseMotion
			motion.Location = *variants.WGS84
			if err := s.publisher.PublishJSON(ctx, mqtt.TopicTrackableMotionEPSG4326(id), motion, false); err != nil {
				return err
			}
		}
	}
	return nil
}

type locationPublicationVariants struct {
	Local *gen.Location
	WGS84 *gen.Location
}

func (s *Service) locationVariants(ctx context.Context, location gen.Location) (locationPublicationVariants, error) {
	rawCRS := locationCRS(location)
	switch {
	case rawCRS == "" || rawCRS == "local":
		localCopy, err := cloneLocation(location)
		if err != nil {
			return locationPublicationVariants{}, err
		}
		localCRS := "local"
		localCopy.Crs = &localCRS
		variants := locationPublicationVariants{Local: &localCopy}
		wgs84Location, err := s.locationToWGS84(ctx, location)
		if err != nil {
			s.logger.Warn("location wgs84 transform unavailable", zap.Error(err), zap.String("provider_id", location.ProviderId), zap.String("source", location.Source))
			return variants, nil
		}
		variants.WGS84 = &wgs84Location
		return variants, nil
	default:
		wgs84Location, err := s.locationToWGS84(ctx, location)
		if err != nil {
			return locationPublicationVariants{}, err
		}
		variants := locationPublicationVariants{WGS84: &wgs84Location}
		localLocation, err := s.locationToLocal(ctx, wgs84Location)
		if err != nil {
			s.logger.Warn("location local transform unavailable", zap.Error(err), zap.String("provider_id", location.ProviderId), zap.String("source", location.Source), zap.String("crs", rawCRS))
			return variants, nil
		}
		variants.Local = &localLocation
		return variants, nil
	}
}

func (s *Service) locationToWGS84(ctx context.Context, location gen.Location) (gen.Location, error) {
	crs := locationCRS(location)
	switch {
	case crs == "EPSG:4326":
		out, err := cloneLocation(location)
		if err != nil {
			return gen.Location{}, err
		}
		epsg := "EPSG:4326"
		out.Crs = &epsg
		return out, nil
	case crs == "" || crs == "local":
		zone, err := s.zoneForLocationSource(ctx, location)
		if err != nil {
			return gen.Location{}, err
		}
		transformer, err := s.transformCache.Get(zone)
		if err != nil {
			return gen.Location{}, err
		}
		point, err := transformer.LocalToWGS84(location.Position)
		if err != nil {
			return gen.Location{}, err
		}
		out, err := cloneLocation(location)
		if err != nil {
			return gen.Location{}, err
		}
		epsg := "EPSG:4326"
		out.Crs = &epsg
		out.Position = point
		return out, nil
	default:
		point, err := s.crsTransformer.ToWGS84(crs, location.Position)
		if err != nil {
			return gen.Location{}, err
		}
		out, err := cloneLocation(location)
		if err != nil {
			return gen.Location{}, err
		}
		epsg := "EPSG:4326"
		out.Crs = &epsg
		out.Position = point
		return out, nil
	}
}

func (s *Service) locationToLocal(ctx context.Context, wgs84Location gen.Location) (gen.Location, error) {
	zone, err := s.zoneForLocationSource(ctx, wgs84Location)
	if err != nil {
		return gen.Location{}, err
	}
	transformer, err := s.transformCache.Get(zone)
	if err != nil {
		return gen.Location{}, err
	}
	point, err := transformer.WGS84ToLocal(wgs84Location.Position)
	if err != nil {
		return gen.Location{}, err
	}
	out, err := cloneLocation(wgs84Location)
	if err != nil {
		return gen.Location{}, err
	}
	localCRS := "local"
	out.Crs = &localCRS
	out.Position = point
	return out, nil
}

func (s *Service) zoneForLocationSource(ctx context.Context, location gen.Location) (gen.Zone, error) {
	zones, err := s.ListZones(ctx)
	if err != nil {
		return gen.Zone{}, err
	}
	for _, zone := range zones {
		if zone.Id.String() == location.Source {
			return zone, nil
		}
		if zone.ForeignId != nil && *zone.ForeignId == location.Source {
			return zone, nil
		}
	}
	return gen.Zone{}, badRequest("location source did not match a known zone id or foreign_id")
}

func (s *Service) invalidateZoneTransform(zoneID string) {
	if s.transformCache == nil || strings.TrimSpace(zoneID) == "" {
		return
	}
	s.transformCache.Invalidate(zoneID)
}

func (s *Service) publishFenceEvents(ctx context.Context, location gen.Location) error {
	if s.publisher == nil || location.Trackables == nil {
		return nil
	}
	fences, err := s.ListFences(ctx)
	if err != nil {
		return err
	}
	point, err := point2D(location.Position)
	if err != nil {
		return nil
	}
	now := time.Now().UTC()
	for _, trackableID := range *location.Trackables {
		for _, fence := range fences {
			inside, err := fenceContainsPoint(fence, point)
			if err != nil {
				continue
			}
			stateKey := fenceMembershipKey(fence.Id.String(), trackableID)
			prev, err := s.cache.Get(ctx, stateKey)
			if err != nil {
				return err
			}
			wasInside := string(prev) == "inside"
			switch {
			case inside && !wasInside:
				if err := s.cache.Set(ctx, stateKey, []byte("inside"), s.cfg.LocationTTL); err != nil {
					return err
				}
				event := gen.FenceEvent{
					EventType:   gen.RegionEntry,
					FenceId:     fence.Id,
					Id:          openapi_types.UUID(uuid.New()),
					ProviderId:  &location.ProviderId,
					TrackableId: &trackableID,
					EntryTime:   &now,
					ForeignId:   fence.ForeignId,
				}
				if err := s.publishFenceEvent(ctx, event); err != nil {
					return err
				}
			case !inside && wasInside:
				if err := s.cache.Delete(ctx, stateKey); err != nil {
					return err
				}
				event := gen.FenceEvent{
					EventType:   gen.RegionExit,
					FenceId:     fence.Id,
					Id:          openapi_types.UUID(uuid.New()),
					ProviderId:  &location.ProviderId,
					TrackableId: &trackableID,
					ExitTime:    &now,
					ForeignId:   fence.ForeignId,
				}
				if err := s.publishFenceEvent(ctx, event); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Service) publishFenceEvent(ctx context.Context, event gen.FenceEvent) error {
	if err := s.publisher.PublishJSON(ctx, mqtt.TopicFenceEvent(event.FenceId.String()), event, false); err != nil {
		return err
	}
	if event.TrackableId != nil {
		if err := s.publisher.PublishJSON(ctx, mqtt.TopicFenceEventTrackable(*event.TrackableId), event, false); err != nil {
			return err
		}
	}
	if event.ProviderId != nil {
		if err := s.publisher.PublishJSON(ctx, mqtt.TopicFenceEventProvider(*event.ProviderId), event, false); err != nil {
			return err
		}
	}
	return nil
}

func normalizeZone(body json.RawMessage, forcedID uuid.UUID) (gen.Zone, []byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return gen.Zone{}, nil, badRequest("invalid zone payload")
	}
	t, _ := doc["type"].(string)
	if strings.TrimSpace(t) == "" {
		return gen.Zone{}, nil, badRequest("zone type is required")
	}
	incomplete, _ := doc["incomplete_configuration"].(bool)
	if (t == "rfid" || t == "ibeacon") && doc["position"] == nil {
		return gen.Zone{}, nil, badRequest("zone position is required for rfid and ibeacon zones")
	}
	if t != "rfid" && t != "ibeacon" && !incomplete && doc["ground_control_points"] == nil {
		return gen.Zone{}, nil, badRequest("ground_control_points are required unless incomplete_configuration=true")
	}
	if err := validateZoneProperties(doc["properties"]); err != nil {
		return gen.Zone{}, nil, err
	}
	if forcedID == uuid.Nil {
		if _, ok := doc["id"]; !ok {
			doc["id"] = uuid.New().String()
		}
	} else {
		doc["id"] = forcedID.String()
	}
	payload, err := json.Marshal(doc)
	if err != nil {
		return gen.Zone{}, nil, err
	}
	zone, err := decodePayload[gen.Zone](payload)
	if err != nil {
		return gen.Zone{}, nil, badRequest("invalid zone payload")
	}
	return zone, payload, nil
}

func normalizeFence(body json.RawMessage, forcedID uuid.UUID) (gen.Fence, []byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return gen.Fence{}, nil, badRequest("invalid fence payload")
	}
	if doc["region"] == nil {
		return gen.Fence{}, nil, badRequest("fence region is required")
	}
	if crs, _ := doc["crs"].(string); crs == "local" && doc["zone_id"] == nil {
		return gen.Fence{}, nil, badRequest("zone_id is required for local fences")
	}
	if forcedID == uuid.Nil {
		if _, ok := doc["id"]; !ok {
			doc["id"] = uuid.New().String()
		}
	} else {
		doc["id"] = forcedID.String()
	}
	payload, err := json.Marshal(doc)
	if err != nil {
		return gen.Fence{}, nil, err
	}
	fence, err := decodePayload[gen.Fence](payload)
	if err != nil {
		return gen.Fence{}, nil, badRequest("invalid fence payload")
	}
	return fence, payload, nil
}

func validateZoneProperties(raw any) error {
	if raw == nil {
		return nil
	}
	props, ok := raw.(map[string]any)
	if !ok {
		return badRequest("zone properties must be an object")
	}
	_, err := parseProximityResolutionOverrides(props)
	return err
}

func normalizeProvider(body gen.LocationProviderWrite, forcedID string) (gen.LocationProvider, []byte, error) {
	if forcedID != "" {
		body.Id = forcedID
	}
	if strings.TrimSpace(body.Id) == "" || strings.TrimSpace(body.Type) == "" {
		return gen.LocationProvider{}, nil, badRequest("provider id and type are required")
	}
	provider := gen.LocationProvider{
		ExitDelay:        body.ExitDelay,
		ExitTolerance:    body.ExitTolerance,
		FenceTimeout:     body.FenceTimeout,
		Id:               body.Id,
		Name:             body.Name,
		Properties:       body.Properties,
		Sensors:          body.Sensors,
		ToleranceTimeout: body.ToleranceTimeout,
		Type:             body.Type,
	}
	payload, err := json.Marshal(provider)
	return provider, payload, err
}

func normalizeTrackable(body gen.TrackableWrite, forcedID uuid.UUID) (gen.Trackable, []byte, error) {
	id := forcedID
	if id == uuid.Nil {
		if body.Id != nil {
			id = uuid.UUID(*body.Id)
		} else {
			id = uuid.New()
		}
	}
	if strings.TrimSpace(string(body.Type)) == "" {
		return gen.Trackable{}, nil, badRequest("trackable type is required")
	}
	trackable := gen.Trackable{
		ExitDelay:         body.ExitDelay,
		ExitTolerance:     body.ExitTolerance,
		Extrusion:         body.Extrusion,
		FenceTimeout:      body.FenceTimeout,
		Geometry:          body.Geometry,
		Id:                openapi_types.UUID(id),
		LocatingRules:     body.LocatingRules,
		LocationProviders: body.LocationProviders,
		Name:              body.Name,
		Properties:        body.Properties,
		Radius:            body.Radius,
		ToleranceTimeout:  body.ToleranceTimeout,
		Type:              gen.TrackableType(body.Type),
	}
	payload, err := json.Marshal(trackable)
	return trackable, payload, err
}

func validateLocation(location gen.Location) error {
	if strings.TrimSpace(location.ProviderId) == "" || strings.TrimSpace(location.ProviderType) == "" || strings.TrimSpace(location.Source) == "" {
		return badRequest("location entries require provider_id, provider_type, and source")
	}
	if location.Position.Type != "Point" {
		return badRequest("location position must be a GeoJSON Point")
	}
	_, err := point2D(location.Position)
	if err != nil {
		return badRequest("location position must include 2D or 3D coordinates")
	}
	if err := validateLocationCRS(location.Crs); err != nil {
		return err
	}
	return nil
}

func validateLocationCRS(crs *string) error {
	if crs == nil {
		return nil
	}
	value := strings.TrimSpace(*crs)
	switch value {
	case "", "local":
		return nil
	default:
		if strings.HasPrefix(value, "EPSG:") && len(value) > len("EPSG:") {
			return nil
		}
		return badRequest("location crs must be local or an EPSG code")
	}
}

func locationCRS(location gen.Location) string {
	if location.Crs == nil {
		return ""
	}
	return strings.TrimSpace(*location.Crs)
}

func cloneLocation(location gen.Location) (gen.Location, error) {
	payload, err := json.Marshal(location)
	if err != nil {
		return gen.Location{}, err
	}
	return decodePayload[gen.Location](payload)
}

func translateDBError(err error) error {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return notFound("resource not found")
	case isConflict(err):
		return &HTTPError{Status: 409, Type: "conflict", Message: "resource already exists"}
	default:
		return err
	}
}

func isConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func badRequest(message string) error {
	return &HTTPError{Status: 400, Type: "bad_request", Message: message}
}

func notFound(message string) error {
	return &HTTPError{Status: 404, Type: "not_found", Message: message}
}

func decodePayload[T any](payload []byte) (T, error) {
	var value T
	err := json.Unmarshal(payload, &value)
	return value, err
}

func uuidParam(id openapi_types.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.UUID(id), Valid: true}
}

func textParam(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *value, Valid: true}
}

func latestLocationKey(providerID, source string) string {
	return fmt.Sprintf("hub:location:%s:%s", providerID, source)
}

func latestTrackableLocationKey(trackableID string) string {
	return fmt.Sprintf("hub:trackable:%s:location", trackableID)
}

func proximityResolutionStateKey(providerType, providerID string) string {
	return fmt.Sprintf("hub:proximity:%s:%s", providerType, providerID)
}

func dedupKey(payload []byte) string {
	sum := sha256.Sum256(payload)
	return "hub:dedup:" + hex.EncodeToString(sum[:])
}

func fenceMembershipKey(fenceID, trackableID string) string {
	return fmt.Sprintf("hub:fence:%s:%s", fenceID, trackableID)
}

func point2D(point gen.Point) ([2]float64, error) {
	coords2d, err := point.Coordinates.AsGeoJsonPosition2D()
	if err == nil && len(coords2d) == 2 {
		return [2]float64{float64(coords2d[0]), float64(coords2d[1])}, nil
	}
	coords3d, err := point.Coordinates.AsGeoJsonPosition3D()
	if err == nil && len(coords3d) >= 2 {
		return [2]float64{float64(coords3d[0]), float64(coords3d[1])}, nil
	}
	return [2]float64{}, errors.New("invalid coordinates")
}

func fenceContainsPoint(fence gen.Fence, point [2]float64) (bool, error) {
	if p, err := fence.Region.AsPoint(); err == nil {
		center, err := point2D(p)
		if err != nil {
			return false, err
		}
		radius := 0.0
		if fence.Radius != nil {
			radius = float64(*fence.Radius)
		}
		dx := point[0] - center[0]
		dy := point[1] - center[1]
		return dx*dx+dy*dy <= radius*radius, nil
	}
	polygon, err := fence.Region.AsPolygon()
	if err != nil {
		return false, err
	}
	if len(polygon.Coordinates) == 0 || len(polygon.Coordinates[0]) == 0 {
		return false, errors.New("empty polygon")
	}
	return pointInRing(point, polygon.Coordinates[0]), nil
}

func pointInRing(point [2]float64, ring []gen.GeoJsonPosition) bool {
	inside := false
	j := len(ring) - 1
	for i := 0; i < len(ring); i++ {
		pi, err := geoPoint(ring[i])
		if err != nil {
			j = i
			continue
		}
		pj, err := geoPoint(ring[j])
		if err != nil {
			j = i
			continue
		}
		intersects := (pi[1] > point[1]) != (pj[1] > point[1]) &&
			point[0] < (pj[0]-pi[0])*(point[1]-pi[1])/(pj[1]-pi[1])+pi[0]
		if intersects {
			inside = !inside
		}
		j = i
	}
	return inside
}

func geoPoint(pos gen.GeoJsonPosition) ([2]float64, error) {
	coords2d, err := pos.AsGeoJsonPosition2D()
	if err == nil && len(coords2d) == 2 {
		return [2]float64{float64(coords2d[0]), float64(coords2d[1])}, nil
	}
	coords3d, err := pos.AsGeoJsonPosition3D()
	if err == nil && len(coords3d) >= 2 {
		return [2]float64{float64(coords3d[0]), float64(coords3d[1])}, nil
	}
	return [2]float64{}, errors.New("invalid geojson position")
}

func proximityPolicyForZone(zone gen.Zone, defaults proximityResolutionPolicy) (proximityResolutionPolicy, error) {
	policy := defaults
	if zone.Properties == nil {
		return validateProximityResolutionPolicy(policy)
	}
	overrides, err := parseProximityResolutionOverrides(map[string]any(*zone.Properties))
	if err != nil {
		return proximityResolutionPolicy{}, err
	}
	if overrides.EntryConfidenceMin != nil {
		policy.EntryConfidenceMin = *overrides.EntryConfidenceMin
	}
	if overrides.ExitGraceDuration != nil {
		policy.ExitGraceDuration = *overrides.ExitGraceDuration
	}
	if overrides.BoundaryGrace != nil {
		policy.BoundaryGrace = *overrides.BoundaryGrace
	}
	if overrides.MinDwellDuration != nil {
		policy.MinDwellDuration = *overrides.MinDwellDuration
	}
	if overrides.PositionMode != nil {
		policy.PositionMode = *overrides.PositionMode
	}
	if overrides.FallbackRadius != nil {
		policy.FallbackRadius = *overrides.FallbackRadius
	}
	if overrides.StaleStateTTL != nil {
		policy.StaleStateTTL = *overrides.StaleStateTTL
	}
	return validateProximityResolutionPolicy(policy)
}

func parseProximityResolutionOverrides(props map[string]any) (proximityResolutionOverrides, error) {
	raw, ok := props["proximity_resolution"]
	if !ok || raw == nil {
		return proximityResolutionOverrides{}, nil
	}
	doc, ok := raw.(map[string]any)
	if !ok {
		return proximityResolutionOverrides{}, badRequest("zone properties.proximity_resolution must be an object")
	}
	var out proximityResolutionOverrides
	for key, value := range doc {
		switch key {
		case "entry_confidence_min":
			n, err := parseFloatValue(value, "entry_confidence_min")
			if err != nil {
				return proximityResolutionOverrides{}, err
			}
			out.EntryConfidenceMin = &n
		case "exit_grace_duration":
			d, err := parseDurationValue(value, "exit_grace_duration")
			if err != nil {
				return proximityResolutionOverrides{}, err
			}
			out.ExitGraceDuration = &d
		case "boundary_grace_distance":
			n, err := parseFloatValue(value, "boundary_grace_distance")
			if err != nil {
				return proximityResolutionOverrides{}, err
			}
			out.BoundaryGrace = &n
		case "min_dwell_duration":
			d, err := parseDurationValue(value, "min_dwell_duration")
			if err != nil {
				return proximityResolutionOverrides{}, err
			}
			out.MinDwellDuration = &d
		case "position_mode":
			s, ok := value.(string)
			if !ok {
				return proximityResolutionOverrides{}, badRequest("zone properties.proximity_resolution.position_mode must be a string")
			}
			out.PositionMode = &s
		case "fallback_radius":
			n, err := parseFloatValue(value, "fallback_radius")
			if err != nil {
				return proximityResolutionOverrides{}, err
			}
			out.FallbackRadius = &n
		case "stale_state_ttl":
			d, err := parseDurationValue(value, "stale_state_ttl")
			if err != nil {
				return proximityResolutionOverrides{}, err
			}
			out.StaleStateTTL = &d
		}
	}
	return out, nil
}

func validateProximityResolutionPolicy(policy proximityResolutionPolicy) (proximityResolutionPolicy, error) {
	if policy.EntryConfidenceMin < 0 {
		return proximityResolutionPolicy{}, badRequest("proximity resolution entry_confidence_min must be >= 0")
	}
	if policy.ExitGraceDuration <= 0 {
		return proximityResolutionPolicy{}, badRequest("proximity resolution exit_grace_duration must be > 0")
	}
	if policy.BoundaryGrace < 0 {
		return proximityResolutionPolicy{}, badRequest("proximity resolution boundary_grace_distance must be >= 0")
	}
	if policy.MinDwellDuration < 0 {
		return proximityResolutionPolicy{}, badRequest("proximity resolution min_dwell_duration must be >= 0")
	}
	if strings.TrimSpace(policy.PositionMode) == "" {
		return proximityResolutionPolicy{}, badRequest("proximity resolution position_mode is required")
	}
	if policy.PositionMode != "zone_position" {
		return proximityResolutionPolicy{}, badRequest("proximity resolution position_mode must be zone_position")
	}
	if policy.FallbackRadius < 0 {
		return proximityResolutionPolicy{}, badRequest("proximity resolution fallback_radius must be >= 0")
	}
	if policy.StaleStateTTL <= 0 {
		return proximityResolutionPolicy{}, badRequest("proximity resolution stale_state_ttl must be > 0")
	}
	return policy, nil
}

func parseFloatValue(raw any, key string) (float64, error) {
	switch value := raw.(type) {
	case float64:
		return value, nil
	case float32:
		return float64(value), nil
	case int:
		return float64(value), nil
	case int32:
		return float64(value), nil
	case int64:
		return float64(value), nil
	case json.Number:
		n, err := value.Float64()
		if err != nil {
			return 0, badRequest(fmt.Sprintf("zone properties.proximity_resolution.%s must be numeric", key))
		}
		return n, nil
	default:
		return 0, badRequest(fmt.Sprintf("zone properties.proximity_resolution.%s must be numeric", key))
	}
}

func parseDurationValue(raw any, key string) (time.Duration, error) {
	switch value := raw.(type) {
	case string:
		d, err := time.ParseDuration(value)
		if err != nil {
			return 0, badRequest(fmt.Sprintf("zone properties.proximity_resolution.%s must be a valid duration string", key))
		}
		return d, nil
	case float64:
		return time.Duration(value * float64(time.Second)), nil
	case int:
		return time.Duration(value) * time.Second, nil
	case int64:
		return time.Duration(value) * time.Second, nil
	case json.Number:
		n, err := value.Float64()
		if err != nil {
			return 0, badRequest(fmt.Sprintf("zone properties.proximity_resolution.%s must be a duration string or seconds", key))
		}
		return time.Duration(n * float64(time.Second)), nil
	default:
		return 0, badRequest(fmt.Sprintf("zone properties.proximity_resolution.%s must be a duration string or seconds", key))
	}
}

func isProximityZoneType(zoneType string) bool {
	switch strings.ToLower(strings.TrimSpace(zoneType)) {
	case "rfid", "ibeacon":
		return true
	default:
		return false
	}
}

func findZoneByID(zones []gen.Zone, id string) *gen.Zone {
	for _, zone := range zones {
		if zone.Id.String() == id {
			z := zone
			return &z
		}
	}
	return nil
}

func proximityConfidence(props *gen.ExtensionProperties) (float64, bool) {
	if props == nil {
		return 0, false
	}
	raw, ok := (*props)["confidence"]
	if !ok {
		return 0, false
	}
	n, err := parseFloatValue(raw, "confidence")
	if err != nil {
		return 0, false
	}
	return n, true
}

func proximityStateExpired(state proximityResolutionState, policy proximityResolutionPolicy, now time.Time) bool {
	if state.LastConfirmedAt.IsZero() {
		return true
	}
	if now.Sub(state.LastConfirmedAt) > policy.ExitGraceDuration {
		return true
	}
	return now.Sub(state.LastConfirmedAt) > policy.StaleStateTTL
}

func zonesWithinBoundaryGrace(current gen.Zone, currentPolicy proximityResolutionPolicy, candidate gen.Zone, candidatePolicy proximityResolutionPolicy) bool {
	if current.Position == nil || candidate.Position == nil {
		return false
	}
	currentPoint, err := point2D(*current.Position)
	if err != nil {
		return false
	}
	candidatePoint, err := point2D(*candidate.Position)
	if err != nil {
		return false
	}
	dx := candidatePoint[0] - currentPoint[0]
	dy := candidatePoint[1] - currentPoint[1]
	distance := math.Sqrt(dx*dx + dy*dy)
	margin := math.Max(currentPolicy.BoundaryGrace, candidatePolicy.BoundaryGrace)
	return distance <= zoneRadius(current, currentPolicy)+zoneRadius(candidate, candidatePolicy)+margin
}

func zoneRadius(zone gen.Zone, policy proximityResolutionPolicy) float64 {
	if zone.Radius != nil {
		return float64(*zone.Radius)
	}
	return policy.FallbackRadius
}

func mergeProximityResolutionProperties(props *gen.ExtensionProperties, resolvedZoneID string, sticky bool) *gen.ExtensionProperties {
	out := gen.ExtensionProperties{}
	if props != nil {
		for key, value := range *props {
			out[key] = value
		}
	}
	out["resolution_method"] = "proximity_zone"
	out["resolved_zone_id"] = resolvedZoneID
	out["resolution_policy_version"] = "v1"
	out["sticky"] = sticky
	return &out
}
