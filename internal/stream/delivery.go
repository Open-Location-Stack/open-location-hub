package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/formation-res/open-location-hub/internal/config"
	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/hub"
	"github.com/formation-res/open-location-hub/internal/ids"
	"github.com/formation-res/open-location-hub/internal/observability"
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

// DeliveryTopics lists the source streams for the public gateway.
type DeliveryTopics struct {
	Estimates, Fences, Collisions string
	StateTTL                      time.Duration
}

// Delivery publishes committed records to the local gateway.
type Delivery struct {
	client *kgo.Client
	hubID  string
	routes map[string]string
	tails  map[string]map[int32]kgo.Offset
}

// OpenDelivery records the committed tail of each subscribed topic. Every
// gateway needs its own reader so its connected subscribers see the stream.
func OpenDelivery(ctx context.Context, cfg config.KafkaShadowConfig, hubID string, topics DeliveryTopics) (*Delivery, error) {
	options, err := kafkaClientOptions(cfg)
	if err != nil {
		return nil, err
	}
	routes := map[string]string{cfg.Topic: "observation"}
	for kind, name := range map[string]string{"estimate": topics.Estimates, "fence": topics.Fences, "collision": topics.Collisions} {
		if name == "" {
			continue
		}
		if routes[name] != "" {
			return nil, fmt.Errorf("gateway requires separate stream topics")
		}
		routes[name] = kind
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	partitions := make(map[string]map[int32]kgo.Offset, len(routes))
	tails := make(map[string]map[int32]kgo.Offset, len(routes))
	since := time.Now().Add(-topics.StateTTL).UnixMilli()
	for topic, kind := range routes {
		offsets, err := configurationOffsets(ctx, options, topic)
		if err != nil {
			return nil, fmt.Errorf("find committed gateway offsets for %s: %w", topic, err)
		}
		partitions[topic] = offsets
		tails[topic] = offsets
		if topics.StateTTL > 0 && (kind == "observation" || kind == "estimate") {
			recent, err := committedOffsets(ctx, options, topic, since)
			if err != nil {
				return nil, fmt.Errorf("find recent state offsets for %s: %w", topic, err)
			}
			for partition, offset := range recent {
				tail, exists := offsets[partition]
				if !exists {
					return nil, fmt.Errorf("gateway topic %q changed partitions during startup", topic)
				}
				// A timestamp query can miss a partition with no recent data or
				// see a new append after the tail was sampled.
				if at := offset.EpochOffset().Offset; at < 0 || at > tail.EpochOffset().Offset {
					recent[partition] = tail
				}
			}
			if len(recent) != len(offsets) {
				return nil, fmt.Errorf("gateway topic %q changed partitions during startup", topic)
			}
			partitions[topic] = recent
		}
	}
	client, err := kgo.NewClient(append(options,
		kgo.ConsumePartitions(partitions), kgo.ConsumeResetOffset(kgo.NoResetOffset()),
		kgo.FetchIsolationLevel(kgo.ReadCommitted()),
	)...)
	if err != nil {
		return nil, err
	}
	return &Delivery{client: client, hubID: hubID, routes: routes, tails: tails}, nil
}

// Close releases the Kafka reader.
func (d *Delivery) Close() { d.client.Close() }

// Run applies committed input and decision records to the gateway.
func (d *Delivery) Run(ctx context.Context, service *hub.Service) error {
	telemetry := observability.RuntimeFromContext(ctx)
	for ctx.Err() == nil {
		fetches := d.client.PollRecords(ctx, 256)
		if ctx.Err() != nil {
			return nil
		}
		if err := fetches.Err(); err != nil {
			return err
		}
		for record := range fetches.RecordsAll() {
			stage := d.routes[record.Topic]
			start := time.Now()
			publish := record.Offset >= d.tails[record.Topic][record.Partition].EpochOffset().Offset
			worker, outcome := "stream_delivery", "delivered"
			if publish {
				telemetry.RecordQueueWait(ctx, worker, max(0, start.Sub(record.Timestamp)))
			} else {
				worker, outcome = "stream_projection_restore", "restored"
			}
			if err := d.deliver(ctx, service, stage, record, publish); err != nil {
				return fmt.Errorf("deliver %s partition %d offset %d: %w", record.Topic, record.Partition, record.Offset, err)
			}
			if publish {
				telemetry.RecordEndToEnd(ctx, stage, "kafka_append_to_gateway", time.Since(record.Timestamp))
			}
			telemetry.RecordStreamServiceTime(ctx, worker, stage, "none", "stream", time.Since(start))
			telemetry.RecordStreamHandled(ctx, worker, stage, "none", "stream", outcome)
		}
	}
	return nil
}

func (d *Delivery) deliver(ctx context.Context, service *hub.Service, stage string, record *kgo.Record, publish bool) error {
	switch stage {
	case "observation":
		var input streampb.Observation
		if err := proto.Unmarshal(record.Value, &input); err != nil {
			return err
		}
		if input.GetOriginHubId() != d.hubID {
			return nil
		}
		location, err := decodeObservationLocation(&input)
		if err != nil {
			return err
		}
		if input.Kind == Proximity && publish {
			var proximity gen.Proximity
			if err := json.Unmarshal(input.PayloadJson, &proximity); err != nil {
				return err
			}
			if err := service.ApplyStreamProximity(proximity); err != nil {
				return err
			}
		}
		return service.ApplyStreamLocation(ctx, location, record.Timestamp, input.Kind == Proximity, publish)
	case "estimate":
		var estimate streampb.MotionEstimate
		if err := proto.Unmarshal(record.Value, &estimate); err != nil {
			return err
		}
		if estimate.GetOriginHubId() != d.hubID {
			return nil
		}
		var location gen.Location
		if err := json.Unmarshal(estimate.LocationJson, &location); err != nil {
			return err
		}
		return service.ApplyStreamMotion(ctx, estimate.TrackableId, location, record.Timestamp, publish)
	case "fence":
		var transition streampb.FenceTransition
		if err := proto.Unmarshal(record.Value, &transition); err != nil {
			return err
		}
		source := transition.GetObservation()
		if source.GetOriginHubId() != d.hubID {
			return nil
		}
		if len(transition.EventId) != 32 {
			return fmt.Errorf("invalid fence transition identity")
		}
		var fence gen.Fence
		if err := json.Unmarshal(source.FenceJson, &fence); err != nil {
			return err
		}
		var location gen.Location
		if err := json.Unmarshal(source.LocationJson, &location); err != nil {
			return err
		}
		at := time.Unix(0, transition.OccurredAtUnixNano).UTC()
		event := gen.FenceEvent{
			Id: ids.EventUUID(at, [32]byte(transition.EventId)), FenceId: fence.Id, EventType: gen.RegionEntry,
			ProviderId: &location.ProviderId, TrackableId: &source.TrackableId, ForeignId: fence.ForeignId,
			Properties: fence.Properties, EntryTime: protoTime(transition.EnteredAtUnixNano),
		}
		if transition.Kind == streampb.FenceTransition_KIND_EXIT {
			event.EventType, event.ExitTime = gen.RegionExit, &at
		}
		return service.ApplyStreamFence(ctx, fence, event, location)
	case "collision":
		var transition streampb.CollisionTransition
		if err := proto.Unmarshal(record.Value, &transition); err != nil {
			return err
		}
		leading := transition.GetLeft()
		if leading.GetOriginHubId() != d.hubID {
			return nil
		}
		var event gen.CollisionEvent
		if err := json.Unmarshal(transition.CollisionEventJson, &event); err != nil {
			return err
		}
		if len(event.Collisions) > 0 && event.Collisions[0].Id.String() == transition.GetRight().GetTrackableId() {
			leading = transition.GetRight()
		}
		var location gen.Location
		if err := json.Unmarshal(leading.LocationJson, &location); err != nil {
			return err
		}
		return service.ApplyStreamCollision(event, gen.TrackableMotion{Id: leading.TrackableId, Location: location})
	default:
		return fmt.Errorf("unknown gateway stage %q", stage)
	}
}

func protoTime(nanos int64) *time.Time {
	value := time.Unix(0, nanos).UTC()
	return &value
}
