package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/auth"
	"github.com/formation-res/open-rtls-hub/internal/config"
	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/formation-res/open-rtls-hub/internal/httpapi/handlers"
	"github.com/formation-res/open-rtls-hub/internal/hub"
	"github.com/formation-res/open-rtls-hub/internal/mqtt"
	"github.com/formation-res/open-rtls-hub/internal/observability"
	"github.com/formation-res/open-rtls-hub/internal/rpc"
	"github.com/formation-res/open-rtls-hub/internal/state/valkey"
	"github.com/formation-res/open-rtls-hub/internal/storage/postgres"
	"github.com/formation-res/open-rtls-hub/internal/storage/postgres/sqlcgen"
	"github.com/go-chi/chi/v5"
)

func main() {
	cfg, err := config.FromEnv()
	if err != nil {
		panic(err)
	}

	logger, cleanupLogger, err := observability.NewLogger(cfg.LogLevel)
	if err != nil {
		panic(err)
	}
	defer cleanupLogger()

	ctx := context.Background()

	pg, err := postgres.NewPool(ctx, cfg.PostgresURL)
	if err != nil {
		logger.Fatal("postgres init failed", observability.Error(err))
	}
	defer pg.Close()

	cache := valkey.NewClient(cfg.ValkeyURL)
	defer func() { _ = cache.Close() }()

	mq, err := mqtt.NewClient(logger, cfg.MQTTBrokerURL)
	if err != nil {
		logger.Fatal("mqtt init failed", observability.Error(err))
	}
	defer func() { _ = mq.Close() }()

	authenticator, err := auth.NewAuthenticator(ctx, cfg.Auth)
	if err != nil {
		logger.Fatal("auth init failed", observability.Error(err))
	}
	var registry *auth.Registry
	if cfg.Auth.Enabled && cfg.Auth.Mode != "none" {
		registry, err = auth.LoadRegistry(cfg.Auth.PermissionsFile)
		if err != nil {
			logger.Fatal("auth permissions init failed", observability.Error(err))
		}
	}

	queries := sqlcgen.New(pg)
	service := hub.New(logger, queries, cache, mq, hub.Config{
		LocationTTL:  cfg.StateLocationTTL,
		ProximityTTL: cfg.StateProximityTTL,
		DedupTTL:     cfg.StateDedupTTL,
	})
	rpcBridge, err := rpc.NewBridge(logger, mq, cfg.RPCTimeout)
	if err != nil {
		logger.Fatal("rpc bridge init failed", observability.Error(err))
	}

	if err := mq.Subscribe(mqtt.TopicLocationPubWildcard(), func(ctx context.Context, _ string, payload []byte) error {
		var body []gen.Location
		if err := json.Unmarshal(payload, &body); err == nil {
			return service.ProcessLocations(ctx, body)
		}
		var single gen.Location
		if err := json.Unmarshal(payload, &single); err != nil {
			return err
		}
		return service.ProcessLocations(ctx, []gen.Location{single})
	}); err != nil {
		logger.Fatal("mqtt location subscription failed", observability.Error(err))
	}

	if err := mq.Subscribe(mqtt.TopicProximityWildcard(), func(ctx context.Context, _ string, payload []byte) error {
		var body []gen.Proximity
		if err := json.Unmarshal(payload, &body); err == nil {
			return service.ProcessProximities(ctx, body)
		}
		var single gen.Proximity
		if err := json.Unmarshal(payload, &single); err != nil {
			return err
		}
		return service.ProcessProximities(ctx, []gen.Proximity{single})
	}); err != nil {
		logger.Fatal("mqtt proximity subscription failed", observability.Error(err))
	}

	r := chi.NewRouter()
	r.Use(observability.RequestLogger(logger))
	r.Use(auth.Middleware(authenticator, cfg.Auth, registry))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	h := handlers.New(handlers.Dependencies{
		Logger:  logger,
		Service: service,
		RPC:     rpcBridge,
	})
	gen.HandlerFromMux(h, r)

	srv := &http.Server{
		Addr:              cfg.HTTPListenAddr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("http server starting", observability.String("addr", cfg.HTTPListenAddr))
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("http server failed", observability.Error(err))
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown failed", observability.Error(err))
	}
}
