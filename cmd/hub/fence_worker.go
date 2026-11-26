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

func runFenceWorker(ctx context.Context) error {
	cfg, err := config.FromEnv()
	if err != nil {
		return fmt.Errorf("load fence-worker config: %w", err)
	}
	instance := strings.TrimSpace(os.Getenv("KAFKA_FENCE_INSTANCE"))
	if instance == "" {
		instance, err = os.Hostname()
		if err != nil {
			return fmt.Errorf("resolve fence worker instance ID: %w", err)
		}
	}
	if cfg.Telemetry.ServiceName == "open-location-hub" {
		cfg.Telemetry.ServiceName = "open-location-hub-fence-worker"
	}
	telemetry, err := observability.NewRuntime(ctx, cfg.Telemetry, cfg.HubID)
	if err != nil {
		return fmt.Errorf("initialize fence worker telemetry: %w", err)
	}
	defer func() { _ = telemetry.Shutdown(context.WithoutCancel(ctx)) }()
	ctx = observability.WithRuntime(ctx, telemetry)
	input := workerEnv("KAFKA_FENCE_INPUT_TOPIC", "location-hub.fence-evaluations.v1")
	output := workerEnv("KAFKA_FENCE_TRANSITION_TOPIC", "location-hub.fence-transitions.v1")
	state := workerEnv("KAFKA_FENCE_STATE_TOPIC", "location-hub.fence-state.v1")
	worker, err := stream.OpenFenceWorker(ctx, cfg.KafkaShadow, input, output, state, instance)
	if err != nil {
		return fmt.Errorf("initialize fence worker: %w", err)
	}
	defer worker.Close()
	return serveWorker(ctx, func(context.Context) error { return worker.Run() })
}
