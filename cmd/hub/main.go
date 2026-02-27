package main

import (
	"context"
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
	"github.com/formation-res/open-rtls-hub/internal/mqtt"
	"github.com/formation-res/open-rtls-hub/internal/observability"
	"github.com/formation-res/open-rtls-hub/internal/state/valkey"
	"github.com/formation-res/open-rtls-hub/internal/storage/postgres"
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

	mq := mqtt.NewClient(cfg.MQTTBrokerURL)
	defer func() { _ = mq.Close() }()

	jwtVerifier, err := auth.NewVerifier(ctx, cfg.Auth)
	if err != nil {
		logger.Fatal("auth init failed", observability.Error(err))
	}

	r := chi.NewRouter()
	r.Use(observability.RequestLogger(logger))
	r.Use(auth.Middleware(jwtVerifier, cfg.Auth))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	h := handlers.New(handlers.Dependencies{
		Logger: logger,
		DB:     pg,
		Cache:  cache,
		MQTT:   mq,
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
