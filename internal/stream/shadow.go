package stream

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"github.com/formation-res/open-location-hub/internal/config"
	"github.com/formation-res/open-location-hub/internal/observability"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

// ShadowWriter mirrors ingest into Kafka while the existing Hub owns decisions.
type ShadowWriter struct {
	client *kgo.Client
	hubID  string
	topic  string
}

// OpenShadow verifies that the configured Kafka topic already exists and opens the mirror.
func OpenShadow(ctx context.Context, cfg config.KafkaShadowConfig, hubID string) (*ShadowWriter, error) {
	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerLinger(5 * time.Millisecond),
		kgo.MaxBufferedRecords(16_384),
		kgo.MaxBufferedBytes(32 << 20),
		kgo.RecordDeliveryTimeout(15 * time.Second),
	}
	if !cfg.InsecureLocal {
		tlsConfig, err := brokerTLS(cfg)
		if err != nil {
			return nil, err
		}
		opts = append(opts, kgo.DialTLSConfig(tlsConfig))
	}
	if cfg.Username != "" {
		opts = append(opts, kgo.SASL(scram.Auth{User: cfg.Username, Pass: cfg.Password}.AsSha256Mechanism()))
	}
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := requireTopic(ctx, client, cfg.Topic); err != nil {
		client.Close()
		return nil, err
	}
	return &ShadowWriter{client: client, hubID: hubID, topic: cfg.Topic}, nil
}

// Mirror serializes a validated input and queues a bounded, asynchronous shadow
// write. Its outcome is recorded separately from the transport acknowledgement.
func (w *ShadowWriter) Mirror(ctx context.Context, observation Observation) {
	receivedAt := time.Now()
	transport := observability.IngestTransportFromContext(ctx)
	operation := "location"
	if observation.Kind == Proximity {
		operation = "proximity"
	}
	metricCtx := context.WithoutCancel(ctx)
	telemetry := observability.Global()
	key, value, err := observation.encode(w.hubID, transport, receivedAt)
	telemetry.RecordStreamServiceTime(metricCtx, "shadow_ingress", operation, "none", "shadow", time.Since(receivedAt))
	if err != nil {
		telemetry.RecordStreamHandled(metricCtx, "shadow_ingress", operation, "none", "shadow", "encode_failed")
		return
	}

	w.client.TryProduce(metricCtx, &kgo.Record{Topic: w.topic, Key: key, Value: value}, func(_ *kgo.Record, err error) {
		outcome := "committed"
		if err != nil {
			outcome = "append_failed"
		}
		telemetry.RecordStreamHandled(metricCtx, "shadow_ingress", operation, "none", "shadow", outcome)
		telemetry.RecordProcessingDuration(metricCtx, "kafka_shadow_append", operation, time.Since(receivedAt))
	})
}

// Close waits briefly for buffered shadow records before releasing the producer.
func (w *ShadowWriter) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer w.client.Close()
	return w.client.Flush(ctx)
}

func requireTopic(ctx context.Context, client *kgo.Client, topic string) error {
	request := kmsg.NewPtrMetadataRequest()
	request.Topics = []kmsg.MetadataRequestTopic{{Topic: &topic}}
	response, err := request.RequestWith(ctx, client)
	if err != nil {
		return fmt.Errorf("read Kafka observation topic metadata: %w", err)
	}
	if len(response.Topics) != 1 {
		return fmt.Errorf("cannot find observation topic %q", topic)
	}
	if err := kerr.ErrorForCode(response.Topics[0].ErrorCode); err != nil {
		return fmt.Errorf("verify observation topic %q: %w", topic, err)
	}
	if len(response.Topics[0].Partitions) == 0 {
		return fmt.Errorf("observation topic %q has no partitions", topic)
	}
	return nil
}

func brokerTLS(cfg config.KafkaShadowConfig) (*tls.Config, error) {
	brokerConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.CAFile != "" {
		data, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read Kafka CA: %w", err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("no PEM certificates found in Kafka CA file")
		}
		brokerConfig.RootCAs = roots
	}
	if cfg.ClientCertFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.ClientCertFile, cfg.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load Kafka client certificate: %w", err)
		}
		brokerConfig.Certificates = []tls.Certificate{cert}
	}
	return brokerConfig, nil
}
