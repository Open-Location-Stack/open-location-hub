package config

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
)

var kafkaTopicName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,249}$`)

// KafkaShadowConfig controls the optional mirror of validated observations.
type KafkaShadowConfig struct {
	Brokers        []string
	Topic          string
	InsecureLocal  bool
	CAFile         string
	ClientCertFile string
	ClientKeyFile  string
	Username       string
	Password       string
}

// Enabled reports whether a Kafka shadow sink was explicitly configured.
func (c KafkaShadowConfig) Enabled() bool {
	return len(c.Brokers) != 0
}

func loadKafkaShadow(lookup lookupEnvFunc) (KafkaShadowConfig, error) {
	get := func(name string) string { return envWithLookup(lookup, "KAFKA_SHADOW_"+name, "") }
	cfg := KafkaShadowConfig{
		Brokers:        csvEnvWithLookup(lookup, "KAFKA_SHADOW_BROKERS", ""),
		Topic:          envWithLookup(lookup, "KAFKA_SHADOW_TOPIC", "location-hub.observations.v1"),
		CAFile:         get("CA_FILE"),
		ClientCertFile: get("CLIENT_CERT_FILE"),
		ClientKeyFile:  get("CLIENT_KEY_FILE"),
		Username:       get("USERNAME"),
		Password:       get("PASSWORD"),
	}
	if value := get("INSECURE_LOCAL"); value != "" {
		insecure, err := strconv.ParseBool(value)
		if err != nil {
			return cfg, fmt.Errorf("KAFKA_SHADOW_INSECURE_LOCAL must be a boolean")
		}
		cfg.InsecureLocal = insecure
	}
	if !cfg.Enabled() {
		if cfg.InsecureLocal || cfg.CAFile != "" || cfg.ClientCertFile != "" || cfg.ClientKeyFile != "" ||
			cfg.Username != "" || cfg.Password != "" || get("TOPIC") != "" {
			return cfg, fmt.Errorf("KAFKA_SHADOW_BROKERS is required when configuring the Kafka mirror")
		}
		return cfg, nil
	}
	for _, broker := range cfg.Brokers {
		host, port, err := net.SplitHostPort(broker)
		number, portErr := strconv.ParseUint(port, 10, 16)
		if err != nil || host == "" || portErr != nil || number == 0 {
			return cfg, fmt.Errorf("KAFKA_SHADOW_BROKERS entries must be host:port")
		}
	}
	if !kafkaTopicName.MatchString(cfg.Topic) || cfg.Topic == "." || cfg.Topic == ".." {
		return cfg, fmt.Errorf("KAFKA_SHADOW_TOPIC is not a valid Kafka topic")
	}
	if (cfg.ClientCertFile == "") != (cfg.ClientKeyFile == "") {
		return cfg, fmt.Errorf("configure both Kafka client certificate and key")
	}
	if (cfg.Username == "") != (cfg.Password == "") {
		return cfg, fmt.Errorf("configure both Kafka SCRAM username and password")
	}
	if cfg.InsecureLocal && (cfg.CAFile != "" || cfg.ClientCertFile != "" || cfg.Username != "") {
		return cfg, fmt.Errorf("a TLS broker is required for Kafka TLS and SCRAM settings")
	}
	return cfg, nil
}
