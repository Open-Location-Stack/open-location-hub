package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/formation-res/open-rtls-hub/internal/mqtt"
	"github.com/formation-res/open-rtls-hub/internal/state/valkey"
	"github.com/formation-res/open-rtls-hub/internal/storage/postgres/sqlcgen"
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

type Config struct {
	LocationTTL  time.Duration
	ProximityTTL time.Duration
	DedupTTL     time.Duration
}

type Service struct {
	logger    *zap.Logger
	queries   sqlcgen.Querier
	cache     *valkey.Client
	publisher Publisher
	cfg       Config
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
		logger:    logger,
		queries:   queries,
		cache:     cache,
		publisher: publisher,
		cfg:       cfg,
	}
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
	return decodePayload[gen.Zone](row.Payload)
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
	return decodePayload[gen.Zone](row.Payload)
}

func (s *Service) DeleteZone(ctx context.Context, id openapi_types.UUID) error {
	rows, err := s.queries.DeleteZone(ctx, uuidParam(id))
	if err != nil {
		return translateDBError(err)
	}
	if rows == 0 {
		return notFound("zone not found")
	}
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
	for _, zone := range zones {
		if zone.Id.String() != proximity.Source && (zone.ForeignId == nil || *zone.ForeignId != proximity.Source) {
			continue
		}
		if zone.Position == nil {
			return gen.Location{}, badRequest("proximity source zone does not expose a position")
		}
		crs := "local"
		return gen.Location{
			Accuracy:           proximity.Accuracy,
			Crs:                &crs,
			Position:           *zone.Position,
			Properties:         proximity.Properties,
			ProviderId:         proximity.ProviderId,
			ProviderType:       proximity.ProviderType,
			Source:             proximity.Source,
			TimestampGenerated: proximity.TimestampGenerated,
			TimestampSent:      proximity.TimestampSent,
		}, nil
	}
	return gen.Location{}, badRequest("proximity source did not match a known zone id or foreign_id")
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
	if err := s.publisher.PublishJSON(ctx, mqtt.TopicLocationLocal(location.ProviderId), location, false); err != nil {
		return err
	}
	return s.publisher.PublishJSON(ctx, mqtt.TopicLocationEPSG4326(location.ProviderId), location, false)
}

func (s *Service) publishTrackableMotions(ctx context.Context, location gen.Location) error {
	if s.publisher == nil || location.Trackables == nil {
		return nil
	}
	for _, id := range *location.Trackables {
		motion := gen.TrackableMotion{
			Id:       id,
			Location: location,
		}
		if parsed, err := uuid.Parse(id); err == nil {
			trackable, getErr := s.GetTrackable(ctx, openapi_types.UUID(parsed))
			if getErr == nil {
				motion.Name = trackable.Name
				motion.Geometry = trackable.Geometry
				motion.Extrusion = trackable.Extrusion
				motion.Properties = trackable.Properties
			}
		}
		if err := s.publisher.PublishJSON(ctx, mqtt.TopicTrackableMotionLocal(id), motion, false); err != nil {
			return err
		}
		if err := s.publisher.PublishJSON(ctx, mqtt.TopicTrackableMotionEPSG4326(id), motion, false); err != nil {
			return err
		}
	}
	return nil
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
	return nil
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
