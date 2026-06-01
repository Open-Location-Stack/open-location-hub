package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dhconnelly/rtreego"
	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/formation-res/open-rtls-hub/internal/storage/postgres/sqlcgen"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

const (
	metadataTypeZone             = "zone"
	metadataTypeFence            = "fence"
	metadataTypeTrackable        = "trackable"
	metadataTypeLocationProvider = "location_provider"
)

type metadataSnapshot struct {
	zones               []gen.Zone
	zonesByID           map[string]gen.Zone
	zonesByForeignID    map[string]gen.Zone
	zoneSignatures      map[string]string
	fences              []gen.Fence
	fencesByID          map[string]gen.Fence
	fenceSignatures     map[string]string
	fenceIndexes        map[string]*fenceSpatialIndex
	trackables          []gen.Trackable
	trackablesByID      map[string]gen.Trackable
	trackableSignatures map[string]string
	providers           []gen.LocationProvider
	providersByID       map[string]gen.LocationProvider
	providerSignatures  map[string]string
}

const (
	fenceIndexDimensions = 2
	fenceIndexMinEntries = 8
	fenceIndexMaxEntries = 32
)

type fenceSpatialIndex struct {
	tree    *rtreego.Rtree
	entries map[string]indexedFence
}

type indexedFence struct {
	fence gen.Fence
	rect  rtreego.Rect
}

func (f indexedFence) Bounds() rtreego.Rect {
	return f.rect
}

// MetadataCache keeps an immutable in-memory metadata snapshot used by the
// ingest and eventing hot path.
type MetadataCache struct {
	mu       sync.RWMutex
	queries  sqlcgen.Querier
	snapshot metadataSnapshot
}

// NewMetadataCache loads the initial metadata snapshot from durable storage.
func NewMetadataCache(ctx context.Context, queries sqlcgen.Querier) (*MetadataCache, error) {
	cache := &MetadataCache{queries: queries}
	snapshot, err := loadMetadataSnapshot(ctx, queries)
	if err != nil {
		return nil, err
	}
	cache.snapshot = snapshot
	return cache, nil
}

func loadMetadataSnapshot(ctx context.Context, queries sqlcgen.Querier) (metadataSnapshot, error) {
	zones, err := loadZones(ctx, queries)
	if err != nil {
		return metadataSnapshot{}, err
	}
	fences, err := loadFences(ctx, queries)
	if err != nil {
		return metadataSnapshot{}, err
	}
	trackables, err := loadTrackables(ctx, queries)
	if err != nil {
		return metadataSnapshot{}, err
	}
	providers, err := loadProviders(ctx, queries)
	if err != nil {
		return metadataSnapshot{}, err
	}
	return newMetadataSnapshot(zones, fences, trackables, providers), nil
}

func loadZones(ctx context.Context, queries sqlcgen.Querier) ([]zoneRecord, error) {
	rows, err := queries.ListZones(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]zoneRecord, 0, len(rows))
	for _, row := range rows {
		item, err := decodePayload[gen.Zone](row.Payload)
		if err != nil {
			return nil, err
		}
		out = append(out, zoneRecord{Zone: item, Signature: payloadSignature(row.Payload)})
	}
	return out, nil
}

func loadFences(ctx context.Context, queries sqlcgen.Querier) ([]fenceRecord, error) {
	rows, err := queries.ListFences(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]fenceRecord, 0, len(rows))
	for _, row := range rows {
		item, err := decodePayload[gen.Fence](row.Payload)
		if err != nil {
			return nil, err
		}
		out = append(out, fenceRecord{Fence: item, Signature: payloadSignature(row.Payload)})
	}
	return out, nil
}

func loadTrackables(ctx context.Context, queries sqlcgen.Querier) ([]trackableRecord, error) {
	rows, err := queries.ListTrackables(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]trackableRecord, 0, len(rows))
	for _, row := range rows {
		item, err := decodePayload[gen.Trackable](row.Payload)
		if err != nil {
			return nil, err
		}
		out = append(out, trackableRecord{Trackable: item, Signature: payloadSignature(row.Payload)})
	}
	return out, nil
}

func loadProviders(ctx context.Context, queries sqlcgen.Querier) ([]providerRecord, error) {
	rows, err := queries.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]providerRecord, 0, len(rows))
	for _, row := range rows {
		item, err := decodePayload[gen.LocationProvider](row.Payload)
		if err != nil {
			return nil, err
		}
		out = append(out, providerRecord{Provider: item, Signature: payloadSignature(row.Payload)})
	}
	return out, nil
}

type zoneRecord struct {
	Zone      gen.Zone
	Signature string
}

type fenceRecord struct {
	Fence     gen.Fence
	Signature string
}

type trackableRecord struct {
	Trackable gen.Trackable
	Signature string
}

type providerRecord struct {
	Provider  gen.LocationProvider
	Signature string
}

func newMetadataSnapshot(zones []zoneRecord, fences []fenceRecord, trackables []trackableRecord, providers []providerRecord) metadataSnapshot {
	snapshot := metadataSnapshot{
		zones:               make([]gen.Zone, 0, len(zones)),
		zonesByID:           make(map[string]gen.Zone, len(zones)),
		zonesByForeignID:    make(map[string]gen.Zone, len(zones)),
		zoneSignatures:      make(map[string]string, len(zones)),
		fences:              make([]gen.Fence, 0, len(fences)),
		fencesByID:          make(map[string]gen.Fence, len(fences)),
		fenceSignatures:     make(map[string]string, len(fences)),
		fenceIndexes:        make(map[string]*fenceSpatialIndex),
		trackables:          make([]gen.Trackable, 0, len(trackables)),
		trackablesByID:      make(map[string]gen.Trackable, len(trackables)),
		trackableSignatures: make(map[string]string, len(trackables)),
		providers:           make([]gen.LocationProvider, 0, len(providers)),
		providersByID:       make(map[string]gen.LocationProvider, len(providers)),
		providerSignatures:  make(map[string]string, len(providers)),
	}
	for _, record := range zones {
		item := record.Zone
		snapshot.zones = append(snapshot.zones, item)
		snapshot.zonesByID[item.Id.String()] = item
		if item.ForeignId != nil && *item.ForeignId != "" {
			snapshot.zonesByForeignID[*item.ForeignId] = item
		}
		snapshot.zoneSignatures[item.Id.String()] = record.Signature
	}
	for _, record := range fences {
		item := record.Fence
		snapshot.fences = append(snapshot.fences, item)
		snapshot.fencesByID[item.Id.String()] = item
		snapshot.fenceSignatures[item.Id.String()] = record.Signature
	}
	for _, record := range trackables {
		item := record.Trackable
		snapshot.trackables = append(snapshot.trackables, item)
		snapshot.trackablesByID[item.Id.String()] = item
		snapshot.trackableSignatures[item.Id.String()] = record.Signature
	}
	for _, record := range providers {
		item := record.Provider
		snapshot.providers = append(snapshot.providers, item)
		snapshot.providersByID[item.Id] = item
		snapshot.providerSignatures[item.Id] = record.Signature
	}
	snapshot.fenceIndexes = buildFenceIndexes(snapshot.fences)
	return snapshot
}

func payloadSignature(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func (c *MetadataCache) current() metadataSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshot
}

func (c *MetadataCache) ListZones() []gen.Zone {
	snapshot := c.current()
	return append([]gen.Zone(nil), snapshot.zones...)
}

func (c *MetadataCache) ListFences() []gen.Fence {
	snapshot := c.current()
	return append([]gen.Fence(nil), snapshot.fences...)
}

func (c *MetadataCache) ListTrackables() []gen.Trackable {
	snapshot := c.current()
	return append([]gen.Trackable(nil), snapshot.trackables...)
}

func (c *MetadataCache) ListProviders() []gen.LocationProvider {
	snapshot := c.current()
	return append([]gen.LocationProvider(nil), snapshot.providers...)
}

func (c *MetadataCache) ZoneByID(id string) (gen.Zone, bool) {
	snapshot := c.current()
	item, ok := snapshot.zonesByID[id]
	return item, ok
}

func (c *MetadataCache) ZoneBySource(source string) (gen.Zone, bool) {
	snapshot := c.current()
	if item, ok := snapshot.zonesByID[source]; ok {
		return item, true
	}
	item, ok := snapshot.zonesByForeignID[source]
	return item, ok
}

func (c *MetadataCache) FenceByID(id string) (gen.Fence, bool) {
	snapshot := c.current()
	item, ok := snapshot.fencesByID[id]
	return item, ok
}

func (c *MetadataCache) FenceCandidates(location gen.Location) ([]gen.Fence, error) {
	snapshot := c.current()
	return snapshot.fenceCandidates(location)
}

func (c *MetadataCache) TrackableByID(id string) (gen.Trackable, bool) {
	snapshot := c.current()
	item, ok := snapshot.trackablesByID[id]
	return item, ok
}

func (c *MetadataCache) ProviderByID(id string) (gen.LocationProvider, bool) {
	snapshot := c.current()
	item, ok := snapshot.providersByID[id]
	return item, ok
}

func (c *MetadataCache) UpsertZone(item gen.Zone, signature string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := c.snapshot
	next.zones = upsertByZoneID(next.zones, item)
	next.zonesByID = cloneZoneMap(next.zonesByID)
	next.zonesByForeignID = cloneZoneMap(next.zonesByForeignID)
	next.zoneSignatures = cloneStringMap(next.zoneSignatures)
	next.zonesByID[item.Id.String()] = item
	for key, zone := range next.zonesByForeignID {
		if zone.Id == item.Id {
			delete(next.zonesByForeignID, key)
		}
	}
	if item.ForeignId != nil && *item.ForeignId != "" {
		next.zonesByForeignID[*item.ForeignId] = item
	}
	next.zoneSignatures[item.Id.String()] = signature
	next.fenceIndexes = buildFenceIndexes(next.fences)
	c.snapshot = next
}

func (c *MetadataCache) DeleteZone(id openapi_types.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := c.snapshot
	next.zones = removeZoneByID(next.zones, id)
	next.zonesByID = cloneZoneMap(next.zonesByID)
	next.zonesByForeignID = cloneZoneMap(next.zonesByForeignID)
	next.zoneSignatures = cloneStringMap(next.zoneSignatures)
	delete(next.zonesByID, id.String())
	for key, zone := range next.zonesByForeignID {
		if zone.Id == id {
			delete(next.zonesByForeignID, key)
		}
	}
	delete(next.zoneSignatures, id.String())
	next.fenceIndexes = buildFenceIndexes(next.fences)
	c.snapshot = next
}

func (c *MetadataCache) UpsertFence(item gen.Fence, signature string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := c.snapshot
	next.fences = upsertByFenceID(next.fences, item)
	next.fencesByID = cloneFenceMap(next.fencesByID)
	next.fenceSignatures = cloneStringMap(next.fenceSignatures)
	next.fencesByID[item.Id.String()] = item
	next.fenceSignatures[item.Id.String()] = signature
	next.fenceIndexes = buildFenceIndexes(next.fences)
	c.snapshot = next
}

func (c *MetadataCache) DeleteFence(id openapi_types.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := c.snapshot
	next.fences = removeFenceByID(next.fences, id)
	next.fencesByID = cloneFenceMap(next.fencesByID)
	next.fenceSignatures = cloneStringMap(next.fenceSignatures)
	delete(next.fencesByID, id.String())
	delete(next.fenceSignatures, id.String())
	next.fenceIndexes = buildFenceIndexes(next.fences)
	c.snapshot = next
}

func (c *MetadataCache) UpsertTrackable(item gen.Trackable, signature string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := c.snapshot
	next.trackables = upsertByTrackableID(next.trackables, item)
	next.trackablesByID = cloneTrackableMap(next.trackablesByID)
	next.trackableSignatures = cloneStringMap(next.trackableSignatures)
	next.trackablesByID[item.Id.String()] = item
	next.trackableSignatures[item.Id.String()] = signature
	c.snapshot = next
}

func (c *MetadataCache) DeleteTrackable(id openapi_types.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := c.snapshot
	next.trackables = removeTrackableByID(next.trackables, id)
	next.trackablesByID = cloneTrackableMap(next.trackablesByID)
	next.trackableSignatures = cloneStringMap(next.trackableSignatures)
	delete(next.trackablesByID, id.String())
	delete(next.trackableSignatures, id.String())
	c.snapshot = next
}

func (c *MetadataCache) UpsertProvider(item gen.LocationProvider, signature string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := c.snapshot
	next.providers = upsertByProviderID(next.providers, item)
	next.providersByID = cloneProviderMap(next.providersByID)
	next.providerSignatures = cloneStringMap(next.providerSignatures)
	next.providersByID[item.Id] = item
	next.providerSignatures[item.Id] = signature
	c.snapshot = next
}

func (c *MetadataCache) DeleteProvider(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := c.snapshot
	next.providers = removeProviderByID(next.providers, id)
	next.providersByID = cloneProviderMap(next.providersByID)
	next.providerSignatures = cloneStringMap(next.providerSignatures)
	delete(next.providersByID, id)
	delete(next.providerSignatures, id)
	c.snapshot = next
}

func (c *MetadataCache) Reconcile(ctx context.Context, now time.Time) ([]MetadataChange, error) {
	next, err := loadMetadataSnapshot(ctx, c.queries)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	changes := diffMetadataSnapshots(c.snapshot, next, now)
	c.snapshot = next
	return changes, nil
}

func diffMetadataSnapshots(current, next metadataSnapshot, now time.Time) []MetadataChange {
	var changes []MetadataChange
	changes = append(changes, diffSignatures(metadataTypeZone, current.zoneSignatures, next.zoneSignatures, now)...)
	changes = append(changes, diffSignatures(metadataTypeFence, current.fenceSignatures, next.fenceSignatures, now)...)
	changes = append(changes, diffSignatures(metadataTypeTrackable, current.trackableSignatures, next.trackableSignatures, now)...)
	changes = append(changes, diffSignatures(metadataTypeLocationProvider, current.providerSignatures, next.providerSignatures, now)...)
	return changes
}

func diffSignatures(resourceType string, current, next map[string]string, now time.Time) []MetadataChange {
	changes := make([]MetadataChange, 0)
	for id, signature := range next {
		existing, ok := current[id]
		switch {
		case !ok:
			changes = append(changes, MetadataChange{ID: id, Type: resourceType, Operation: metadataOperationCreate, Timestamp: now})
		case existing != signature:
			changes = append(changes, MetadataChange{ID: id, Type: resourceType, Operation: metadataOperationUpdate, Timestamp: now})
		}
	}
	for id := range current {
		if _, ok := next[id]; !ok {
			changes = append(changes, MetadataChange{ID: id, Type: resourceType, Operation: metadataOperationDelete, Timestamp: now})
		}
	}
	return changes
}

func upsertByZoneID(items []gen.Zone, item gen.Zone) []gen.Zone {
	for i := range items {
		if items[i].Id == item.Id {
			next := append([]gen.Zone(nil), items...)
			next[i] = item
			return next
		}
	}
	return append(append([]gen.Zone(nil), items...), item)
}

func removeZoneByID(items []gen.Zone, id openapi_types.UUID) []gen.Zone {
	next := make([]gen.Zone, 0, len(items))
	for _, item := range items {
		if item.Id != id {
			next = append(next, item)
		}
	}
	return next
}

func upsertByFenceID(items []gen.Fence, item gen.Fence) []gen.Fence {
	for i := range items {
		if items[i].Id == item.Id {
			next := append([]gen.Fence(nil), items...)
			next[i] = item
			return next
		}
	}
	return append(append([]gen.Fence(nil), items...), item)
}

func removeFenceByID(items []gen.Fence, id openapi_types.UUID) []gen.Fence {
	next := make([]gen.Fence, 0, len(items))
	for _, item := range items {
		if item.Id != id {
			next = append(next, item)
		}
	}
	return next
}

func upsertByTrackableID(items []gen.Trackable, item gen.Trackable) []gen.Trackable {
	for i := range items {
		if items[i].Id == item.Id {
			next := append([]gen.Trackable(nil), items...)
			next[i] = item
			return next
		}
	}
	return append(append([]gen.Trackable(nil), items...), item)
}

func removeTrackableByID(items []gen.Trackable, id openapi_types.UUID) []gen.Trackable {
	next := make([]gen.Trackable, 0, len(items))
	for _, item := range items {
		if item.Id != id {
			next = append(next, item)
		}
	}
	return next
}

func upsertByProviderID(items []gen.LocationProvider, item gen.LocationProvider) []gen.LocationProvider {
	for i := range items {
		if items[i].Id == item.Id {
			next := append([]gen.LocationProvider(nil), items...)
			next[i] = item
			return next
		}
	}
	return append(append([]gen.LocationProvider(nil), items...), item)
}

func removeProviderByID(items []gen.LocationProvider, id string) []gen.LocationProvider {
	next := make([]gen.LocationProvider, 0, len(items))
	for _, item := range items {
		if item.Id != id {
			next = append(next, item)
		}
	}
	return next
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneZoneMap(in map[string]gen.Zone) map[string]gen.Zone {
	out := make(map[string]gen.Zone, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneFenceMap(in map[string]gen.Fence) map[string]gen.Fence {
	out := make(map[string]gen.Fence, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func buildFenceIndexes(fences []gen.Fence) map[string]*fenceSpatialIndex {
	indexes := make(map[string]*fenceSpatialIndex)
	for _, fence := range fences {
		scopeKey, ok := fenceScopeKey(fence)
		if !ok {
			continue
		}
		rect, ok := fenceBoundingRect(fence)
		if !ok {
			continue
		}
		index := indexes[scopeKey]
		if index == nil {
			index = &fenceSpatialIndex{
				tree:    rtreego.NewTree(fenceIndexDimensions, fenceIndexMinEntries, fenceIndexMaxEntries),
				entries: make(map[string]indexedFence),
			}
			indexes[scopeKey] = index
		}
		entry := indexedFence{fence: fence, rect: rect}
		index.entries[fence.Id.String()] = entry
		index.tree.Insert(entry)
	}
	return indexes
}

func (s metadataSnapshot) fenceCandidates(location gen.Location) ([]gen.Fence, error) {
	scopeKey, ok, err := s.locationFenceScopeKey(location)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	index := s.fenceIndexes[scopeKey]
	if index == nil || index.tree == nil {
		return nil, nil
	}
	point, err := point2D(location.Position)
	if err != nil {
		return nil, nil
	}
	results := index.tree.SearchIntersect(rtreego.Point{point[0], point[1]}.ToRect(0))
	fences := make([]gen.Fence, 0, len(results))
	for _, spatial := range results {
		entry, ok := spatial.(indexedFence)
		if !ok {
			continue
		}
		fences = append(fences, entry.fence)
	}
	return fences, nil
}

func (s metadataSnapshot) locationFenceScopeKey(location gen.Location) (string, bool, error) {
	crs := strings.TrimSpace(locationCRS(location))
	if crs == "" {
		zone, ok := s.zonesByID[location.Source]
		if !ok {
			zone, ok = s.zonesByForeignID[location.Source]
		}
		if ok {
			return fmt.Sprintf("local:%s", zone.Id.String()), true, nil
		}
		return "crs:EPSG:4326", true, nil
	}
	if crs == "local" {
		zone, ok := s.zonesByID[location.Source]
		if !ok {
			zone, ok = s.zonesByForeignID[location.Source]
		}
		if !ok {
			return "", false, nil
		}
		return fmt.Sprintf("local:%s", zone.Id.String()), true, nil
	}
	return "crs:" + crs, true, nil
}

func fenceScopeKey(fence gen.Fence) (string, bool) {
	crs := strings.TrimSpace(stringPtrValue(fence.Crs))
	if crs == "" {
		crs = "EPSG:4326"
	}
	if crs == "local" {
		if fence.ZoneId == nil || strings.TrimSpace(*fence.ZoneId) == "" {
			return "", false
		}
		return "local:" + strings.TrimSpace(*fence.ZoneId), true
	}
	return "crs:" + crs, true
}

func fenceBoundingRect(fence gen.Fence) (rtreego.Rect, bool) {
	if p, err := fence.Region.AsPoint(); err == nil {
		center, err := point2D(p)
		if err != nil {
			return rtreego.Rect{}, false
		}
		radius := 0.0
		if fence.Radius != nil {
			radius = float64(*fence.Radius)
		}
		rect, err := rtreego.NewRect(
			rtreego.Point{center[0] - radius, center[1] - radius},
			[]float64{radius * 2, radius * 2},
		)
		if err != nil {
			return rtreego.Rect{}, false
		}
		return rect, true
	}
	polygon, err := fence.Region.AsPolygon()
	if err != nil || len(polygon.Coordinates) == 0 || len(polygon.Coordinates[0]) == 0 {
		return rtreego.Rect{}, false
	}
	var (
		minX, minY  float64
		maxX, maxY  float64
		initialized bool
	)
	for _, pos := range polygon.Coordinates[0] {
		point, err := geoPoint(pos)
		if err != nil {
			continue
		}
		if !initialized {
			minX, maxX = point[0], point[0]
			minY, maxY = point[1], point[1]
			initialized = true
			continue
		}
		if point[0] < minX {
			minX = point[0]
		}
		if point[0] > maxX {
			maxX = point[0]
		}
		if point[1] < minY {
			minY = point[1]
		}
		if point[1] > maxY {
			maxY = point[1]
		}
	}
	if !initialized {
		return rtreego.Rect{}, false
	}
	rect, err := rtreego.NewRectFromPoints(
		rtreego.Point{minX, minY},
		rtreego.Point{maxX, maxY},
	)
	if err != nil {
		return rtreego.Rect{}, false
	}
	return rect, true
}

func cloneTrackableMap(in map[string]gen.Trackable) map[string]gen.Trackable {
	out := make(map[string]gen.Trackable, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneProviderMap(in map[string]gen.LocationProvider) map[string]gen.LocationProvider {
	out := make(map[string]gen.LocationProvider, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
