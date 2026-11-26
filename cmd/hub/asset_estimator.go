package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/formation-res/open-location-hub/internal/config"
	"github.com/formation-res/open-location-hub/internal/observability"
	"github.com/formation-res/open-location-hub/internal/stream"
)

func runAssetEstimator(ctx context.Context) error {
	cfg, err := config.FromEnv()
	if err != nil {
		return fmt.Errorf("load asset-estimator config: %w", err)
	}
	input := workerEnv("KAFKA_ASSET_INPUT_TOPIC", "location-hub.asset-inputs.v1")
	output := workerEnv("KAFKA_ESTIMATE_TOPIC", "location-hub.motion-estimates.v1")
	state := workerEnv("KAFKA_ESTIMATOR_STATE_TOPIC", "location-hub.estimator-state.v1")
	instance := strings.TrimSpace(os.Getenv("KAFKA_ESTIMATOR_INSTANCE"))
	if instance == "" {
		instance, err = os.Hostname()
		if err != nil {
			return fmt.Errorf("resolve estimator instance ID: %w", err)
		}
	}
	if cfg.Telemetry.ServiceName == "open-location-hub" {
		cfg.Telemetry.ServiceName = "open-location-hub-asset-estimator"
	}
	telemetry, err := observability.NewRuntime(ctx, cfg.Telemetry, cfg.HubID)
	if err != nil {
		return fmt.Errorf("initialize estimator telemetry: %w", err)
	}
	defer func() { _ = telemetry.Shutdown(context.WithoutCancel(ctx)) }()
	ctx = observability.WithRuntime(ctx, telemetry)
	worker, err := stream.OpenEstimateWorker(ctx, cfg.KafkaShadow, input, output, state, instance)
	if err != nil {
		return fmt.Errorf("initialize estimator: %w", err)
	}
	defer worker.Close()
	return worker.Run()
}

func workerEnv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
