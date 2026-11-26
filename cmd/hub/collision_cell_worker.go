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

func runCollisionCellWorker(ctx context.Context) error {
	cfg, err := config.FromEnv()
	if err != nil {
		return fmt.Errorf("load collision-cell-worker config: %w", err)
	}
	instance := strings.TrimSpace(os.Getenv("KAFKA_COLLISION_CELL_INSTANCE"))
	if instance == "" {
		instance, err = os.Hostname()
		if err != nil {
			return fmt.Errorf("resolve collision cell worker instance ID: %w", err)
		}
	}
	if cfg.Telemetry.ServiceName == "open-location-hub" {
		cfg.Telemetry.ServiceName = "open-location-hub-collision-cell-worker"
	}
	telemetry, err := observability.NewRuntime(ctx, cfg.Telemetry, cfg.HubID)
	if err != nil {
		return fmt.Errorf("initialize collision cell worker telemetry: %w", err)
	}
	defer func() { _ = telemetry.Shutdown(context.WithoutCancel(ctx)) }()
	ctx = observability.WithRuntime(ctx, telemetry)
	input := workerEnv("KAFKA_COLLISION_CELL_TOPIC", "location-hub.collision-cells.v1")
	output := workerEnv("KAFKA_COLLISION_PAIR_TOPIC", "location-hub.collision-pairs.v1")
	state := workerEnv("KAFKA_COLLISION_CELL_STATE_TOPIC", "location-hub.collision-cell-state.v1")
	worker, err := stream.OpenCollisionCellWorker(ctx, cfg.KafkaShadow, input, output, state, instance)
	if err != nil {
		return fmt.Errorf("initialize collision cell worker: %w", err)
	}
	defer worker.Close()
	return serveWorker(ctx, func(context.Context) error { return worker.Run() })
}
