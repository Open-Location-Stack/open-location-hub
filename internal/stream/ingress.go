package stream

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/formation-res/open-location-hub/internal/config"
	"github.com/formation-res/open-location-hub/internal/observability"
	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
)

const ingressConcurrency = 4
const ingressTimeout = 5 * time.Second

var errIngressBusy = errors.New("ingress queue is full")

type ingressBatch struct {
	records []*kgo.Record
	result  chan error
	queued  time.Time
	expires time.Time
}

// IngressWriter acknowledges batches after Kafka commits them.
type IngressWriter struct {
	ctx     context.Context
	cancel  context.CancelFunc
	done    sync.WaitGroup
	topic   string
	hubID   string
	batches chan ingressBatch
}

// OpenIngress verifies the observation topic and starts bounded producers.
func OpenIngress(ctx context.Context, cfg config.KafkaShadowConfig, hubID string) (*IngressWriter, error) {
	if !cfg.Enabled() || hubID == "" {
		return nil, fmt.Errorf("ingress requires Kafka brokers and a hub identity")
	}
	opts, err := kafkaClientOptions(cfg)
	if err != nil {
		return nil, err
	}
	probe, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	check, cancel := context.WithTimeout(ctx, ingressTimeout)
	err = requireTopic(check, probe, cfg.Topic)
	cancel()
	probe.Close()
	if err != nil {
		return nil, err
	}
	ctx, stop := context.WithCancel(ctx)
	w := &IngressWriter{
		ctx: ctx, cancel: stop, topic: cfg.Topic, hubID: hubID,
		batches: make(chan ingressBatch, 32),
	}
	instance := uuid.NewString()
	for lane := range ingressConcurrency {
		laneOptions := append(append([]kgo.Opt(nil), opts...),
			kgo.ProducerLinger(0),
			kgo.TransactionalID(fmt.Sprintf("location-hub.ingress.%s.%s.%d", hubID, instance, lane)),
			kgo.TransactionTimeout(15*time.Second),
		)
		w.done.Go(func() { w.runProducer(laneOptions) })
	}
	return w, nil
}

// Append validates encoding for the entire batch before handing it to Kafka.
func (w *IngressWriter) Append(ctx context.Context, observations []Observation) error {
	if len(observations) == 0 {
		return nil
	}
	wait, cancel := context.WithTimeout(ctx, ingressTimeout)
	defer cancel()
	expires, _ := wait.Deadline()
	batch := ingressBatch{records: make([]*kgo.Record, 0, len(observations)), result: make(chan error, 1), expires: expires}
	receivedAt := time.Now()
	transport := observability.IngestTransportFromContext(ctx)
	for _, observation := range observations {
		key, value, err := encodeObservation(observation, w.hubID, transport, receivedAt)
		if err != nil {
			return err
		}
		batch.records = append(batch.records, &kgo.Record{Topic: w.topic, Key: key, Value: value})
	}
	telemetry := observability.RuntimeFromContext(w.ctx)
	telemetry.RecordProcessingDuration(ctx, "ingress_encode", transport, time.Since(receivedAt))
	batch.queued = time.Now()
	select {
	case <-w.ctx.Done():
		return w.ctx.Err()
	case <-wait.Done():
		return wait.Err()
	case w.batches <- batch:
	default:
		telemetry.RecordStreamHandled(ctx, "kafka_ingress", transport, "none", "stream", "overloaded")
		return errIngressBusy
	}
	select {
	case err := <-batch.result:
		return err
	case <-w.ctx.Done():
		return w.ctx.Err()
	case <-wait.Done():
		return wait.Err()
	}
}

func (w *IngressWriter) runProducer(options []kgo.Opt) {
	var client *kgo.Client
	defer func() {
		if client != nil {
			client.Close()
		}
	}()
	telemetry := observability.RuntimeFromContext(w.ctx)
	for w.ctx.Err() == nil {
		var batch ingressBatch
		select {
		case <-w.ctx.Done():
			return
		case batch = <-w.batches:
		}
		start := time.Now()
		telemetry.RecordQueueWait(w.ctx, "kafka_ingress", start.Sub(batch.queued))
		if !start.Before(batch.expires) {
			for range batch.records {
				telemetry.RecordStreamHandled(w.ctx, "kafka_ingress", "observation", "none", "stream", "queue_expired")
			}
			batch.result <- context.DeadlineExceeded
			continue
		}
		var err error
		if client == nil {
			client, err = kgo.NewClient(options...)
		}
		if err == nil {
			err = w.commit(client, batch.records, batch.expires)
		}
		outcome := "committed"
		if err != nil {
			outcome = "unconfirmed"
			if client != nil {
				client.Close()
				client = nil
			}
		}
		telemetry.RecordProcessingDuration(w.ctx, "kafka_ingress_commit", "batch", time.Since(start))
		for range batch.records {
			telemetry.RecordStreamHandled(w.ctx, "kafka_ingress", "observation", "none", "stream", outcome)
		}
		batch.result <- err
	}
}

func (w *IngressWriter) commit(client *kgo.Client, records []*kgo.Record, expires time.Time) error {
	if err := client.BeginTransaction(); err != nil {
		return fmt.Errorf("begin ingress transaction: %w", err)
	}
	ctx, cancel := context.WithDeadline(w.ctx, expires)
	defer cancel()
	if err := client.ProduceSync(ctx, records...).FirstErr(); err != nil {
		abort, finish := context.WithTimeout(context.WithoutCancel(w.ctx), time.Second)
		defer finish()
		if abortErr := client.AbortBufferedRecords(abort); abortErr == nil {
			_ = client.EndTransaction(abort, kgo.TryAbort)
		}
		return fmt.Errorf("produce ingress transaction: %w", err)
	}
	if err := client.EndTransaction(ctx, kgo.TryCommit); err != nil {
		return fmt.Errorf("confirm ingress transaction: %w", err)
	}
	return nil
}

// Close stops the producers and releases their Kafka connections.
func (w *IngressWriter) Close() {
	w.cancel()
	w.done.Wait()
}
