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
	"github.com/formation-res/open-rtls-hub/internal/hubmeta"
	"github.com/formation-res/open-rtls-hub/internal/mqtt"
	"github.com/formation-res/open-rtls-hub/internal/observability"
	"github.com/formation-res/open-rtls-hub/internal/rpc"
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

type runtimeDeps struct {
	loadConfig           func() (config.Config, error)
	newLogger            func(string) (*zap.Logger, func(), error)
	openQueries          func(context.Context, string) (sqlcgen.Querier, runtimeCloser, error)
	newMQTT              func(*zap.Logger, string) (mqttRuntimeClient, error)
	newAuthenticator     func(context.Context, config.AuthConfig) (auth.Authenticator, error)
	loadRegistry         func(string) (*auth.Registry, error)
	resolveHubMetadata   func(context.Context, sqlcgen.Querier, config.Config) (hubmeta.Metadata, error)
	newEventBus          func() *hub.EventBus
	newService           func(*zap.Logger, sqlcgen.Querier, *hub.EventBus, hub.Config) (*hub.Service, error)
	eventPublisherHandle func(mqttRuntimeClient) func(context.Context, hub.Event) error
	newRPCBridge         func(*zap.Logger, rpc.Publisher, rpc.Config) (rpcRuntimeBridge, error)
	newHTTPServer        func(string, http.Handler) httpServer
}

var defaultRuntime = runtimeDeps{
	loadConfig: config.FromEnv,
	newLogger:  observability.NewLogger,
	openQueries: func(ctx context.Context, dsn string) (sqlcgen.Querier, runtimeCloser, error) {
		pool, err := postgres.NewPool(ctx, dsn)
		if err != nil {
			return nil, nil, err
		}
		return sqlcgen.New(pool), runtimeCloserFunc(func() error {
			pool.Close()
			return nil
		}), nil
	},
	newMQTT:            authlessNewMQTT,
	newAuthenticator:   auth.NewAuthenticator,
	loadRegistry:       auth.LoadRegistry,
	resolveHubMetadata: hubmeta.Resolve,
	newEventBus:        hub.NewEventBus,
	newService:         hub.New,
	eventPublisherHandle: func(client mqttRuntimeClient) func(context.Context, hub.Event) error {
		if concrete, ok := client.(*mqtt.Client); ok {
			return mqtt.NewEventPublisher(concrete).Handle
		}
		return func(context.Context, hub.Event) error { return nil }
	},
	newRPCBridge: func(logger *zap.Logger, publisher rpc.Publisher, cfg rpc.Config) (rpcRuntimeBridge, error) {
		return rpc.NewBridge(logger, publisher, cfg)
	},
	newHTTPServer: func(addr string, handler http.Handler) httpServer {
		return &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
		}
	},
}

func authlessNewMQTT(logger *zap.Logger, brokerURL string) (mqttRuntimeClient, error) {
	return mqtt.NewClient(logger, brokerURL)
}

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
	return runWithRuntime(ctx, defaultRuntime)
}

func runWithRuntime(ctx context.Context, rt runtimeDeps) error {
	cfg, err := rt.loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	telemetry, err := observability.NewRuntime(ctx, cfg.Telemetry, cfg.HubID)
	if err != nil {
		return fmt.Errorf("init telemetry: %w", err)
	}
	observability.SetGlobal(telemetry)
	defer observability.SetGlobal(nil)
	defer func() { _ = telemetry.Shutdown(context.WithoutCancel(ctx)) }()
	ctx = observability.WithRuntime(ctx, telemetry)

	logger, cleanupLogger, err := rt.newLogger(cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer cleanupLogger()

	postgresCtx, postgresSpan := telemetry.StartSpan(ctx, "runtime.postgres.init")
	queries, closeQueries, err := rt.openQueries(ctx, cfg.PostgresURL)
	if err != nil {
		postgresSpan.RecordError(err)
		telemetry.RecordDependencyEvent(postgresCtx, "postgres", "init", "failure")
		postgresSpan.End()
		return fmt.Errorf("postgres init failed: %w", err)
	}
	telemetry.RecordDependencyEvent(postgresCtx, "postgres", "init", "success")
	postgresSpan.End()
	defer func() { _ = closeQueries.Close() }()

	hubMeta, err := rt.resolveHubMetadata(ctx, queries, cfg)
	if err != nil {
		return fmt.Errorf("hub metadata init failed: %w", err)
	}

	mqttCtx, mqttSpan := telemetry.StartSpan(ctx, "runtime.mqtt.init")
	mq, err := rt.newMQTT(logger, cfg.MQTTBrokerURL)
	if err != nil {
		mqttSpan.RecordError(err)
		telemetry.RecordDependencyEvent(mqttCtx, "mqtt", "init", "failure")
		mqttSpan.End()
		return fmt.Errorf("mqtt init failed: %w", err)
	}
	telemetry.RecordDependencyEvent(mqttCtx, "mqtt", "init", "success")
	mqttSpan.End()
	defer func() { _ = mq.Close() }()

	authCtx, authSpan := telemetry.StartSpan(ctx, "runtime.auth.init")
	authenticator, err := rt.newAuthenticator(ctx, cfg.Auth)
	if err != nil {
		authSpan.RecordError(err)
		telemetry.RecordDependencyEvent(authCtx, "auth", "init", "failure")
		authSpan.End()
		return fmt.Errorf("auth init failed: %w", err)
	}
	telemetry.RecordDependencyEvent(authCtx, "auth", "init", "success")
	authSpan.End()
	var registry *auth.Registry
	if cfg.Auth.Enabled && cfg.Auth.Mode != "none" {
		registry, err = rt.loadRegistry(cfg.Auth.PermissionsFile)
		if err != nil {
			return fmt.Errorf("auth permissions init failed: %w", err)
		}
	}

	eventBus := rt.newEventBus()
	service, err := rt.newService(logger, queries, eventBus, hub.Config{
		HubID:                                 hubMeta.HubID,
		LocationTTL:                           cfg.StateLocationTTL,
		ProximityTTL:                          cfg.StateProximityTTL,
		DedupTTL:                              cfg.StateDedupTTL,
		NativeLocationBuffer:                  cfg.NativeLocationBuffer,
		DerivedLocationBuffer:                 cfg.DerivedLocationBuffer,
		MetadataReconcileInterval:             cfg.MetadataReconcileInterval,
		CollisionsEnabled:                     cfg.CollisionsEnabled,
		CollisionStateTTL:                     cfg.CollisionStateTTL,
		CollisionCollidingDebounce:            cfg.CollisionCollidingDebounce,
		CollisionDefaultRadiusMeters:          cfg.CollisionDefaultRadiusMeters,
		KalmanFilterEnabled:                   cfg.KalmanFilterEnabled,
		KalmanLocationMaxPoints:               cfg.KalmanLocationMaxPoints,
		KalmanLocationMaxAge:                  cfg.KalmanLocationMaxAge,
		KalmanEmitMaxFrequencyHz:              cfg.KalmanEmitMaxFrequencyHz,
		ProximityResolutionEntryConfidenceMin: cfg.ProximityResolutionEntryConfidenceMin,
		ProximityResolutionExitGraceDuration:  cfg.ProximityResolutionExitGraceDuration,
		ProximityResolutionBoundaryGrace:      cfg.ProximityResolutionBoundaryGrace,
		ProximityResolutionMinDwellDuration:   cfg.ProximityResolutionMinDwellDuration,
		ProximityResolutionPositionMode:       cfg.ProximityResolutionPositionMode,
		ProximityResolutionFallbackRadius:     cfg.ProximityResolutionFallbackRadius,
		ProximityResolutionStaleStateTTL:      cfg.ProximityResolutionStaleStateTTL,
	})
	if err != nil {
		return fmt.Errorf("service init failed: %w", err)
	}
	service.Start(ctx)
	var cleanupMQTTPublisher func()
	if eventBus != nil {
		var ch <-chan hub.Event
		var unsubscribeMQTTPublisher func()
		ch, unsubscribeMQTTPublisher = eventBus.Subscribe(cfg.EventBusSubscriberBuffer)
		mqttPublisherDone := runEventPublisher(ctx, logger, ch, rt.eventPublisherHandle(mq))
		cleanupMQTTPublisher = func() {
			unsubscribeMQTTPublisher()
			<-mqttPublisherDone
		}
	}
	if cleanupMQTTPublisher != nil {
		defer cleanupMQTTPublisher()
	}
	rpcCtx, rpcSpan := telemetry.StartSpan(ctx, "runtime.rpc.init")
	rpcBridge, err := rt.newRPCBridge(logger, mq, rpc.Config{
		Timeout:              cfg.RPCTimeout,
		HandlerID:            cfg.RPCHandlerID,
		AnnouncementInterval: cfg.RPCAnnouncementInterval,
		Authorizer:           registry,
		Identify: rpc.IdentifyConfig{
			ServiceName: hubMeta.Label,
			AuthMode:    cfg.Auth.Mode,
			HubID:       hubMeta.HubID,
		},
	})
	if err != nil {
		rpcSpan.RecordError(err)
		telemetry.RecordDependencyEvent(rpcCtx, "rpc", "init", "failure")
		rpcSpan.End()
		return fmt.Errorf("rpc bridge init failed: %w", err)
	}
	telemetry.RecordDependencyEvent(rpcCtx, "rpc", "init", "success")
	rpcSpan.End()
	defer func() { _ = rpcBridge.Close() }()

	if err := mq.Subscribe(mqtt.TopicLocationPubWildcard(), func(ctx context.Context, _ string, payload []byte) error {
		ctx = observability.WithIngestTransport(ctx, "mqtt")
		switch firstNonWhitespaceByte(payload) {
		case '[':
			var body []gen.Location
			if err := json.Unmarshal(payload, &body); err != nil {
				return err
			}
			return service.ProcessLocations(ctx, body)
		default:
			var single gen.Location
			if err := json.Unmarshal(payload, &single); err != nil {
				return err
			}
			return service.ProcessLocations(ctx, []gen.Location{single})
		}
	}); err != nil {
		return fmt.Errorf("mqtt location subscription failed: %w", err)
	}

	if err := mq.Subscribe(mqtt.TopicProximityWildcard(), func(ctx context.Context, _ string, payload []byte) error {
		ctx = observability.WithIngestTransport(ctx, "mqtt")
		switch firstNonWhitespaceByte(payload) {
		case '[':
			var body []gen.Proximity
			if err := json.Unmarshal(payload, &body); err != nil {
				return err
			}
			return service.ProcessProximities(ctx, body)
		default:
			var single gen.Proximity
			if err := json.Unmarshal(payload, &single); err != nil {
				return err
			}
			return service.ProcessProximities(ctx, []gen.Proximity{single})
		}
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
	r.Get("/debug/runtime/drops", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(service.RuntimeStatsSnapshot()); err != nil {
			http.Error(w, "encode runtime drop diagnostics", http.StatusInternalServerError)
		}
	})

	h := handlers.New(handlers.Dependencies{
		Logger:                logger,
		Service:               service,
		RPC:                   rpcBridge,
		RequestBodyLimitBytes: cfg.HTTPRequestBodyLimitBytes,
	})
	gen.HandlerFromMux(h, r)
	wsHub := ws.New(
		logger,
		service,
		eventBus,
		authenticator,
		registry,
		cfg.Auth,
		cfg.WebSocketWriteTimeout,
		cfg.WebSocketReadTimeout,
		cfg.WebSocketPingInterval,
		cfg.WebSocketOutboundBuffer,
		cfg.EventBusSubscriberBuffer,
		cfg.CollisionsEnabled,
	)
	r.Get("/v2/ws/socket", wsHub.Handle)

	srv := rt.newHTTPServer(cfg.HTTPListenAddr, r)

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

func firstNonWhitespaceByte(payload []byte) byte {
	for _, b := range payload {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return b
		}
	}
	return 0
}
