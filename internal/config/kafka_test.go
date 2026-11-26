package config

import "testing"

func TestKafkaShadowValidatesTransportAndCredentials(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name       string
		broker     string
		insecure   string
		cert       string
		user, pass string
		mechanism  string
		wantError  bool
	}{
		{name: "disabled"},
		{name: "TLS", broker: "kafka.example.test:9093"},
		{name: "local plaintext", broker: "kafka:9092", insecure: "true"},
		{name: "TLS with SCRAM", broker: "kafka.example.test:9093", user: "hub", pass: "fixture"},
		{name: "TLS with Strimzi SCRAM", broker: "kafka.example.test:9093", user: "hub", pass: "fixture", mechanism: "SCRAM-SHA-512"},
		{name: "unknown SCRAM mechanism", broker: "kafka:9093", user: "hub", pass: "fixture", mechanism: "PLAIN", wantError: true},
		{name: "SCRAM mechanism requires credentials", broker: "kafka:9093", mechanism: "SCRAM-SHA-512", wantError: true},
		{name: "missing port", broker: "kafka", wantError: true},
		{name: "missing client key", broker: "kafka:9093", cert: "/run/secrets/client.crt", wantError: true},
		{name: "SCRAM over plaintext", broker: "kafka:9092", insecure: "true", user: "hub", pass: "fixture", wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := configFromMap(map[string]string{
				"AUTH_MODE": "none", "KAFKA_SHADOW_BROKERS": tt.broker,
				"KAFKA_SHADOW_INSECURE_LOCAL": tt.insecure, "KAFKA_SHADOW_CLIENT_CERT_FILE": tt.cert,
				"KAFKA_SHADOW_USERNAME": tt.user, "KAFKA_SHADOW_PASSWORD": tt.pass,
				"KAFKA_SHADOW_SCRAM_MECHANISM": tt.mechanism,
			})
			if (err != nil) != tt.wantError {
				t.Fatalf("configuration error = %v, want error %v", err, tt.wantError)
			}
			if err == nil && (cfg.KafkaShadow.Enabled() != (tt.broker != "") ||
				cfg.KafkaShadow.Topic != "location-hub.observations.v1" || cfg.KafkaShadow.SCRAMMechanism != tt.mechanism) {
				t.Fatal("incorrect Kafka mirror defaults")
			}
		})
	}
}
