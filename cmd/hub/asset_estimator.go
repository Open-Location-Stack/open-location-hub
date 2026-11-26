package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/formation-res/open-location-hub/internal/config"
	"github.com/formation-res/open-location-hub/internal/observability"
	"github.com/formation-res/open-location-hub/internal/storage/postgres"
	"github.com/formation-res/open-location-hub/internal/stream"
	"golang.org/x/sync/errgroup"
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
	group, workerCtx := errgroup.WithContext(ctx)
	var estimatorConfiguration *stream.EstimatorConfiguration
	fenceInputTopic := strings.TrimSpace(os.Getenv("KAFKA_FENCE_INPUT_TOPIC"))
	collisionCellTopic := strings.TrimSpace(os.Getenv("KAFKA_COLLISION_CELL_TOPIC"))
	spatial := fenceInputTopic != "" || collisionCellTopic != ""
	if spatial && strings.TrimSpace(os.Getenv("HUB_CONFIG_READ_URL")) == "" {
		return fmt.Errorf("HUB_CONFIG_READ_URL is required for spatial routing")
	}
	if snapshotURL := strings.TrimSpace(os.Getenv("HUB_CONFIG_READ_URL")); snapshotURL != "" {
		if cfg.HubID == "" {
			return fmt.Errorf("HUB_ID is required for the estimator configuration watch")
		}
		topic := workerEnv("KAFKA_CONFIG_TOPIC", "location-hub.configuration.v1")
		if topic == cfg.KafkaShadow.Topic || topic == input || topic == output || topic == state {
			return fmt.Errorf("estimator configuration watch requires a separate topic")
		}
		maxStale, err := time.ParseDuration(workerEnv("HUB_CONFIG_MAX_STALE", "30s"))
		if err != nil {
			return fmt.Errorf("invalid HUB_CONFIG_MAX_STALE: %w", err)
		}
		db, err := postgres.NewPool(ctx, snapshotURL)
		if err != nil {
			return fmt.Errorf("open estimator configuration database: %w", err)
		}
		defer db.Close()
		watch, err := stream.OpenConfigurationWatch(ctx, cfg.KafkaShadow, db, topic, cfg.HubID, maxStale, spatial)
		if err != nil {
			return fmt.Errorf("initialize estimator configuration watch: %w", err)
		}
		defer watch.Close()
		estimatorConfiguration = &stream.EstimatorConfiguration{
			Watch: watch, LocationTTL: cfg.StateLocationTTL, ProximityTTL: cfg.StateProximityTTL,
			FenceInputTopic: fenceInputTopic, CollisionCellTopic: collisionCellTopic, CollisionTTL: cfg.CollisionStateTTL,
		}
	}
	worker, err := stream.OpenEstimateWorker(workerCtx, cfg.KafkaShadow, input, output, state, instance, estimatorConfiguration)
	if err != nil {
		return fmt.Errorf("initialize estimator: %w", err)
	}
	defer worker.Close()
	if estimatorConfiguration != nil {
		group.Go(func() error { return estimatorConfiguration.Watch.Run(workerCtx) })
	}
	group.Go(func() error {
		return serveWorker(workerCtx, func(context.Context) error { return worker.Run() })
	})
	return group.Wait()
}

func workerEnv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
