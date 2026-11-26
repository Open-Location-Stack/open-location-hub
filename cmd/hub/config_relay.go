package main

import (
	"context"
	"fmt"
	"time"

	"github.com/formation-res/open-location-hub/internal/config"
	"github.com/formation-res/open-location-hub/internal/observability"
	"github.com/formation-res/open-location-hub/internal/storage/postgres"
	"github.com/formation-res/open-location-hub/internal/storage/postgres/sqlcgen"
	"github.com/formation-res/open-location-hub/internal/stream"
	"github.com/google/uuid"
)

func runConfigRelay(ctx context.Context) error {
	cfg, err := config.FromEnv()
	if err != nil {
		return fmt.Errorf("load configuration relay settings: %w", err)
	}
	topic := workerEnv("KAFKA_CONFIG_TOPIC", "location-hub.configuration.v1")
	history, err := time.ParseDuration(workerEnv("HUB_CONFIG_HISTORY", "6h"))
	if err != nil || history <= 0 {
		return fmt.Errorf("HUB_CONFIG_HISTORY must be a positive duration")
	}
	db, err := postgres.NewPool(ctx, cfg.PostgresURL)
	if err != nil {
		return fmt.Errorf("open configuration outbox database: %w", err)
	}
	defer db.Close()

	startCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	row, err := sqlcgen.New(db).GetHubMetadata(startCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("read persisted hub identity for configuration relay: %w", err)
	}
	if !row.HubID.Valid || uuid.UUID(row.HubID.Bytes) == uuid.Nil {
		return fmt.Errorf("persisted hub identity is invalid")
	}
	hubID := uuid.UUID(row.HubID.Bytes).String()
	if cfg.HubID != "" && cfg.HubID != hubID {
		return fmt.Errorf("configured hub ID differs from the persisted identity")
	}
	if cfg.Telemetry.ServiceName == "open-location-hub" {
		cfg.Telemetry.ServiceName = "open-location-hub-config-relay"
	}
	telemetry, err := observability.NewRuntime(ctx, cfg.Telemetry, hubID)
	if err != nil {
		return fmt.Errorf("initialize configuration relay telemetry: %w", err)
	}
	defer func() { _ = telemetry.Shutdown(context.WithoutCancel(ctx)) }()
	ctx = observability.WithRuntime(ctx, telemetry)
	relay, err := stream.OpenConfigRelay(ctx, cfg.KafkaShadow, db, hubID, topic, history)
	if err != nil {
		return fmt.Errorf("initialize configuration relay: %w", err)
	}
	defer relay.Close()
	return serveWorker(ctx, relay.Run)
}
