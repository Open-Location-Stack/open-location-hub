package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/formation-res/open-rtls-hub/internal/ids"
	"github.com/formation-res/open-rtls-hub/internal/observability"
	"github.com/formation-res/open-rtls-hub/internal/storage/postgres/sqlcgen"
	"github.com/formation-res/open-rtls-hub/internal/transform"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// Config controls ingest deduplication, TTLs, and proximity resolution
// behavior.
type Config struct {
	HubID                                 string
	LocationTTL                           time.Duration
	ProximityTTL                          time.Duration
	DedupTTL                              time.Duration
	NativeLocationBuffer                  int
	DerivedLocationBuffer                 int
	ProximityResolutionEntryConfidenceMin float64
	ProximityResolutionExitGraceDuration  time.Duration
	ProximityResolutionBoundaryGrace      float64
	ProximityResolutionMinDwellDuration   time.Duration
	ProximityResolutionPositionMode       string
	ProximityResolutionFallbackRadius     float64
	ProximityResolutionStaleStateTTL      time.Duration
	MetadataReconcileInterval             time.Duration
	CollisionsEnabled                     bool
	CollisionStateTTL                     time.Duration
	CollisionCollidingDebounce            time.Duration
	CollisionDefaultRadiusMeters          float64
}

// Service implements the hub's CRUD and ingest behavior over storage, cache,
// and publish dependencies.
type Service struct {
	logger           *zap.Logger
	queries          sqlcgen.Querier
	bus              *EventBus
	cfg              Config
	now              func() time.Time
	crsTransformer   *transform.CRSTransformer
	transformCache   *transform.Cache
	metadata         *MetadataCache
	state            *ProcessingState
	stats            *RuntimeStats
	telemetryRuntime *observability.Runtime
	nativeQueue      derivedLocationSubmitter
	derivedQueue     derivedLocationSubmitter
	decisionStage    decisionLocationStage
}

// HTTPError represents an API error that should be rendered with a specific
// status code and error body.
type HTTPError struct {
	Status  int
	Type    string
	Message string
}

func (e *HTTPError) Error() string {
	return e.Message
}

// Start launches background maintenance loops for transient state sweeping and
// metadata reconciliation.
func (s *Service) Start(ctx context.Context) {
	if s == nil {
		return
	}
	_ = s.telemetry().RegisterRuntimeMetricsSource(s.stats)
	s.processingState().StartSweeper(ctx, time.Second)
	if s.nativeQueue == nil {
		s.nativeQueue = startDerivedLocationProcessor(ctx, s, s.cfg.NativeLocationBuffer, "native location queue", s.stats.IncNativeQueueDrops, s.processNativeLocation)
	}
	if s.derivedQueue == nil {
		s.derivedQueue = startDerivedLocationProcessor(ctx, s, s.cfg.DerivedLocationBuffer, "decision location queue", s.stats.IncDecisionQueueDrops, s.processDecisionLocation)
	}
	if s.metadata == nil || s.queries == nil {
		return
	}
	ticker := time.NewTicker(s.cfg.MetadataReconcileInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				reconcileCtx, span := s.telemetry().StartSpan(observability.WithIngestTransport(ctx, "internal"), "hub.metadata.reconcile")
				start := time.Now()
				err := s.reconcileMetadata(reconcileCtx)
				s.telemetry().RecordProcessingDuration(reconcileCtx, "metadata_reconcile", "metadata", time.Since(start))
				if err != nil {
					span.RecordError(err)
					s.telemetry().RecordDependencyEvent(reconcileCtx, "metadata", "reconcile", "failure")
					if s.logger != nil {
						s.logger.Warn("metadata reconcile failed", zap.Any("context", reconcileCtx), zap.Error(err))
					}
				} else {
					s.telemetry().RecordDependencyEvent(reconcileCtx, "metadata", "reconcile", "success")
				}
				span.End()
			}
		}
	}()
}

// New constructs a Service with the configured dependencies and eagerly loads
// the in-memory metadata snapshot required by the hot path.
func New(logger *zap.Logger, queries sqlcgen.Querier, bus *EventBus, cfg Config) (*Service, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	if cfg.MetadataReconcileInterval <= 0 {
		cfg.MetadataReconcileInterval = 30 * time.Second
	}
	if cfg.NativeLocationBuffer <= 0 {
		cfg.NativeLocationBuffer = 2048
	}
	if cfg.DerivedLocationBuffer <= 0 {
		cfg.DerivedLocationBuffer = 1024
	}
	metadata, err := NewMetadataCache(context.Background(), queries)
	if err != nil {
		return nil, err
	}
	nowFn := time.Now
	return &Service{
		logger:           logger,
		queries:          queries,
		bus:              bus,
		cfg:              cfg,
		now:              nowFn,
		crsTransformer:   transform.NewCRSTransformer(),
		transformCache:   transform.NewCache(),
		metadata:         metadata,
		state:            NewProcessingState(nowFn),
		stats:            runtimeStatsFromBus(bus),
		telemetryRuntime: observability.Global(),
		decisionStage:    passthroughDecisionStage{},
	}, nil
}

func runtimeStatsFromBus(bus *EventBus) *RuntimeStats {
	if bus != nil && bus.Stats() != nil {
		return bus.Stats()
	}
	return newRuntimeStats()
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

func (s *Service) processingState() *ProcessingState {
	if s.state == nil {
		s.state = NewProcessingState(s.now)
	}
	return s.state
}

func (s *Service) telemetry() *observability.Runtime {
	if s == nil || s.telemetryRuntime == nil {
		return observability.Global()
	}
	return s.telemetryRuntime
}

// RuntimeStatsSnapshot returns the current overload and queue diagnostics.
func (s *Service) RuntimeStatsSnapshot() RuntimeStatsSnapshot {
	if s == nil || s.stats == nil {
		return RuntimeStatsSnapshot{}
	}
	return s.stats.Snapshot()
}

func (s *Service) metadataCache() *MetadataCache {
	return s.metadata
}

func (s *Service) reconcileMetadata(ctx context.Context) error {
	if s.metadata == nil {
		return nil
	}
	changes, err := s.metadata.Reconcile(ctx, s.now().UTC())
	if err != nil {
		return err
	}
	for _, change := range changes {
		if change.Type == metadataTypeZone {
			s.invalidateZoneTransform(change.ID)
		}
		s.emitMetadataChange(change)
	}
	return nil
}

func (s *Service) emitMetadataChange(change MetadataChange) {
	if s.bus == nil {
		return
	}
	event, err := newEvent(EventMetadataChange, ScopeMetadata, change.Timestamp, "", "", "", s.cfg.HubID, change)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("metadata change emit failed", zap.Error(err), zap.String("id", change.ID), zap.String("type", change.Type))
		}
		return
	}
	s.bus.Emit(event)
}

// ListZones returns all zones known to the hub.
func (s *Service) ListZones(ctx context.Context) ([]gen.Zone, error) {
	if cache := s.metadataCache(); cache != nil {
		return cache.ListZones(), nil
	}
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

// CreateZone validates and stores a new zone document.
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
	zone, err = decodePayload[gen.Zone](row.Payload)
	if err == nil {
		if cache := s.metadataCache(); cache != nil {
			cache.UpsertZone(zone, payloadSignature(row.Payload))
		}
		s.invalidateZoneTransform(zone.Id.String())
		s.emitMetadataChange(MetadataChange{
			ID:        zone.Id.String(),
			Type:      metadataTypeZone,
			Operation: metadataOperationCreate,
			Timestamp: s.now().UTC(),
		})
	}
	return zone, err
}

// GetZone returns a single zone by identifier.
func (s *Service) GetZone(ctx context.Context, id openapi_types.UUID) (gen.Zone, error) {
	if cache := s.metadataCache(); cache != nil {
		if zone, ok := cache.ZoneByID(id.String()); ok {
			return zone, nil
		}
		return gen.Zone{}, notFound("zone not found")
	}
	row, err := s.queries.GetZone(ctx, uuidParam(id))
	if err != nil {
		return gen.Zone{}, translateDBError(err)
	}
	return decodePayload[gen.Zone](row.Payload)
}

// UpdateZone validates and replaces an existing zone document.
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
	zone, err = decodePayload[gen.Zone](row.Payload)
	if err == nil {
		if cache := s.metadataCache(); cache != nil {
			cache.UpsertZone(zone, payloadSignature(row.Payload))
		}
		s.invalidateZoneTransform(zone.Id.String())
		s.emitMetadataChange(MetadataChange{
			ID:        zone.Id.String(),
			Type:      metadataTypeZone,
			Operation: metadataOperationUpdate,
			Timestamp: s.now().UTC(),
		})
	}
	return zone, err
}

// DeleteZone removes a zone by identifier.
func (s *Service) DeleteZone(ctx context.Context, id openapi_types.UUID) error {
	rows, err := s.queries.DeleteZone(ctx, uuidParam(id))
	if err != nil {
		return translateDBError(err)
	}
	if rows == 0 {
		return notFound("zone not found")
	}
	if cache := s.metadataCache(); cache != nil {
		cache.DeleteZone(id)
	}
	s.invalidateZoneTransform(id.String())
	s.emitMetadataChange(MetadataChange{
		ID:        id.String(),
		Type:      metadataTypeZone,
		Operation: metadataOperationDelete,
		Timestamp: s.now().UTC(),
	})
	return nil
}

// ListProviders returns all location providers known to the hub.
func (s *Service) ListProviders(ctx context.Context) ([]gen.LocationProvider, error) {
	if cache := s.metadataCache(); cache != nil {
		return cache.ListProviders(), nil
	}
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

// CreateProvider validates and stores a new location provider.
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
	item, err := decodePayload[gen.LocationProvider](row.Payload)
	if err == nil {
		if cache := s.metadataCache(); cache != nil {
			cache.UpsertProvider(item, payloadSignature(row.Payload))
		}
		s.emitMetadataChange(MetadataChange{
			ID:        item.Id,
			Type:      metadataTypeLocationProvider,
			Operation: metadataOperationCreate,
			Timestamp: s.now().UTC(),
		})
	}
	return item, err
}

// GetProvider returns a location provider by identifier.
func (s *Service) GetProvider(ctx context.Context, id string) (gen.LocationProvider, error) {
	if cache := s.metadataCache(); cache != nil {
		if item, ok := cache.ProviderByID(id); ok {
			return item, nil
		}
		return gen.LocationProvider{}, notFound("provider not found")
	}
	row, err := s.queries.GetProvider(ctx, id)
	if err != nil {
		return gen.LocationProvider{}, translateDBError(err)
	}
	return decodePayload[gen.LocationProvider](row.Payload)
}

// UpdateProvider validates and replaces an existing location provider.
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
	item, err := decodePayload[gen.LocationProvider](row.Payload)
	if err == nil {
		if cache := s.metadataCache(); cache != nil {
			cache.UpsertProvider(item, payloadSignature(row.Payload))
		}
		s.emitMetadataChange(MetadataChange{
			ID:        item.Id,
			Type:      metadataTypeLocationProvider,
			Operation: metadataOperationUpdate,
			Timestamp: s.now().UTC(),
		})
	}
	return item, err
}

// DeleteProvider removes a location provider by identifier.
func (s *Service) DeleteProvider(ctx context.Context, id string) error {
	rows, err := s.queries.DeleteProvider(ctx, id)
	if err != nil {
		return translateDBError(err)
	}
	if rows == 0 {
		return notFound("provider not found")
	}
	if cache := s.metadataCache(); cache != nil {
		cache.DeleteProvider(id)
	}
	s.emitMetadataChange(MetadataChange{
		ID:        id,
		Type:      metadataTypeLocationProvider,
		Operation: metadataOperationDelete,
		Timestamp: s.now().UTC(),
	})
	return nil
}

// ListTrackables returns all trackables known to the hub.
func (s *Service) ListTrackables(ctx context.Context) ([]gen.Trackable, error) {
	if cache := s.metadataCache(); cache != nil {
		return cache.ListTrackables(), nil
	}
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

// CreateTrackable validates and stores a new trackable.
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
	item, err := decodePayload[gen.Trackable](row.Payload)
	if err == nil {
		if cache := s.metadataCache(); cache != nil {
			cache.UpsertTrackable(item, payloadSignature(row.Payload))
		}
		s.emitMetadataChange(MetadataChange{
			ID:        item.Id.String(),
			Type:      metadataTypeTrackable,
			Operation: metadataOperationCreate,
			Timestamp: s.now().UTC(),
		})
	}
	return item, err
}

// GetTrackable returns a trackable by identifier.
func (s *Service) GetTrackable(ctx context.Context, id openapi_types.UUID) (gen.Trackable, error) {
	if cache := s.metadataCache(); cache != nil {
		if item, ok := cache.TrackableByID(id.String()); ok {
			return item, nil
		}
		return gen.Trackable{}, notFound("trackable not found")
	}
	row, err := s.queries.GetTrackable(ctx, uuidParam(id))
	if err != nil {
		return gen.Trackable{}, translateDBError(err)
	}
	return decodePayload[gen.Trackable](row.Payload)
}

// UpdateTrackable validates and replaces an existing trackable.
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
	item, err := decodePayload[gen.Trackable](row.Payload)
	if err == nil {
		if cache := s.metadataCache(); cache != nil {
			cache.UpsertTrackable(item, payloadSignature(row.Payload))
		}
		s.emitMetadataChange(MetadataChange{
			ID:        item.Id.String(),
			Type:      metadataTypeTrackable,
			Operation: metadataOperationUpdate,
			Timestamp: s.now().UTC(),
		})
	}
	return item, err
}

// DeleteTrackable removes a trackable by identifier.
func (s *Service) DeleteTrackable(ctx context.Context, id openapi_types.UUID) error {
	rows, err := s.queries.DeleteTrackable(ctx, uuidParam(id))
	if err != nil {
		return translateDBError(err)
	}
	if rows == 0 {
		return notFound("trackable not found")
	}
	if cache := s.metadataCache(); cache != nil {
		cache.DeleteTrackable(id)
	}
	s.emitMetadataChange(MetadataChange{
		ID:        id.String(),
		Type:      metadataTypeTrackable,
		Operation: metadataOperationDelete,
		Timestamp: s.now().UTC(),
	})
	return nil
}

// ListFences returns all fences known to the hub.
func (s *Service) ListFences(ctx context.Context) ([]gen.Fence, error) {
	if cache := s.metadataCache(); cache != nil {
		return cache.ListFences(), nil
	}
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

// CreateFence validates and stores a new fence document.
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
	item, err := decodePayload[gen.Fence](row.Payload)
	if err == nil {
		if cache := s.metadataCache(); cache != nil {
			cache.UpsertFence(item, payloadSignature(row.Payload))
		}
		s.emitMetadataChange(MetadataChange{
			ID:        item.Id.String(),
			Type:      metadataTypeFence,
			Operation: metadataOperationCreate,
			Timestamp: s.now().UTC(),
		})
	}
	return item, err
}

// GetFence returns a fence by identifier.
func (s *Service) GetFence(ctx context.Context, id openapi_types.UUID) (gen.Fence, error) {
	if cache := s.metadataCache(); cache != nil {
		if item, ok := cache.FenceByID(id.String()); ok {
			return item, nil
		}
		return gen.Fence{}, notFound("fence not found")
	}
	row, err := s.queries.GetFence(ctx, uuidParam(id))
	if err != nil {
		return gen.Fence{}, translateDBError(err)
	}
	return decodePayload[gen.Fence](row.Payload)
}

// UpdateFence validates and replaces an existing fence document.
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
	item, err := decodePayload[gen.Fence](row.Payload)
	if err == nil {
		if cache := s.metadataCache(); cache != nil {
			cache.UpsertFence(item, payloadSignature(row.Payload))
		}
		s.emitMetadataChange(MetadataChange{
			ID:        item.Id.String(),
			Type:      metadataTypeFence,
			Operation: metadataOperationUpdate,
			Timestamp: s.now().UTC(),
		})
	}
	return item, err
}

// DeleteFence removes a fence by identifier.
func (s *Service) DeleteFence(ctx context.Context, id openapi_types.UUID) error {
	rows, err := s.queries.DeleteFence(ctx, uuidParam(id))
	if err != nil {
		return translateDBError(err)
	}
	if rows == 0 {
		return notFound("fence not found")
	}
	if cache := s.metadataCache(); cache != nil {
		cache.DeleteFence(id)
	}
	s.emitMetadataChange(MetadataChange{
		ID:        id.String(),
		Type:      metadataTypeFence,
		Operation: metadataOperationDelete,
		Timestamp: s.now().UTC(),
	})
	return nil
}

// ProcessLocations validates, stores, and republishes provider location
// updates.
func (s *Service) ProcessLocations(ctx context.Context, locations []gen.Location) error {
	for _, location := range locations {
		itemCtx := observability.WithIngestTransport(ctx, observability.IngestTransportFromContext(ctx))
		itemCtx, span := s.telemetry().StartSpan(itemCtx, "hub.ingest.location",
			attribute.String("provider_id", location.ProviderId),
			attribute.String("provider_type", location.ProviderType),
			attribute.String("source", location.Source),
			attribute.StringSlice("trackable_ids", stringSliceValue(location.Trackables)),
		)
		start := time.Now()
		if err := validateLocation(location); err != nil {
			span.RecordError(err)
			s.telemetry().RecordIngestRecord(itemCtx, "location", "invalid")
			span.End()
			return err
		}
		if err := s.recordLocation(itemCtx, location, s.cfg.LocationTTL); err != nil {
			span.RecordError(err)
			s.telemetry().RecordIngestRecord(itemCtx, "location", "failed")
			span.End()
			return err
		}
		s.telemetry().RecordProcessingDuration(itemCtx, "location_ingest", "location", time.Since(start))
		span.End()
	}
	return nil
}

// ProcessProximities validates, resolves, and republishes provider proximity
// updates.
func (s *Service) ProcessProximities(ctx context.Context, proximities []gen.Proximity) error {
	for _, proximity := range proximities {
		itemCtx := observability.WithIngestTransport(ctx, observability.IngestTransportFromContext(ctx))
		itemCtx, span := s.telemetry().StartSpan(itemCtx, "hub.ingest.proximity",
			attribute.String("provider_id", proximity.ProviderId),
			attribute.String("provider_type", proximity.ProviderType),
			attribute.String("source", proximity.Source),
		)
		start := time.Now()
		if strings.TrimSpace(proximity.ProviderId) == "" || strings.TrimSpace(proximity.ProviderType) == "" || strings.TrimSpace(proximity.Source) == "" {
			err := badRequest("proximity entries require provider_id, provider_type, and source")
			span.RecordError(err)
			s.telemetry().RecordIngestRecord(itemCtx, "proximity", "invalid")
			span.End()
			return err
		}
		if err := s.publishProximity(proximity); err != nil {
			span.RecordError(err)
			s.telemetry().RecordIngestRecord(itemCtx, "proximity", "failed")
			span.End()
			return err
		}
		location, err := s.locationFromProximity(itemCtx, proximity)
		if err != nil {
			span.RecordError(err)
			s.telemetry().RecordIngestRecord(itemCtx, "proximity", "failed")
			span.End()
			return err
		}
		if err := s.recordLocation(itemCtx, location, s.cfg.ProximityTTL); err != nil {
			span.RecordError(err)
			s.telemetry().RecordIngestRecord(itemCtx, "proximity", "failed")
			span.End()
			return err
		}
		s.telemetry().RecordProcessingDuration(itemCtx, "proximity_ingest", "proximity", time.Since(start))
		span.End()
	}
	return nil
}

func (s *Service) publishProximity(proximity gen.Proximity) error {
	if s.bus == nil {
		return nil
	}
	event, err := newEvent(EventProximity, ScopeRaw, proximityTime(proximity), proximity.ProviderId, "", "", s.cfg.HubID, ProximityEnvelope{Proximity: proximity})
	if err != nil {
		return err
	}
	s.bus.Emit(event)
	return nil
}

func (s *Service) locationFromProximity(ctx context.Context, proximity gen.Proximity) (gen.Location, error) {
	start := time.Now()
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
		s.processingState().DeleteProximityState(stateKey)
	} else if err := s.storeProximityState(ctx, stateKey, resolution.State, resolution.Policy.StaleStateTTL); err != nil {
		return gen.Location{}, err
	}
	s.telemetry().RecordProcessingDuration(ctx, "proximity_resolution", "proximity", time.Since(start))
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
	_ = ctx
	state, ok := s.processingState().GetProximityState(key)
	if !ok {
		return proximityResolutionState{}, nil
	}
	return state, nil
}

func (s *Service) storeProximityState(ctx context.Context, key string, state proximityResolutionState, ttl time.Duration) error {
	_ = ctx
	s.processingState().SetProximityState(key, state, ttl)
	return nil
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
	start := time.Now()
	payload, err := json.Marshal(location)
	if err != nil {
		return err
	}
	if !s.processingState().Deduplicate(dedupKey(payload), s.cfg.DedupTTL) {
		s.telemetry().RecordIngestRecord(ctx, "location", "deduplicated")
		return nil
	}
	s.telemetry().RecordIngestRecord(ctx, "location", "accepted")
	s.processingState().SetLatestLocation(latestLocationKey(location.ProviderId, location.Source), location, ttl)
	if location.Trackables != nil {
		for _, trackableID := range *location.Trackables {
			s.processingState().SetTrackableLocation(latestTrackableLocationKey(trackableID), location, ttl)
		}
	}
	if s.nativeQueue != nil {
		s.nativeQueue.Submit(derivedLocationWork{Context: ctx, Location: location, EnqueuedAt: time.Now()})
		s.telemetry().RecordProcessingDuration(ctx, "location_record", "location", time.Since(start))
		return nil
	}
	if err := s.publishLocation(ctx, location); err != nil {
		s.logger.Warn("location event emit failed", zap.Any("context", ctx), zap.Error(err), zap.String("provider_id", location.ProviderId))
	}
	motions, err := s.publishTrackableMotions(ctx, location)
	if err != nil {
		s.logger.Warn("trackable motion event emit failed", zap.Any("context", ctx), zap.Error(err), zap.String("provider_id", location.ProviderId))
	}
	if err := s.publishFenceEvents(ctx, location); err != nil {
		s.logger.Warn("fence event emit failed", zap.Any("context", ctx), zap.Error(err), zap.String("provider_id", location.ProviderId))
	}
	if s.cfg.CollisionsEnabled {
		if err := s.publishCollisionEvents(ctx, motions); err != nil {
			s.logger.Warn("collision event emit failed", zap.Any("context", ctx), zap.Error(err), zap.String("provider_id", location.ProviderId))
		}
	}
	s.telemetry().RecordProcessingDuration(ctx, "location_record", "location", time.Since(start))
	return nil
}

func (s *Service) processNativeLocation(ctx context.Context, location gen.Location) error {
	stageCtx, span := s.telemetry().StartSpan(ctx, "hub.process.native_location",
		attribute.String("provider_id", location.ProviderId),
		attribute.String("source", location.Source),
	)
	start := time.Now()
	if err := s.publishNativeLocation(stageCtx, location); err != nil {
		span.RecordError(err)
		span.End()
		return err
	}
	if _, err := s.publishNativeTrackableMotions(stageCtx, location); err != nil {
		span.RecordError(err)
		span.End()
		return err
	}
	if s.derivedQueue != nil {
		s.derivedQueue.Submit(derivedLocationWork{Context: stageCtx, Location: location, EnqueuedAt: time.Now()})
	}
	s.telemetry().RecordProcessingDuration(stageCtx, "native_publication", "location", time.Since(start))
	span.End()
	return nil
}

func (s *Service) processDecisionLocation(ctx context.Context, location gen.Location) error {
	stageCtx, span := s.telemetry().StartSpan(ctx, "hub.process.decision_location",
		attribute.String("provider_id", location.ProviderId),
		attribute.String("source", location.Source),
	)
	defer span.End()
	return s.processDecisionLocationStage(stageCtx, span, location)
}

func (s *Service) processDecisionLocationBatch(ctx context.Context, batch []derivedLocationWork) error {
	var batchErr error
	for _, work := range batch {
		stageCtx, span := s.telemetry().StartSpan(work.Context, "hub.process.decision_location",
			attribute.String("provider_id", work.Location.ProviderId),
			attribute.String("source", work.Location.Source),
		)
		if err := s.processDecisionLocationStage(stageCtx, span, work.Location); err != nil && batchErr == nil {
			batchErr = err
		}
		span.End()
	}
	return batchErr
}

func (s *Service) processDecisionLocationStage(stageCtx context.Context, span oteltrace.Span, location gen.Location) error {
	start := time.Now()
	decisionLocation, ok, err := s.decisionStage.Process(stageCtx, location)
	if err != nil || !ok {
		if err != nil {
			span.RecordError(err)
		}
		s.telemetry().RecordProcessingDuration(stageCtx, "decision_stage", "location", time.Since(start))
		return err
	}
	err = s.processDerivedLocation(stageCtx, decisionLocation)
	if err != nil {
		span.RecordError(err)
	}
	s.telemetry().RecordProcessingDuration(stageCtx, "decision_stage", "location", time.Since(start))
	return err
}

func (s *Service) processDerivedLocation(ctx context.Context, location gen.Location) error {
	if s.bus == nil {
		return nil
	}
	view := newDerivedLocationView(s, location)
	if err := s.publishFenceEvents(ctx, location); err != nil {
		return err
	}
	switch view.NativeScope() {
	case ScopeLocal:
		wgs84Location, ok, err := view.WGS84(ctx)
		if err == nil && ok {
			if err := s.publishLocationScope(ctx, *wgs84Location, ScopeEPSG4326); err != nil {
				return err
			}
			wgs84Motions, err := s.publishTrackableMotionsForLocation(ctx, *wgs84Location, ScopeEPSG4326)
			if err != nil {
				return err
			}
			if s.cfg.CollisionsEnabled {
				if err := s.publishCollisionEvents(ctx, wgs84Motions); err != nil {
					return err
				}
			}
		}
	case ScopeEPSG4326:
		localLocation, ok, err := view.Local(ctx)
		if err == nil && ok {
			if err := s.publishLocationScope(ctx, *localLocation, ScopeLocal); err != nil {
				return err
			}
			if _, err := s.publishTrackableMotionsForLocation(ctx, *localLocation, ScopeLocal); err != nil {
				return err
			}
		}
		if s.cfg.CollisionsEnabled {
			wgs84Motions, err := s.buildTrackableMotionsForLocation(ctx, location)
			if err != nil {
				return err
			}
			if err := s.publishCollisionEvents(ctx, wgs84Motions); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) publishNativeLocation(ctx context.Context, location gen.Location) error {
	return s.publishLocationScope(ctx, location, nativeLocationScope(location))
}

func (s *Service) publishNativeTrackableMotions(ctx context.Context, location gen.Location) ([]gen.TrackableMotion, error) {
	return s.publishTrackableMotionsForLocation(ctx, location, nativeLocationScope(location))
}

func (s *Service) publishLocation(ctx context.Context, location gen.Location) error {
	if s.bus == nil {
		return nil
	}
	variants, err := s.locationVariants(ctx, location)
	if err != nil {
		return err
	}
	if variants.Local != nil {
		feature, err := locationGeoJSONFeatureCollection(*variants.Local)
		if err != nil {
			return err
		}
		event, err := newEvent(EventLocation, ScopeLocal, locationTime(*variants.Local), variants.Local.ProviderId, "", "", s.cfg.HubID, LocationEnvelope{Location: *variants.Local, GeoJSON: feature})
		if err != nil {
			return err
		}
		s.bus.Emit(event)
	}
	if variants.WGS84 != nil {
		feature, err := locationGeoJSONFeatureCollection(*variants.WGS84)
		if err != nil {
			return err
		}
		event, err := newEvent(EventLocation, ScopeEPSG4326, locationTime(*variants.WGS84), variants.WGS84.ProviderId, "", "", s.cfg.HubID, LocationEnvelope{Location: *variants.WGS84, GeoJSON: feature})
		if err != nil {
			return err
		}
		s.bus.Emit(event)
	}
	return nil
}

func (s *Service) publishLocationScope(_ context.Context, location gen.Location, scope EventScope) error {
	if s.bus == nil {
		return nil
	}
	feature, err := locationGeoJSONFeatureCollection(location)
	if err != nil {
		return err
	}
	event, err := newEvent(EventLocation, scope, locationTime(location), location.ProviderId, "", "", s.cfg.HubID, LocationEnvelope{Location: location, GeoJSON: feature})
	if err != nil {
		return err
	}
	s.bus.Emit(event)
	return nil
}

func (s *Service) publishTrackableMotions(ctx context.Context, location gen.Location) ([]gen.TrackableMotion, error) {
	if s.bus == nil || location.Trackables == nil {
		return nil, nil
	}
	variants, err := s.locationVariants(ctx, location)
	if err != nil {
		return nil, err
	}
	wgsMotions := make([]gen.TrackableMotion, 0, len(*location.Trackables))
	for _, id := range *location.Trackables {
		baseMotion := gen.TrackableMotion{Id: id}
		if cache := s.metadataCache(); cache != nil {
			if trackable, ok := cache.TrackableByID(id); ok {
				baseMotion.Name = trackable.Name
				baseMotion.Geometry = trackable.Geometry
				baseMotion.Extrusion = trackable.Extrusion
				baseMotion.Properties = trackable.Properties
			}
		} else if parsed, parseErr := uuid.Parse(id); parseErr == nil {
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
			event, err := newEvent(EventTrackableMotion, ScopeLocal, locationTime(motion.Location), motion.Location.ProviderId, id, "", s.cfg.HubID, TrackableMotionEnvelope{Motion: motion})
			if err != nil {
				return nil, err
			}
			s.bus.Emit(event)
		}
		if variants.WGS84 != nil {
			motion := baseMotion
			motion.Location = *variants.WGS84
			event, err := newEvent(EventTrackableMotion, ScopeEPSG4326, locationTime(motion.Location), motion.Location.ProviderId, id, "", s.cfg.HubID, TrackableMotionEnvelope{Motion: motion})
			if err != nil {
				return nil, err
			}
			s.bus.Emit(event)
			wgsMotions = append(wgsMotions, motion)
		}
	}
	return wgsMotions, nil
}

func (s *Service) publishTrackableMotionsForLocation(ctx context.Context, location gen.Location, scope EventScope) ([]gen.TrackableMotion, error) {
	motions, err := s.buildTrackableMotionsForLocation(ctx, location)
	if err != nil {
		return nil, err
	}
	return motions, s.publishTrackableMotionEvents(location.ProviderId, scope, motions)
}

func (s *Service) buildTrackableMotionsForLocation(ctx context.Context, location gen.Location) ([]gen.TrackableMotion, error) {
	if s.bus == nil || location.Trackables == nil {
		return nil, nil
	}
	motions := make([]gen.TrackableMotion, 0, len(*location.Trackables))
	for _, id := range *location.Trackables {
		baseMotion, err := s.trackableMotionBase(ctx, id)
		if err != nil {
			return nil, err
		}
		baseMotion.Location = location
		motions = append(motions, baseMotion)
	}
	return motions, nil
}

func (s *Service) publishTrackableMotionEvents(providerID string, scope EventScope, motions []gen.TrackableMotion) error {
	if s.bus == nil {
		return nil
	}
	for _, motion := range motions {
		event, err := newEvent(EventTrackableMotion, scope, locationTime(motion.Location), providerID, motion.Id, "", s.cfg.HubID, TrackableMotionEnvelope{Motion: motion})
		if err != nil {
			return err
		}
		s.bus.Emit(event)
	}
	return nil
}

func (s *Service) trackableMotionBase(ctx context.Context, id string) (gen.TrackableMotion, error) {
	baseMotion := gen.TrackableMotion{Id: id}
	if cache := s.metadataCache(); cache != nil {
		if trackable, ok := cache.TrackableByID(id); ok {
			baseMotion.Name = trackable.Name
			baseMotion.Geometry = trackable.Geometry
			baseMotion.Extrusion = trackable.Extrusion
			baseMotion.Properties = trackable.Properties
		}
		return baseMotion, nil
	}
	parsed, parseErr := uuid.Parse(id)
	if parseErr != nil {
		return baseMotion, nil
	}
	trackable, err := s.GetTrackable(ctx, openapi_types.UUID(parsed))
	if err != nil {
		return baseMotion, nil
	}
	baseMotion.Name = trackable.Name
	baseMotion.Geometry = trackable.Geometry
	baseMotion.Extrusion = trackable.Extrusion
	baseMotion.Properties = trackable.Properties
	return baseMotion, nil
}

func nativeLocationScope(location gen.Location) EventScope {
	if locationCRS(location) == "EPSG:4326" {
		return ScopeEPSG4326
	}
	return ScopeLocal
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
	if cache := s.metadataCache(); cache != nil {
		if zone, ok := cache.ZoneBySource(location.Source); ok {
			return zone, nil
		}
		return gen.Zone{}, badRequest("location source did not match a known zone id or foreign_id")
	}
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
	stageCtx, span := s.telemetry().StartSpan(ctx, "hub.process.fence_events",
		attribute.String("provider_id", location.ProviderId),
		attribute.String("source", location.Source),
		attribute.StringSlice("trackable_ids", stringSliceValue(location.Trackables)),
	)
	defer span.End()
	start := time.Now()
	if s.bus == nil || location.Trackables == nil {
		return nil
	}
	var fences []gen.Fence
	if cache := s.metadataCache(); cache != nil {
		fences = cache.fencesView()
	} else {
		var err error
		fences, err = s.ListFences(ctx)
		if err != nil {
			return err
		}
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
			wasInside := s.processingState().IsInsideFence(trackableID, fence.Id.String())
			switch {
			case inside && !wasInside:
				s.processingState().SetInsideFence(trackableID, fence.Id.String(), s.cfg.LocationTTL)
				event := gen.FenceEvent{
					EventType:   gen.RegionEntry,
					FenceId:     fence.Id,
					Id:          openapi_types.UUID(ids.NewUUID()),
					ProviderId:  &location.ProviderId,
					TrackableId: &trackableID,
					EntryTime:   &now,
					ForeignId:   fence.ForeignId,
				}
				if err := s.publishFenceEvent(ctx, fence, event); err != nil {
					span.RecordError(err)
					return err
				}
			case !inside && wasInside:
				s.processingState().ClearInsideFence(trackableID, fence.Id.String())
				event := gen.FenceEvent{
					EventType:   gen.RegionExit,
					FenceId:     fence.Id,
					Id:          openapi_types.UUID(ids.NewUUID()),
					ProviderId:  &location.ProviderId,
					TrackableId: &trackableID,
					ExitTime:    &now,
					ForeignId:   fence.ForeignId,
				}
				if err := s.publishFenceEvent(ctx, fence, event); err != nil {
					span.RecordError(err)
					return err
				}
			}
		}
	}
	s.telemetry().RecordProcessingDuration(stageCtx, "fence_evaluation", "location", time.Since(start))
	return nil
}

func (s *Service) publishFenceEvent(_ context.Context, fence gen.Fence, event gen.FenceEvent) error {
	geo, err := fenceGeoJSONFeatureCollection(fence, event)
	if err != nil {
		return err
	}
	envelope := FenceEventEnvelope{Event: event, Fence: fence, GeoJSON: geo}
	busEvent, err := newEvent(EventFenceEvent, ScopeDerived, fenceEventTime(event), stringPtrValue(event.ProviderId), stringPtrValue(event.TrackableId), event.FenceId.String(), s.cfg.HubID, envelope)
	if err != nil {
		return err
	}
	s.bus.Emit(busEvent)
	return nil
}

func (s *Service) publishCollisionEvents(ctx context.Context, motions []gen.TrackableMotion) error {
	stageCtx, span := s.telemetry().StartSpan(ctx, "hub.process.collision_events")
	defer span.End()
	start := time.Now()
	if s.bus == nil || !s.cfg.CollisionsEnabled || len(motions) == 0 {
		return nil
	}
	activeMotions := s.processingState().ListActiveMotions()
	indexedActiveMotions := make([]indexedCollisionMotion, 0, len(activeMotions))
	for _, motion := range activeMotions {
		point, err := point2D(motion.Location.Position)
		if err != nil {
			continue
		}
		indexed, ok := newIndexedCollisionMotion(motion, point)
		if !ok {
			continue
		}
		indexedActiveMotions = append(indexedActiveMotions, indexed)
	}
	activeMotionIndex := newCollisionSpatialIndex(indexedActiveMotions)
	trackablesByID := make(map[string]gen.Trackable, len(activeMotions)+len(motions))
	getTrackable := func(id string) (gen.Trackable, bool) {
		if trackable, ok := trackablesByID[id]; ok {
			return trackable, true
		}
		trackable, err := s.trackableByID(ctx, id)
		if err != nil {
			return gen.Trackable{}, false
		}
		trackablesByID[id] = trackable
		return trackable, true
	}
	maxActiveRadiusMeters := s.cfg.CollisionDefaultRadiusMeters
	for _, motion := range indexedActiveMotions {
		trackable, ok := getTrackable(motion.motion.Id)
		if !ok {
			continue
		}
		radius := effectiveRadiusMeters(trackable, s.cfg.CollisionDefaultRadiusMeters)
		if radius > maxActiveRadiusMeters {
			maxActiveRadiusMeters = radius
		}
	}
	for _, motion := range motions {
		s.processingState().SetMotion(motion.Id, motion, s.cfg.CollisionStateTTL)
		leftTrackable, ok := getTrackable(motion.Id)
		if !ok {
			continue
		}
		leftPoint, err := point2D(motion.Location.Position)
		if err != nil {
			continue
		}
		searchDistanceMeters := effectiveRadiusMeters(leftTrackable, s.cfg.CollisionDefaultRadiusMeters) + maxActiveRadiusMeters
		for _, candidate := range activeMotionIndex.Nearby(motion.Location, leftPoint, searchDistanceMeters) {
			otherID := candidate.motion.Id
			if otherID == motion.Id {
				continue
			}
			pairKey := collisionPairKey(motion.Id, otherID)
			otherMotion := candidate.motion
			otherPoint := candidate.point
			otherTrackable, ok := getTrackable(otherID)
			if !ok {
				continue
			}
			if !motionsMayCollide(motion, leftPoint, leftTrackable, otherMotion, otherPoint, otherTrackable, s.cfg.CollisionDefaultRadiusMeters) {
				s.processingState().DeleteCollisionState(pairKey)
				continue
			}
			event, active, err := s.evaluateCollision(motion, leftPoint, leftTrackable, otherMotion, otherPoint, otherTrackable)
			if err != nil {
				span.RecordError(err)
				return err
			}
			if !active {
				if event != nil {
					busEvent, err := newEvent(EventCollisionEvent, ScopeEPSG4326, timeValue(event.CollisionTime), motion.Location.ProviderId, motion.Id, "", s.cfg.HubID, CollisionEnvelope{Event: *event})
					if err != nil {
						span.RecordError(err)
						return err
					}
					s.bus.Emit(busEvent)
				}
				s.processingState().DeleteCollisionState(pairKey)
				continue
			}
			if event == nil {
				continue
			}
			s.processingState().SetCollisionState(pairKey, activeCollisionState{
				Active:      true,
				StartTime:   timeValue(event.StartTime),
				LastSeen:    timeValue(event.CollisionTime),
				LastEmitted: timeValue(event.CollisionTime),
			}, s.cfg.CollisionStateTTL)
			busEvent, err := newEvent(EventCollisionEvent, ScopeEPSG4326, timeValue(event.CollisionTime), motion.Location.ProviderId, motion.Id, "", s.cfg.HubID, CollisionEnvelope{Event: *event})
			if err != nil {
				span.RecordError(err)
				return err
			}
			s.bus.Emit(busEvent)
		}
	}
	s.telemetry().RecordProcessingDuration(stageCtx, "collision_evaluation", "location", time.Since(start))
	return nil
}

func (s *Service) trackableByID(ctx context.Context, id string) (gen.Trackable, error) {
	if cache := s.metadataCache(); cache != nil {
		if trackable, ok := cache.TrackableByID(id); ok {
			return trackable, nil
		}
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return gen.Trackable{}, err
	}
	return s.GetTrackable(ctx, openapi_types.UUID(parsed))
}

type activeCollisionState struct {
	Active      bool      `json:"active"`
	StartTime   time.Time `json:"start_time"`
	LastSeen    time.Time `json:"last_seen"`
	LastEmitted time.Time `json:"last_emitted"`
}

type collisionSpatialCell struct {
	space string
	x     int
	y     int
}

type indexedCollisionMotion struct {
	motion gen.TrackableMotion
	point  [2]float64
	x      float64
	y      float64
	space  string
}

type collisionSpatialIndex struct {
	cells map[collisionSpatialCell][]indexedCollisionMotion
}

func newCollisionSpatialIndex(motions []indexedCollisionMotion) collisionSpatialIndex {
	index := collisionSpatialIndex{cells: make(map[collisionSpatialCell][]indexedCollisionMotion, len(motions))}
	for _, motion := range motions {
		cell := collisionSpatialCell{
			space: motion.space,
			x:     collisionSpatialCellIndex(motion.x),
			y:     collisionSpatialCellIndex(motion.y),
		}
		index.cells[cell] = append(index.cells[cell], motion)
	}
	return index
}

func (i collisionSpatialIndex) Nearby(location gen.Location, point [2]float64, searchDistanceMeters float64) []indexedCollisionMotion {
	space, x, y, paddingFactor, ok := collisionSpatialPoint(location, point)
	if !ok {
		return nil
	}
	radiusCells := int(math.Ceil((searchDistanceMeters * paddingFactor) / collisionSpatialCellSizeMeters))
	if radiusCells < 1 {
		radiusCells = 1
	}
	candidates := make([]indexedCollisionMotion, 0)
	baseX := collisionSpatialCellIndex(x)
	baseY := collisionSpatialCellIndex(y)
	for dx := -radiusCells; dx <= radiusCells; dx++ {
		for dy := -radiusCells; dy <= radiusCells; dy++ {
			cell := collisionSpatialCell{space: space, x: baseX + dx, y: baseY + dy}
			candidates = append(candidates, i.cells[cell]...)
		}
	}
	return candidates
}

func newIndexedCollisionMotion(motion gen.TrackableMotion, point [2]float64) (indexedCollisionMotion, bool) {
	space, x, y, _, ok := collisionSpatialPoint(motion.Location, point)
	if !ok {
		return indexedCollisionMotion{}, false
	}
	return indexedCollisionMotion{
		motion: motion,
		point:  point,
		x:      x,
		y:      y,
		space:  space,
	}, true
}

func (s *Service) evaluateCollision(leftMotion gen.TrackableMotion, leftPoint [2]float64, leftTrackable gen.Trackable, rightMotion gen.TrackableMotion, rightPoint [2]float64, rightTrackable gen.Trackable) (*gen.CollisionEvent, bool, error) {
	colliding, area, distance := motionsCollide(leftMotion, leftTrackable, rightMotion, rightTrackable, leftPoint, rightPoint, s.cfg.CollisionDefaultRadiusMeters)
	key := collisionPairKey(leftMotion.Id, rightMotion.Id)
	var state activeCollisionState
	if existing, ok := s.processingState().GetCollisionState(key); ok {
		state = existing
	}
	if !colliding {
		if !state.Active {
			return nil, false, nil
		}
		now := s.now().UTC()
		event := collisionEventForPair(gen.CollisionEnd, now, state.StartTime, leftMotion, leftTrackable, rightMotion, rightTrackable, area, distance, s.cfg.CollisionDefaultRadiusMeters)
		event.EndTime = &now
		return &event, false, nil
	}
	now := s.now().UTC()
	if !state.Active {
		event := collisionEventForPair(gen.CollisionStart, now, now, leftMotion, leftTrackable, rightMotion, rightTrackable, area, distance, s.cfg.CollisionDefaultRadiusMeters)
		return &event, true, nil
	}
	if s.cfg.CollisionCollidingDebounce > 0 && !state.LastEmitted.IsZero() && now.Sub(state.LastEmitted) < s.cfg.CollisionCollidingDebounce {
		return nil, true, nil
	}
	event := collisionEventForPair(gen.Colliding, now, state.StartTime, leftMotion, leftTrackable, rightMotion, rightTrackable, area, distance, s.cfg.CollisionDefaultRadiusMeters)
	return &event, true, nil
}

func collisionEventForPair(kind gen.CollisionEventCollisionType, at, start time.Time, leftMotion gen.TrackableMotion, leftTrackable gen.Trackable, rightMotion gen.TrackableMotion, rightTrackable gen.Trackable, area *gen.CollisionEvent_CollisionArea, distance float32, defaultRadiusMeters float64) gen.CollisionEvent {
	event := gen.CollisionEvent{
		Id:            openapi_types.UUID(ids.NewUUID()),
		CollisionType: kind,
		CollisionTime: &at,
		Collisions: []gen.Collision{
			collisionFromMotion(leftMotion, leftTrackable, defaultRadiusMeters),
			collisionFromMotion(rightMotion, rightTrackable, defaultRadiusMeters),
		},
		CenterDistance: &distance,
	}
	if !start.IsZero() {
		event.StartTime = &start
	}
	if area != nil {
		event.CollisionArea = area
	}
	return event
}

func collisionFromMotion(motion gen.TrackableMotion, trackable gen.Trackable, defaultRadiusMeters float64) gen.Collision {
	id := uuid.Nil
	if parsed, err := uuid.Parse(motion.Id); err == nil {
		id = parsed
	}
	geometry := motion.Geometry
	if geometry == nil {
		geometry = trackable.Geometry
	}
	if geometry == nil {
		geometry = pointSquarePolygon(motion.Location, effectiveRadiusMeters(trackable, defaultRadiusMeters))
	}
	return gen.Collision{
		Id:         openapi_types.UUID(id),
		ObjectType: string(trackable.Type),
		Position:   motion.Location.Position,
		Geometry:   *geometry,
	}
}

func motionsCollide(leftMotion gen.TrackableMotion, leftTrackable gen.Trackable, rightMotion gen.TrackableMotion, rightTrackable gen.Trackable, leftPoint, rightPoint [2]float64, defaultRadiusMeters float64) (bool, *gen.CollisionEvent_CollisionArea, float32) {
	distanceSquared := collisionDistanceSquaredMeters(leftMotion.Location, rightMotion.Location, leftPoint, rightPoint)
	distanceMeters := math.Sqrt(distanceSquared)
	distance := float32(distanceMeters)
	if leftMotion.Geometry != nil && rightMotion.Geometry != nil && polygonsOverlap(*leftMotion.Geometry, *rightMotion.Geometry) {
		area := collisionAreaPoint(midpoint(leftPoint, rightPoint))
		return true, &area, distance
	}
	radius := effectiveRadiusMeters(leftTrackable, defaultRadiusMeters) + effectiveRadiusMeters(rightTrackable, defaultRadiusMeters)
	if distanceSquared <= radius*radius {
		area := collisionAreaPoint(midpoint(leftPoint, rightPoint))
		return true, &area, distance
	}
	return false, nil, distance
}

func motionsMayCollide(leftMotion gen.TrackableMotion, leftPoint [2]float64, leftTrackable gen.Trackable, rightMotion gen.TrackableMotion, rightPoint [2]float64, rightTrackable gen.Trackable, defaultRadiusMeters float64) bool {
	maxDistanceMeters := effectiveRadiusMeters(leftTrackable, defaultRadiusMeters) + effectiveRadiusMeters(rightTrackable, defaultRadiusMeters)
	dxMeters, dyMeters := collisionAxisDistancesMeters(leftMotion.Location, rightMotion.Location, leftPoint, rightPoint)
	return dxMeters <= maxDistanceMeters && dyMeters <= maxDistanceMeters
}

func collisionPairKey(leftID, rightID string) string {
	ids := []string{leftID, rightID}
	sort.Strings(ids)
	return fmt.Sprintf("hub:collision:%s:%s", ids[0], ids[1])
}

func stringPtrValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func timeValue(v *time.Time) time.Time {
	if v == nil {
		return time.Time{}
	}
	return *v
}

func locationTime(location gen.Location) time.Time {
	if location.TimestampGenerated != nil {
		return *location.TimestampGenerated
	}
	if location.TimestampSent != nil {
		return *location.TimestampSent
	}
	return time.Time{}
}

func proximityTime(proximity gen.Proximity) time.Time {
	if proximity.TimestampGenerated != nil {
		return *proximity.TimestampGenerated
	}
	if proximity.TimestampSent != nil {
		return *proximity.TimestampSent
	}
	return time.Time{}
}

func fenceEventTime(event gen.FenceEvent) time.Time {
	if event.EntryTime != nil {
		return *event.EntryTime
	}
	if event.ExitTime != nil {
		return *event.ExitTime
	}
	return time.Time{}
}

func locationGeoJSONFeatureCollection(location gen.Location) (GeoJSONFeatureCollection, error) {
	return GeoJSONFeatureCollection{
		Type: "FeatureCollection",
		Features: []GeoJSONFeature{{
			Type:       "Feature",
			Geometry:   location.Position,
			Properties: locationProperties(location),
		}},
	}, nil
}

func fenceGeoJSONFeatureCollection(fence gen.Fence, event gen.FenceEvent) (GeoJSONFeatureCollection, error) {
	region, err := fenceRegionGeometry(fence.Region)
	if err != nil {
		return GeoJSONFeatureCollection{}, err
	}
	return GeoJSONFeatureCollection{
		Type: "FeatureCollection",
		Features: []GeoJSONFeature{{
			Type:       "Feature",
			Geometry:   region,
			Properties: fenceEventProperties(event),
		}},
	}, nil
}

func locationProperties(location gen.Location) map[string]any {
	properties := make(map[string]any, 15)
	setProperty(properties, "accuracy", location.Accuracy)
	setProperty(properties, "associated", location.Associated)
	setProperty(properties, "course", location.Course)
	setProperty(properties, "crs", location.Crs)
	setProperty(properties, "elevation_ref", location.ElevationRef)
	setProperty(properties, "floor", location.Floor)
	setProperty(properties, "heading_accuracy", location.HeadingAccuracy)
	setProperty(properties, "magnetic_heading", location.MagneticHeading)
	if location.Properties != nil {
		properties["properties"] = cloneExtensionProperties(*location.Properties)
	}
	properties["provider_id"] = location.ProviderId
	properties["provider_type"] = location.ProviderType
	properties["source"] = location.Source
	setProperty(properties, "speed", location.Speed)
	setProperty(properties, "timestamp_generated", location.TimestampGenerated)
	setProperty(properties, "timestamp_sent", location.TimestampSent)
	if location.Trackables != nil {
		properties["trackables"] = append([]string(nil), []string(*location.Trackables)...)
	}
	setProperty(properties, "true_heading", location.TrueHeading)
	return properties
}

func fenceEventProperties(event gen.FenceEvent) map[string]any {
	properties := make(map[string]any, 9)
	setProperty(properties, "entry_time", event.EntryTime)
	properties["event_type"] = event.EventType
	setProperty(properties, "exit_time", event.ExitTime)
	properties["fence_id"] = event.FenceId
	setProperty(properties, "foreign_id", event.ForeignId)
	properties["id"] = event.Id
	if event.Properties != nil {
		properties["properties"] = cloneExtensionProperties(*event.Properties)
	}
	setProperty(properties, "provider_id", event.ProviderId)
	setProperty(properties, "trackable_id", event.TrackableId)
	if event.Trackables != nil {
		properties["trackables"] = append([]string(nil), []string(*event.Trackables)...)
	}
	return properties
}

func setProperty[T any](properties map[string]any, key string, value *T) {
	if value != nil {
		properties[key] = *value
	}
}

func cloneExtensionProperties(properties gen.ExtensionProperties) map[string]any {
	cloned := make(map[string]any, len(properties))
	for key, value := range properties {
		cloned[key] = value
	}
	return cloned
}

func fenceRegionGeometry(region gen.Fence_Region) (any, error) {
	if point, err := region.AsPoint(); err == nil {
		return point, nil
	}
	if polygon, err := region.AsPolygon(); err == nil {
		return polygon, nil
	}
	return nil, badRequest("unsupported fence region geometry")
}

func midpoint(left, right [2]float64) [2]float64 {
	return [2]float64{(left[0] + right[0]) / 2, (left[1] + right[1]) / 2}
}

func collisionAreaPoint(point [2]float64) gen.CollisionEvent_CollisionArea {
	area := gen.CollisionEvent_CollisionArea{}
	geo := gen.Point{Type: "Point"}
	_ = geo.Coordinates.FromGeoJsonPosition2D([]float32{float32(point[0]), float32(point[1])})
	_ = area.FromPoint(geo)
	return area
}

const defaultCollisionRadiusMeters = 0.5
const collisionSpatialCellSizeMeters = 100.0
const collisionSpatialWGS84PaddingFactor = 2.0

func effectiveRadiusMeters(trackable gen.Trackable, defaultRadiusMeters float64) float64 {
	if trackable.Radius != nil && *trackable.Radius > 0 {
		return float64(*trackable.Radius)
	}
	if defaultRadiusMeters > 0 {
		return defaultRadiusMeters
	}
	return defaultCollisionRadiusMeters
}

func collisionSpatialCellIndex(value float64) int {
	return int(math.Floor(value / collisionSpatialCellSizeMeters))
}

func collisionSpatialPoint(location gen.Location, point [2]float64) (space string, x, y, paddingFactor float64, ok bool) {
	crs := strings.TrimSpace(locationCRS(location))
	if crs == "" {
		crs = "local"
	}
	if crs == "EPSG:4326" {
		projectedX, projectedY, valid := wgs84PointToWebMercator(point)
		return crs, projectedX, projectedY, collisionSpatialWGS84PaddingFactor, valid
	}
	return crs, point[0], point[1], 1, true
}

func wgs84PointToWebMercator(point [2]float64) (float64, float64, bool) {
	lon := point[0]
	lat := point[1]
	if lon < -180 || lon > 180 || lat < -90 || lat > 90 {
		return 0, 0, false
	}
	lat = math.Max(math.Min(lat, 85.05112878), -85.05112878)
	x := 6378137.0 * degreesToRadians(lon)
	latRad := degreesToRadians(lat)
	y := 6378137.0 * math.Log(math.Tan(math.Pi/4+latRad/2))
	return x, y, true
}

func pointSquarePolygon(location gen.Location, radiusMeters float64) *gen.Polygon {
	point, err := point2D(location.Position)
	if err != nil {
		return nil
	}
	return squarePolygon(point, locationCRS(location), radiusMeters)
}

func squarePolygon(center [2]float64, crs string, radiusMeters float64) *gen.Polygon {
	dx, dy := collisionMetersToCoordinateOffsets(crs, center, radiusMeters)
	polygon := gen.Polygon{Type: "Polygon"}
	ring := []gen.GeoJsonPosition{}
	points := [][2]float64{
		{center[0] - dx, center[1] - dy},
		{center[0] + dx, center[1] - dy},
		{center[0] + dx, center[1] + dy},
		{center[0] - dx, center[1] + dy},
		{center[0] - dx, center[1] - dy},
	}
	for _, p := range points {
		pos := gen.GeoJsonPosition{}
		_ = pos.FromGeoJsonPosition2D([]float32{float32(p[0]), float32(p[1])})
		ring = append(ring, pos)
	}
	polygon.Coordinates = [][]gen.GeoJsonPosition{ring}
	return &polygon
}

func collisionDistanceSquaredMeters(leftLocation, rightLocation gen.Location, leftPoint, rightPoint [2]float64) float64 {
	dx, dy := collisionAxisDistancesMeters(leftLocation, rightLocation, leftPoint, rightPoint)
	return dx*dx + dy*dy
}

func collisionAxisDistancesMeters(leftLocation, rightLocation gen.Location, leftPoint, rightPoint [2]float64) (float64, float64) {
	if collisionUsesWGS84(leftLocation, rightLocation) {
		// Tradeoff: use a cheap equirectangular-style approximation instead of
		// haversine/geodesic math so collision candidate checks stay lightweight
		// on the decision hot path. This is accurate enough for the short-range
		// thresholds the hub currently evaluates, but it is not intended for
		// long-distance navigation or great-circle measurements.
		latRad := degreesToRadians((leftPoint[1] + rightPoint[1]) / 2)
		return metersPerLongitudeDegreeAtLatitude(latRad) * math.Abs(leftPoint[0]-rightPoint[0]), metersPerLatitudeDegree * math.Abs(leftPoint[1]-rightPoint[1])
	}
	return math.Abs(leftPoint[0] - rightPoint[0]), math.Abs(leftPoint[1] - rightPoint[1])
}

func collisionMetersToCoordinateOffsets(crs string, center [2]float64, radiusMeters float64) (float64, float64) {
	if strings.TrimSpace(crs) == "EPSG:4326" {
		// Tradeoff: derive a square envelope from the same planar approximation
		// used for distance checks so fallback collision geometry stays fast and
		// consistent with the runtime threshold model.
		latRad := degreesToRadians(center[1])
		return radiusMeters / metersPerLongitudeDegreeAtLatitude(latRad), radiusMeters / metersPerLatitudeDegree
	}
	return radiusMeters, radiusMeters
}

func collisionUsesWGS84(leftLocation, rightLocation gen.Location) bool {
	return strings.TrimSpace(locationCRS(leftLocation)) == "EPSG:4326" && strings.TrimSpace(locationCRS(rightLocation)) == "EPSG:4326"
}

const metersPerLatitudeDegree = 111_320.0

func metersPerLongitudeDegreeAtLatitude(latitudeRadians float64) float64 {
	value := metersPerLatitudeDegree * math.Cos(latitudeRadians)
	if math.Abs(value) < math.SmallestNonzeroFloat64 {
		return math.SmallestNonzeroFloat64
	}
	return value
}

func degreesToRadians(value float64) float64 {
	return value * math.Pi / 180
}

func polygonsOverlap(left, right gen.Polygon) bool {
	leftBounds, err := polygonBounds(left)
	if err != nil {
		return false
	}
	rightBounds, err := polygonBounds(right)
	if err != nil {
		return false
	}
	return !(leftBounds.maxX < rightBounds.minX || rightBounds.maxX < leftBounds.minX || leftBounds.maxY < rightBounds.minY || rightBounds.maxY < leftBounds.minY)
}

type bounds struct {
	minX float64
	minY float64
	maxX float64
	maxY float64
}

func polygonBounds(polygon gen.Polygon) (bounds, error) {
	if len(polygon.Coordinates) == 0 {
		return bounds{}, fmt.Errorf("polygon has no coordinates")
	}
	out := bounds{minX: math.MaxFloat64, minY: math.MaxFloat64, maxX: -math.MaxFloat64, maxY: -math.MaxFloat64}
	for _, pos := range polygon.Coordinates[0] {
		xy, err := pos.AsGeoJsonPosition2D()
		if err != nil || len(xy) < 2 {
			continue
		}
		x := float64(xy[0])
		y := float64(xy[1])
		if x < out.minX {
			out.minX = x
		}
		if x > out.maxX {
			out.maxX = x
		}
		if y < out.minY {
			out.minY = y
		}
		if y > out.maxY {
			out.maxY = y
		}
	}
	if out.minX == math.MaxFloat64 {
		return bounds{}, fmt.Errorf("polygon bounds unavailable")
	}
	return out, nil
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
			doc["id"] = ids.NewString()
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
			doc["id"] = ids.NewString()
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
	provider := gen.LocationProvider(body)
	payload, err := json.Marshal(provider)
	return provider, payload, err
}

func normalizeTrackable(body gen.TrackableWrite, forcedID uuid.UUID) (gen.Trackable, []byte, error) {
	id := forcedID
	if id == uuid.Nil {
		if body.Id != nil {
			id = uuid.UUID(*body.Id)
		} else {
			id = ids.NewUUID()
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

func stringSliceValue(values *[]string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), (*values)...)
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
