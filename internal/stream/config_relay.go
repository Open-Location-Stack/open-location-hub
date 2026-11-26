package stream

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/formation-res/open-location-hub/internal/config"
	"github.com/formation-res/open-location-hub/internal/observability"
	"github.com/formation-res/open-location-hub/internal/storage/postgres/sqlcgen"
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

const configPartSize = 256 << 10
const configPruneBatchSize = 256

// ConfigRelay publishes committed configuration revisions in database order.
type ConfigRelay struct {
	db      *pgxpool.Pool
	client  *kgo.Client
	topic   string
	hubID   string
	history time.Duration
}

// OpenConfigRelay opens a fenced producer scoped to the persisted hub identity.
func OpenConfigRelay(ctx context.Context, cfg config.KafkaShadowConfig, db *pgxpool.Pool, hubID, topic string, history time.Duration) (*ConfigRelay, error) {
	if !cfg.Enabled() || topic == "" || topic == cfg.Topic || hubID == "" || history <= 0 {
		return nil, fmt.Errorf("configuration relay requires a broker, dedicated topic, stable hub ID and positive history")
	}
	options, err := kafkaClientOptions(cfg)
	if err != nil {
		return nil, err
	}
	client, err := kgo.NewClient(append(options,
		kgo.TransactionalID("location-hub.config-relay."+hubID),
		kgo.TransactionTimeout(20*time.Second),
	)...)
	if err != nil {
		return nil, err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := requireTopic(checkCtx, client, topic); err != nil {
		client.Close()
		return nil, err
	}
	if _, err := sqlcgen.New(db).HubConfigRevision(checkCtx); err != nil {
		client.Close()
		return nil, fmt.Errorf("check configuration outbox migration: %w", err)
	}
	return &ConfigRelay{db: db, client: client, topic: topic, hubID: hubID, history: history}, nil
}

// Close closes the Kafka producer. The process that opened the database pool owns it.
func (r *ConfigRelay) Close() {
	r.client.Close()
}

// Run relays pending revisions until cancellation or a database or broker error.
func (r *ConfigRelay) Run(ctx context.Context) error {
	nextPrune := time.Now().Add(time.Minute)
	for ctx.Err() == nil {
		published, err := r.publishNext(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if now := time.Now(); !now.Before(nextPrune) {
			pruneCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			deleted, err := sqlcgen.New(r.db).PruneHubConfigRevisions(pruneCtx, sqlcgen.PruneHubConfigRevisionsParams{
				PublishedBefore: pgtype.Timestamptz{Time: now.Add(-r.history), Valid: true},
				BatchSize:       configPruneBatchSize,
			})
			cancel()
			if err != nil && ctx.Err() == nil {
				return fmt.Errorf("prune published configuration revisions: %w", err)
			}
			nextPrune = now.Add(time.Minute)
			if deleted == configPruneBatchSize {
				nextPrune = now.Add(50 * time.Millisecond)
			}
		}
		if !published {
			timer := time.NewTimer(50 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
	}
	return nil
}

func (r *ConfigRelay) publishNext(parent context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin outbox transaction: %w", err)
	}
	defer func() {
		rollbackCtx, finish := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer finish()
		_ = tx.Rollback(rollbackCtx)
	}()
	queries := sqlcgen.New(tx)
	revision, err := queries.LockNextHubConfigRevision(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock next configuration revision: %w", err)
	}
	telemetry := observability.RuntimeFromContext(parent)
	started := time.Now()
	telemetry.RecordQueueWait(ctx, "config_relay", max(0, started.Sub(revision.CreatedAt.Time)))
	rows, err := queries.ListHubConfigChanges(ctx, revision.Revision)
	if err != nil {
		return false, fmt.Errorf("load configuration revision %d: %w", revision.Revision, err)
	}
	if len(rows) == 0 {
		return false, fmt.Errorf("configuration revision %d has no changes", revision.Revision)
	}
	bundle := &streampb.ConfigurationRevision{
		SchemaVersion: 1, OriginHubId: r.hubID, Revision: revision.Revision,
		RecordedAtUnixNano: revision.CreatedAt.Time.UnixNano(),
		Changes:            make([]*streampb.ConfigurationRevision_Change, 0, len(rows)),
	}
	for _, row := range rows {
		bundle.Changes = append(bundle.Changes, &streampb.ConfigurationRevision_Change{
			ResourceKind: row.ResourceKind, Operation: row.Operation, PreviousJson: row.Previous, CurrentJson: row.Current,
		})
	}
	records, err := configRecords(r.topic, bundle, configPartSize)
	telemetry.RecordStreamServiceTime(ctx, "config_relay", "bundle", "none", "shadow", time.Since(started))
	if err != nil {
		return false, fmt.Errorf("encode configuration revision %d: %w", revision.Revision, err)
	}
	publishStart := time.Now()
	if err := r.publishRecords(ctx, records); err != nil {
		telemetry.RecordStreamHandled(ctx, "config_relay", "publish", "none", "shadow", "failed")
		return false, fmt.Errorf("publish configuration revision %d: %w", revision.Revision, err)
	}
	telemetry.RecordProcessingDuration(ctx, "config_relay_commit", "configuration", time.Since(publishStart))
	if err := queries.MarkHubConfigPublished(ctx, revision.Revision); err != nil {
		return false, fmt.Errorf("mark configuration revision %d published: %w", revision.Revision, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit outbox revision %d: %w", revision.Revision, err)
	}
	telemetry.RecordStreamHandled(ctx, "config_relay", "publish", "none", "shadow", "committed")
	return true, nil
}

func (r *ConfigRelay) publishRecords(ctx context.Context, records []*kgo.Record) error {
	if err := r.client.BeginTransaction(); err != nil {
		return err
	}
	if err := r.client.ProduceSync(ctx, records...).FirstErr(); err != nil {
		abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return errors.Join(err, r.client.EndTransaction(abortCtx, kgo.TryAbort))
	}
	return r.client.EndTransaction(ctx, kgo.TryCommit)
}

func configRecords(topic string, bundle *streampb.ConfigurationRevision, partSize int) ([]*kgo.Record, error) {
	payload, err := proto.Marshal(bundle)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(payload)
	key := []byte(bundle.OriginHubId)
	count := (len(payload) + partSize - 1) / partSize
	records := make([]*kgo.Record, 0, count)
	for position := 0; position < len(payload); position += partSize {
		part := &streampb.ConfigurationRevisionPart{
			SchemaVersion: 1, OriginHubId: bundle.OriginHubId, Revision: bundle.Revision,
			PartIndex: uint32(len(records)), PartCount: uint32(count), BundleSha256: digest[:],
			Payload: payload[position:min(position+partSize, len(payload))],
		}
		value, err := proto.Marshal(part)
		if err != nil {
			return nil, err
		}
		records = append(records, &kgo.Record{Topic: topic, Key: key, Value: value})
	}
	return records, nil
}
