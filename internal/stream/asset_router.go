package stream

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/formation-res/open-location-hub/internal/config"
	"github.com/formation-res/open-location-hub/internal/observability"
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

// AssetRouter consumes validated observations and partitions associated inputs by asset.
type AssetRouter struct {
	session     *kgo.GroupTransactSession
	outputTopic string
}

// OpenAssetRouter joins the router group using a transaction namespace unique to this instance.
func OpenAssetRouter(ctx context.Context, cfg config.KafkaShadowConfig, outputTopic, group, instance string) (*AssetRouter, error) {
	if !cfg.Enabled() || outputTopic == "" || outputTopic == cfg.Topic || group == "" || instance == "" {
		return nil, fmt.Errorf("asset routing requires separate Kafka input and output topics, a group and an instance ID")
	}
	opts, err := kafkaClientOptions(cfg)
	if err != nil {
		return nil, err
	}
	opts = append(opts,
		kgo.ConsumeTopics(cfg.Topic),
		kgo.ConsumerGroup(group),
		kgo.ConsumeStartOffset(kgo.NewOffset().AtStart()),
		kgo.ConsumeResetOffset(kgo.NoResetOffset()),
		kgo.FetchIsolationLevel(kgo.ReadCommitted()),
		kgo.TransactionalID(group+"."+instance),
		kgo.TransactionTimeout(20*time.Second),
	)
	session, err := kgo.NewGroupTransactSession(opts...)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, topic := range []string{cfg.Topic, outputTopic} {
		if err := requireTopic(ctx, session.Client(), topic); err != nil {
			session.Close()
			return nil, err
		}
	}
	return &AssetRouter{session: session, outputTopic: outputTopic}, nil
}

// Close leaves the consumer group and frees the Kafka connection.
func (r *AssetRouter) Close() {
	r.session.Close()
}

// Run processes committed observations until shutdown or an unrecoverable gap.
func (r *AssetRouter) Run(ctx context.Context) error {
	telemetry := observability.RuntimeFromContext(ctx)
	for ctx.Err() == nil {
		fetches := r.session.PollRecords(ctx, 256)
		if ctx.Err() != nil {
			return nil
		}
		if err := fetches.Err(); err != nil {
			return fmt.Errorf("fetch asset-router input: %w", err)
		}
		n := fetches.NumRecords()
		if n == 0 {
			continue
		}

		outputs := make([]*kgo.Record, 0, n)
		unassociated := 0
		for record := range fetches.RecordsAll() {
			start := time.Now()
			telemetry.RecordQueueWait(ctx, "asset_router", max(0, start.Sub(record.Timestamp)))
			expanded, err := assetRecords(record.Value, r.outputTopic)
			telemetry.RecordStreamServiceTime(ctx, "asset_router", "repartition", "none", "shadow", time.Since(start))
			if err != nil {
				return fmt.Errorf("route %s partition %d offset %d: %w", record.Topic, record.Partition, record.Offset, err)
			}
			if len(expanded) == 0 {
				unassociated++
			}
			outputs = append(outputs, expanded...)
		}
		commitStarted := time.Now()
		committed, err := r.commitBatch(ctx, outputs)
		telemetry.RecordProcessingDuration(ctx, "asset_router_commit", "asset_input", time.Since(commitStarted))
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		outcome := "committed"
		if !committed {
			outcome = "rebalanced"
			unassociated = 0
		}
		for range n - unassociated {
			telemetry.RecordStreamHandled(ctx, "asset_router", "repartition", "none", "shadow", outcome)
		}
		for range unassociated {
			telemetry.RecordStreamHandled(ctx, "asset_router", "repartition", "none", "shadow", "unassociated")
		}
	}
	return nil
}

func (r *AssetRouter) commitBatch(ctx context.Context, outputs []*kgo.Record) (bool, error) {
	if err := r.session.Begin(); err != nil {
		return false, fmt.Errorf("begin asset-router transaction: %w", err)
	}
	ioCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := r.session.ProduceSync(ioCtx, outputs...).FirstErr(); err != nil {
		abortCtx, finishAbort := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer finishAbort()
		_, abortErr := r.session.End(abortCtx, kgo.TryAbort)
		if abortErr != nil {
			return false, fmt.Errorf("produce asset-router batch: %w; abort: %w", err, abortErr)
		}
		return false, fmt.Errorf("produce asset-router batch: %w", err)
	}
	committed, err := r.session.End(ioCtx, kgo.TryCommit)
	if err != nil {
		return false, fmt.Errorf("commit asset-router batch: %w", err)
	}
	return committed, nil
}

func assetRecords(value []byte, outputTopic string) ([]*kgo.Record, error) {
	var observation streampb.Observation
	if err := proto.Unmarshal(value, &observation); err != nil {
		return nil, fmt.Errorf("decode observation: %w", err)
	}
	if observation.GetSchemaVersion() != 1 || observation.GetOriginHubId() == "" || len(observation.GetEventId()) != sha256.Size {
		return nil, fmt.Errorf("invalid observation version or identity")
	}
	if observation.GetKind() != Location && observation.GetKind() != Proximity {
		return nil, fmt.Errorf("invalid observation kind")
	}

	ids := observation.GetResolvedTrackableIds()
	if len(ids) == 0 && observation.GetKind() == Location {
		var location struct {
			Trackables []string `json:"trackables"`
		}
		if err := json.Unmarshal(observation.GetPayloadJson(), &location); err != nil {
			return nil, fmt.Errorf("decode location trackables: %w", err)
		}
		ids = location.Trackables
	}
	seen := make(map[string]struct{}, len(ids))
	outputs := make([]*kgo.Record, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, emitted := seen[id]; emitted {
			continue
		}
		seen[id] = struct{}{}
		key := appendKeyPart(nil, observation.GetOriginHubId())
		key = appendKeyPart(key, id)
		eventID := sha256.New()
		_, _ = eventID.Write([]byte("asset-input:v1"))
		_, _ = eventID.Write(observation.GetEventId())
		_, _ = eventID.Write(key)
		payload, err := proto.Marshal(&streampb.AssetInput{
			SchemaVersion: 1, EventId: eventID.Sum(nil), TrackableId: id, Observation: &observation,
		})
		if err != nil {
			return nil, fmt.Errorf("encode asset input: %w", err)
		}
		outputs = append(outputs, &kgo.Record{Topic: outputTopic, Key: key, Value: payload})
	}
	return outputs, nil
}
