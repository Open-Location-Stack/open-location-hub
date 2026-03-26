package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/formation-res/open-rtls-hub/internal/ws"
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

const shutdownTimeout = 10 * time.Second

type mqttRuntimeClient interface {
	rpc.Publisher
	rpc.ConnectAwarePublisher
	Close() error
}

type rpcRuntimeBridge interface {
	handlers.RPCBridge
	Close() error
}

type httpServer interface {
	ListenAndServe() error
	Shutdown(ctx context.Context) error
}

type runtimeCloser interface {
	Close() error
}

var (
	loadConfig  = config.FromEnv
	newLogger   = observability.NewLogger
	openQueries = func(ctx context.Context, dsn string) (sqlcgen.Querier, runtimeCloser, error) {
		pool, err := postgres.NewPool(ctx, dsn)
		if err != nil {
			return nil, nil, err
		}
		return sqlcgen.New(pool), runtimeCloserFunc(func() error {
			pool.Close()
			return nil
		}), nil
	}
	newCache = func(addr string) (*valkey.Client, func(), error) {
		cache := valkey.NewClient(addr)
		return cache, func() { _ = cache.Close() }, nil
	}
	newMQTT = func(logger *zap.Logger, brokerURL string) (mqttRuntimeClient, error) {
		return mqtt.NewClient(logger, brokerURL)
	}
	newAuthenticator     = auth.NewAuthenticator
	loadRegistry         = auth.LoadRegistry
	newEventBus          = hub.NewEventBus
	newService           = hub.New
	eventPublisherHandle = func(client mqttRuntimeClient) func(context.Context, hub.Event) error {
		if concrete, ok := client.(*mqtt.Client); ok {
			return mqtt.NewEventPublisher(concrete).Handle
		}
		return func(context.Context, hub.Event) error { return nil }
	}
	newRPCBridge = func(logger *zap.Logger, publisher rpc.Publisher, cfg rpc.Config) (rpcRuntimeBridge, error) {
		return rpc.NewBridge(logger, publisher, cfg)
	}
	newHTTPServer = func(addr string, handler http.Handler) httpServer {
		return &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
		}
	}
)

type runtimeCloserFunc func() error

func (fn runtimeCloserFunc) Close() error { return fn() }

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "open-rtls-hub: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger, cleanupLogger, err := newLogger(cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer cleanupLogger()

	queries, closeQueries, err := openQueries(ctx, cfg.PostgresURL)
	if err != nil {
		return fmt.Errorf("postgres init failed: %w", err)
	}
	defer func() { _ = closeQueries.Close() }()

	cache, closeCache, err := newCache(cfg.ValkeyURL)
	if err != nil {
		return fmt.Errorf("cache init failed: %w", err)
	}
	defer closeCache()

	mq, err := newMQTT(logger, cfg.MQTTBrokerURL)
	if err != nil {
		return fmt.Errorf("mqtt init failed: %w", err)
	}
	defer func() { _ = mq.Close() }()

	authenticator, err := newAuthenticator(ctx, cfg.Auth)
	if err != nil {
		return fmt.Errorf("auth init failed: %w", err)
	}
	var registry *auth.Registry
	if cfg.Auth.Enabled && cfg.Auth.Mode != "none" {
		registry, err = loadRegistry(cfg.Auth.PermissionsFile)
		if err != nil {
			return fmt.Errorf("auth permissions init failed: %w", err)
		}
	}

	eventBus := newEventBus()
	service := newService(logger, queries, cache, eventBus, hub.Config{
		LocationTTL:                           cfg.StateLocationTTL,
		ProximityTTL:                          cfg.StateProximityTTL,
		DedupTTL:                              cfg.StateDedupTTL,
		CollisionsEnabled:                     cfg.CollisionsEnabled,
		CollisionStateTTL:                     cfg.CollisionStateTTL,
		CollisionCollidingDebounce:            cfg.CollisionCollidingDebounce,
		ProximityResolutionEntryConfidenceMin: cfg.ProximityResolutionEntryConfidenceMin,
		ProximityResolutionExitGraceDuration:  cfg.ProximityResolutionExitGraceDuration,
		ProximityResolutionBoundaryGrace:      cfg.ProximityResolutionBoundaryGrace,
		ProximityResolutionMinDwellDuration:   cfg.ProximityResolutionMinDwellDuration,
		ProximityResolutionPositionMode:       cfg.ProximityResolutionPositionMode,
		ProximityResolutionFallbackRadius:     cfg.ProximityResolutionFallbackRadius,
		ProximityResolutionStaleStateTTL:      cfg.ProximityResolutionStaleStateTTL,
	})
	var cleanupMQTTPublisher func()
	if eventBus != nil {
		var ch <-chan hub.Event
		var unsubscribeMQTTPublisher func()
		ch, unsubscribeMQTTPublisher = eventBus.Subscribe(128)
		mqttPublisherDone := runEventPublisher(ctx, logger, ch, eventPublisherHandle(mq))
		cleanupMQTTPublisher = func() {
			unsubscribeMQTTPublisher()
			<-mqttPublisherDone
		}
	}
	if cleanupMQTTPublisher != nil {
		defer cleanupMQTTPublisher()
	}
	rpcBridge, err := newRPCBridge(logger, mq, rpc.Config{
		Timeout:              cfg.RPCTimeout,
		HandlerID:            cfg.RPCHandlerID,
		AnnouncementInterval: cfg.RPCAnnouncementInterval,
		Authorizer:           registry,
		Identify: rpc.IdentifyConfig{
			ServiceName: "open-rtls-hub",
			AuthMode:    cfg.Auth.Mode,
		},
	})
	if err != nil {
		return fmt.Errorf("rpc bridge init failed: %w", err)
	}
	defer func() { _ = rpcBridge.Close() }()

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
		return fmt.Errorf("mqtt location subscription failed: %w", err)
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
		return fmt.Errorf("mqtt proximity subscription failed: %w", err)
	}

	r := chi.NewRouter()
	r.Use(observability.RequestLogger(logger))
	r.Use(auth.Middleware(authenticator, cfg.Auth, registry))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	h := handlers.New(handlers.Dependencies{
		Logger:                logger,
		Service:               service,
		RPC:                   rpcBridge,
		RequestBodyLimitBytes: cfg.HTTPRequestBodyLimitBytes,
	})
	gen.HandlerFromMux(h, r)
	wsHub := ws.New(logger, service, eventBus, authenticator, registry, cfg.Auth, cfg.WebSocketWriteTimeout, cfg.WebSocketOutboundBuffer, cfg.CollisionsEnabled)
	r.Get("/v2/ws/socket", wsHub.Handle)

	srv := newHTTPServer(cfg.HTTPListenAddr, r)

	serverErrCh := make(chan error, 1)
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		logger.Info("http server starting", observability.String("addr", cfg.HTTPListenAddr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrCh <- err
		}
	}()

	select {
	case err := <-serverErrCh:
		return fmt.Errorf("http server failed: %w", err)
	case <-ctx.Done():
		logger.Info("shutdown requested", observability.Error(ctx.Err()))
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown failed: %w", err)
	}
	<-serverDone

	return nil
}

func runEventPublisher(ctx context.Context, logger *zap.Logger, events <-chan hub.Event, publish func(context.Context, hub.Event) error) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				if err := publish(ctx, event); err != nil && !errors.Is(err, context.Canceled) {
					logger.Warn("mqtt event publish failed", observability.Error(err))
				}
			}
		}
	}()
	return done
}
