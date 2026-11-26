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

func runAssetRouter(ctx context.Context) error {
	cfg, err := config.FromEnv()
	if err != nil {
		return fmt.Errorf("load asset-router config: %w", err)
	}
	outputTopic := os.Getenv("KAFKA_ASSET_INPUT_TOPIC")
	if outputTopic == "" {
		outputTopic = "location-hub.asset-inputs.v1"
	}
	group := os.Getenv("KAFKA_ASSET_ROUTER_GROUP")
	if group == "" {
		group = "location-hub.asset-router.v1"
	}
	instance := strings.TrimSpace(os.Getenv("KAFKA_ASSET_ROUTER_INSTANCE"))
	if instance == "" {
		instance, err = os.Hostname()
		if err != nil {
			return fmt.Errorf("resolve asset-router instance ID: %w", err)
		}
	}

	if cfg.Telemetry.ServiceName == "open-location-hub" {
		cfg.Telemetry.ServiceName = "open-location-hub-asset-router"
	}
	telemetry, err := observability.NewRuntime(ctx, cfg.Telemetry, cfg.HubID)
	if err != nil {
		return fmt.Errorf("initialize asset-router telemetry: %w", err)
	}
	defer func() { _ = telemetry.Shutdown(context.WithoutCancel(ctx)) }()
	ctx = observability.WithRuntime(ctx, telemetry)
	router, err := stream.OpenAssetRouter(ctx, cfg.KafkaShadow, outputTopic, group, instance)
	if err != nil {
		return fmt.Errorf("initialize asset router: %w", err)
	}
	defer router.Close()
	return router.Run(ctx)
}
