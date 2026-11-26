package stream

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"hash/maphash"
	"maps"
	"sync/atomic"
	"time"

	"github.com/formation-res/open-location-hub/internal/config"
	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/locating"
	"github.com/formation-res/open-location-hub/internal/observability"
	"github.com/formation-res/open-location-hub/internal/storage/postgres/sqlcgen"
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"google.golang.org/protobuf/proto"
)

const configMaxParts = 128
const configCacheShards = 64

var configCacheHash = maphash.MakeSeed()

// ConfigurationWatch activates complete committed revisions for one local
// estimator replica. Work batches load one immutable snapshot for decisions.
type ConfigurationWatch struct {
	cache          atomic.Pointer[locatingConfiguration]
	snapshotCutoff int64
	lastDigest     [sha256.Size]byte
	assembly       configAssembly
	topic          string
	hubID          string
	db             *pgxpool.Pool
	client         *kgo.Client
	maxStale       time.Duration
}

type locatingConfiguration struct {
	revision int64
	shards   [configCacheShards]locatingConfigurationShard
	geometry *fenceConfiguration
}

type locatingConfigurationShard struct {
	rules      map[string]locating.Rules
	providers  map[string]gen.LocationProvider
	trackables map[string]gen.Trackable
}

func configurationShard(id string) int {
	return int(maphash.String(configCacheHash, id) % configCacheShards)
}

func newLocatingConfiguration(revision int64) *locatingConfiguration {
	snapshot := &locatingConfiguration{revision: revision}
	for i := range snapshot.shards {
		snapshot.shards[i] = locatingConfigurationShard{
			rules: make(map[string]locating.Rules), providers: make(map[string]gen.LocationProvider),
		}
	}
	return snapshot
}

type configAssembly struct {
	revision int64
	next     uint32
	count    uint32
	digest   [sha256.Size]byte
	payload  []byte
}

// OpenConfigurationWatch captures committed Kafka offsets before taking the
// PostgreSQL snapshot, so revisions committed after the snapshot stay in view.
func OpenConfigurationWatch(ctx context.Context, cfg config.KafkaShadowConfig, db *pgxpool.Pool, topic, hubID string, maxStale time.Duration, spatial bool) (*ConfigurationWatch, error) {
	if !cfg.Enabled() || db == nil || topic == "" || hubID == "" || maxStale < time.Second {
		return nil, fmt.Errorf("configuration watch requires Kafka, PostgreSQL, a topic, a Hub ID and at least one second of allowed staleness")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	options, err := kafkaClientOptions(cfg)
	if err != nil {
		return nil, err
	}
	offsets, err := configurationOffsets(ctx, options, topic)
	if err != nil {
		return nil, err
	}
	snapshot, err := loadLocatingConfiguration(ctx, db, hubID, spatial)
	if err != nil {
		return nil, fmt.Errorf("load locating configuration snapshot: %w", err)
	}
	client, err := kgo.NewClient(append(options,
		kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{topic: offsets}),
		kgo.FetchIsolationLevel(kgo.ReadCommitted()), kgo.ConsumeResetOffset(kgo.NoResetOffset()),
	)...)
	if err != nil {
		return nil, err
	}
	watch := &ConfigurationWatch{
		snapshotCutoff: snapshot.revision, topic: topic, hubID: hubID,
		db: db, client: client, maxStale: maxStale,
	}
	watch.cache.Store(snapshot)
	return watch, nil
}

func configurationOffsets(ctx context.Context, options []kgo.Opt, topic string) (map[int32]kgo.Offset, error) {
	return committedOffsets(ctx, options, topic, -1)
}

func committedOffsets(ctx context.Context, options []kgo.Opt, topic string, timestamp int64) (map[int32]kgo.Offset, error) {
	client, err := kgo.NewClient(options...)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	metadata, err := topicMetadata(ctx, client, topic)
	if err != nil {
		return nil, err
	}
	partitions := make([]kmsg.ListOffsetsRequestTopicPartition, 0, len(metadata.Partitions))
	for _, partition := range metadata.Partitions {
		partitions = append(partitions, kmsg.ListOffsetsRequestTopicPartition{
			Partition: partition.Partition, Timestamp: timestamp, CurrentLeaderEpoch: -1,
		})
	}
	request := kmsg.NewPtrListOffsetsRequest()
	request.IsolationLevel = 1
	request.Topics = []kmsg.ListOffsetsRequestTopic{{Topic: topic, Partitions: partitions}}
	offsets := make(map[int32]kgo.Offset, len(partitions))
	for _, shard := range client.RequestSharded(ctx, request) {
		if shard.Err != nil {
			return nil, fmt.Errorf("list offsets for %q: %w", topic, shard.Err)
		}
		for _, result := range shard.Resp.(*kmsg.ListOffsetsResponse).Topics {
			for _, partition := range result.Partitions {
				if err := kerr.ErrorForCode(partition.ErrorCode); err != nil {
					return nil, fmt.Errorf("list offset for %q partition %d: %w", topic, partition.Partition, err)
				}
				if partition.Offset < 0 && timestamp < 0 {
					return nil, fmt.Errorf("missing committed offset for %q partition %d", topic, partition.Partition)
				}
				offsets[partition.Partition] = kgo.NewOffset().At(partition.Offset)
			}
		}
	}
	if len(offsets) == 0 || len(offsets) != len(partitions) {
		return nil, fmt.Errorf("incomplete offset frontier for %q", topic)
	}
	return offsets, nil
}

func loadLocatingConfiguration(ctx context.Context, db *pgxpool.Pool, hubID string, spatial bool) (*locatingConfiguration, error) {
	snapshot := newLocatingConfiguration(0)
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		hub, err := q.GetHubMetadata(ctx)
		if err != nil {
			return err
		}
		if hub.HubID.String() != hubID {
			return fmt.Errorf("configuration database belongs to a different Hub")
		}
		snapshot.revision, err = q.HubConfigRevision(ctx)
		if err != nil {
			return err
		}
		trackables, err := q.ListTrackables(ctx)
		if err != nil {
			return err
		}
		for _, row := range trackables {
			trackable, rules, err := decodeTrackable(row.Payload)
			if err != nil {
				return fmt.Errorf("compile trackable %s: %w", row.ID.String(), err)
			}
			id := row.ID.String()
			shard := &snapshot.shards[configurationShard(id)]
			shard.rules[id] = rules
			if spatial {
				if shard.trackables == nil {
					shard.trackables = make(map[string]gen.Trackable)
				}
				shard.trackables[id] = trackable
			}
		}
		providers, err := q.ListProviders(ctx)
		if err != nil {
			return err
		}
		for _, row := range providers {
			provider := providerForLocating(row.ID, row.Type, row.Name)
			if spatial {
				if err := json.Unmarshal(row.Payload, &provider); err != nil {
					return fmt.Errorf("decode provider %s: %w", row.ID, err)
				}
			}
			snapshot.shards[configurationShard(row.ID)].providers[row.ID] = provider
		}
		if spatial {
			snapshot.geometry, err = loadFenceConfiguration(ctx, q)
			return err
		}
		return nil
	})
	return snapshot, err
}

func decodeTrackable(payload []byte) (gen.Trackable, locating.Rules, error) {
	var trackable gen.Trackable
	if err := json.Unmarshal(payload, &trackable); err != nil {
		return trackable, nil, err
	}
	rules, err := locating.Compile(trackable.LocatingRules)
	return trackable, rules, err
}

func providerForLocating(id, providerType string, name pgtype.Text) gen.LocationProvider {
	provider := gen.LocationProvider{Id: id, Type: providerType}
	if name.Valid {
		provider.Name = &name.String
	}
	return provider
}

// Close releases the Kafka connection.
func (w *ConfigurationWatch) Close() {
	w.client.Close()
}

// Run observes each revision independently of the load-balanced asset group.
func (w *ConfigurationWatch) Run(ctx context.Context) error {
	telemetry := observability.RuntimeFromContext(ctx)
	verifiedAt := time.Now()
	interval := min(5*time.Second, w.maxStale/4)
	nextCheck := verifiedAt.Add(interval)
	for ctx.Err() == nil {
		pollCtx, cancel := context.WithDeadline(ctx, nextCheck)
		fetches := w.client.PollRecords(pollCtx, 256)
		cancel()
		if ctx.Err() != nil {
			return nil
		}
		if err := fetches.Err(); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("consume configuration topic %q: %w", w.topic, err)
		}
		for record := range fetches.RecordsAll() {
			if err := w.applyRecord(ctx, record); err != nil {
				return fmt.Errorf("configuration partition %d offset %d: %w", record.Partition, record.Offset, err)
			}
		}
		if time.Now().Before(nextCheck) {
			continue
		}

		checkCtx, cancel := context.WithTimeout(ctx, min(3*time.Second, interval))
		head, err := sqlcgen.New(w.db).HubConfigRevision(checkCtx)
		cancel()
		if ctx.Err() != nil {
			return nil
		}
		current := w.cache.Load().revision
		switch {
		case err == nil && head < current:
			return fmt.Errorf("configuration revision regressed from %d to %d in PostgreSQL", current, head)
		case err == nil && head == current:
			verifiedAt = time.Now()
		case time.Since(verifiedAt) >= w.maxStale:
			telemetry.RecordStreamHandled(ctx, "config_activation", "freshness", "none", "shadow", "stale")
			if err != nil {
				return fmt.Errorf("configuration freshness could not be verified within %s: %w", w.maxStale, err)
			}
			return fmt.Errorf("configuration revision %d is stale, PostgreSQL is at %d", current, head)
		}
		nextCheck = time.Now().Add(interval)
	}
	return nil
}

func (w *ConfigurationWatch) applyRecord(ctx context.Context, record *kgo.Record) error {
	if string(record.Key) != w.hubID {
		return nil
	}
	part := new(streampb.ConfigurationRevisionPart)
	if err := proto.Unmarshal(record.Value, part); err != nil {
		return fmt.Errorf("decode revision fragment: %w", err)
	}
	if part.SchemaVersion != 1 || part.OriginHubId != w.hubID || part.Revision < 1 {
		return fmt.Errorf("invalid configuration fragment identity")
	}
	if part.Revision <= w.snapshotCutoff {
		return nil
	}
	current := w.cache.Load()
	if part.Revision != current.revision && part.Revision != current.revision+1 {
		return fmt.Errorf("configuration revision %d follows active revision %d", part.Revision, current.revision)
	}
	bundle, digest, err := w.assembly.append(part)
	if err != nil || bundle == nil {
		return err
	}
	if bundle.Revision == current.revision {
		if digest != w.lastDigest {
			return fmt.Errorf("conflicting payloads for configuration revision %d", current.revision)
		}
		return nil
	}
	start := time.Now()
	changes, err := decodeLocatingChanges(bundle.Changes, current.geometry != nil)
	if err != nil {
		return err
	}
	w.cache.Store(current.withChanges(bundle.Revision, changes))
	w.lastDigest = digest
	telemetry := observability.RuntimeFromContext(ctx)
	telemetry.RecordStreamServiceTime(ctx, "config_activation", "apply", "none", "shadow", time.Since(start))
	telemetry.RecordProcessingDuration(ctx, "config_activation_age", "configuration", max(0, time.Since(time.Unix(0, bundle.RecordedAtUnixNano))))
	telemetry.RecordStreamHandled(ctx, "config_activation", "apply", "none", "shadow", "activated")
	return nil
}

func (current *locatingConfiguration) withChanges(revision int64, changes []locatingChange) *locatingConfiguration {
	next := *current
	next.revision = revision
	var updatedRules, updatedProviders [configCacheShards]bool
	for _, change := range changes {
		if change.kind == "fence" || change.kind == "zone" {
			continue
		}
		index := configurationShard(change.id)
		shard := &next.shards[index]
		if change.kind == "trackable" {
			if !updatedRules[index] {
				shard.rules = maps.Clone(shard.rules)
				if next.geometry != nil {
					shard.trackables = maps.Clone(shard.trackables)
					if shard.trackables == nil {
						shard.trackables = make(map[string]gen.Trackable)
					}
				}
				updatedRules[index] = true
			}
			if change.remove {
				delete(shard.rules, change.id)
				delete(shard.trackables, change.id)
			} else {
				shard.rules[change.id] = change.rules
				if shard.trackables != nil {
					shard.trackables[change.id] = change.trackable
				}
			}
			continue
		}
		if !updatedProviders[index] {
			shard.providers = maps.Clone(shard.providers)
			updatedProviders[index] = true
		}
		if change.remove {
			delete(shard.providers, change.id)
		} else {
			shard.providers[change.id] = change.provider
		}
	}
	if next.geometry != nil {
		next.geometry = next.geometry.withChanges(changes)
	}
	return &next
}

func (a *configAssembly) append(part *streampb.ConfigurationRevisionPart) (*streampb.ConfigurationRevision, [sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	if part.PartCount == 0 || part.PartCount > configMaxParts || part.PartIndex >= part.PartCount ||
		len(part.BundleSha256) != sha256.Size || len(part.Payload) == 0 || len(part.Payload) > configPartSize {
		return nil, digest, fmt.Errorf("invalid configuration fragment dimensions")
	}
	copy(digest[:], part.BundleSha256)
	if a.next == 0 {
		a.revision, a.count, a.digest = part.Revision, part.PartCount, digest
	}
	if part.Revision != a.revision || part.PartIndex != a.next || part.PartCount != a.count || digest != a.digest {
		return nil, digest, fmt.Errorf("discontinuous fragments in configuration revision %d", part.Revision)
	}
	a.payload = append(a.payload, part.Payload...)
	a.next++
	if a.next < a.count {
		return nil, digest, nil
	}
	payload := a.payload
	*a = configAssembly{}
	if sha256.Sum256(payload) != digest {
		return nil, digest, fmt.Errorf("configuration bundle checksum mismatch")
	}
	bundle := new(streampb.ConfigurationRevision)
	if err := proto.Unmarshal(payload, bundle); err != nil {
		return nil, digest, err
	}
	if bundle.SchemaVersion != 1 || bundle.OriginHubId != part.OriginHubId || bundle.Revision != part.Revision ||
		bundle.RecordedAtUnixNano <= 0 || len(bundle.Changes) == 0 {
		return nil, digest, fmt.Errorf("configuration bundle identity or contents do not match the fragments")
	}
	return bundle, digest, nil
}

type locatingChange struct {
	kind, id  string
	remove    bool
	rules     locating.Rules
	provider  gen.LocationProvider
	trackable gen.Trackable
	zone      gen.Zone
	fence     gen.Fence
}

func decodeLocatingChanges(rows []*streampb.ConfigurationRevision_Change, spatial bool) ([]locatingChange, error) {
	changes := make([]locatingChange, 0, len(rows))
	for _, row := range rows {
		kind := row.GetResourceKind()
		if kind != "trackable" && kind != "location_provider" && !(spatial && (kind == "zone" || kind == "fence")) {
			continue
		}
		change := locatingChange{kind: kind}
		raw := row.GetCurrentJson()
		switch row.GetOperation() {
		case "INSERT", "UPDATE":
		case "DELETE":
			raw = row.GetPreviousJson()
			change.remove = true
		default:
			return nil, fmt.Errorf("unknown configuration operation for %s", kind)
		}
		var entry struct {
			ID      string          `json:"id"`
			Type    string          `json:"type"`
			Name    *string         `json:"name"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(raw, &entry); err != nil || entry.ID == "" {
			return nil, fmt.Errorf("invalid %s configuration row", kind)
		}
		change.id = entry.ID
		if !change.remove {
			switch kind {
			case "trackable":
				trackable, rules, err := decodeTrackable(entry.Payload)
				if err != nil {
					return nil, fmt.Errorf("compile trackable %s: %w", entry.ID, err)
				}
				change.rules = rules
				change.trackable = trackable
			case "location_provider":
				change.provider = gen.LocationProvider{Id: entry.ID, Type: entry.Type, Name: entry.Name}
				if spatial {
					if err := json.Unmarshal(entry.Payload, &change.provider); err != nil {
						return nil, fmt.Errorf("decode provider %s: %w", entry.ID, err)
					}
				}
			case "zone":
				if err := decodeFenceResource(entry.ID, entry.Payload, &change.zone); err != nil {
					return nil, err
				}
			case "fence":
				if err := decodeFenceResource(entry.ID, entry.Payload, &change.fence); err != nil {
					return nil, err
				}
			}
		}
		changes = append(changes, change)
	}
	return changes, nil
}
