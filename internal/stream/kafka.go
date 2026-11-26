package stream

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"github.com/formation-res/open-location-hub/internal/config"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

func kafkaClientOptions(cfg config.KafkaShadowConfig) ([]kgo.Opt, error) {
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
		credentials := scram.Auth{User: cfg.Username, Pass: cfg.Password}
		mechanism := credentials.AsSha256Mechanism()
		if cfg.SCRAMMechanism == "SCRAM-SHA-512" {
			mechanism = credentials.AsSha512Mechanism()
		}
		opts = append(opts, kgo.SASL(mechanism))
	}
	return opts, nil
}

func requireTopic(ctx context.Context, client *kgo.Client, topic string) error {
	_, err := topicMetadata(ctx, client, topic)
	return err
}

func topicMetadata(ctx context.Context, client *kgo.Client, topic string) (kmsg.MetadataResponseTopic, error) {
	request := kmsg.NewPtrMetadataRequest()
	request.Topics = []kmsg.MetadataRequestTopic{{Topic: &topic}}
	response, err := request.RequestWith(ctx, client)
	if err != nil {
		return kmsg.MetadataResponseTopic{}, fmt.Errorf("read Kafka topic metadata for %q: %w", topic, err)
	}
	if len(response.Topics) != 1 {
		return kmsg.MetadataResponseTopic{}, fmt.Errorf("cannot find Kafka topic %q", topic)
	}
	result := response.Topics[0]
	if err := kerr.ErrorForCode(result.ErrorCode); err != nil {
		return kmsg.MetadataResponseTopic{}, fmt.Errorf("verify Kafka topic %q: %w", topic, err)
	}
	if len(result.Partitions) == 0 {
		return kmsg.MetadataResponseTopic{}, fmt.Errorf("topic %q has no partitions", topic)
	}
	return result, nil
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
