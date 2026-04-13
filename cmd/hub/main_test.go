package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/auth"
	"github.com/formation-res/open-rtls-hub/internal/config"
	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/formation-res/open-rtls-hub/internal/hub"
	"github.com/formation-res/open-rtls-hub/internal/hubmeta"
	"github.com/formation-res/open-rtls-hub/internal/mqtt"
	"github.com/formation-res/open-rtls-hub/internal/rpc"
	"github.com/formation-res/open-rtls-hub/internal/storage/postgres/sqlcgen"
	"go.uber.org/zap"
)

func TestRunStartsAndShutsDownServerGracefully(t *testing.T) {
	t.Parallel()

	rt := stubRuntimeForTest(t)

	fakeMQTTClient := &fakeRuntimeMQTT{}
	probeDone := make(chan error, 1)
	server := &fakeHTTPServer{
		onListen: func(handler http.Handler) {
			healthReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			healthRec := httptest.NewRecorder()
			handler.ServeHTTP(healthRec, healthReq)
			if healthRec.Code != http.StatusOK || strings.TrimSpace(healthRec.Body.String()) != "ok" {
				probeDone <- errors.New("health endpoint not wired correctly")
				return
			}

			dropsReq := httptest.NewRequest(http.MethodGet, "/debug/runtime/drops", nil)
			dropsRec := httptest.NewRecorder()
			handler.ServeHTTP(dropsRec, dropsReq)
			if dropsRec.Code != http.StatusOK {
				probeDone <- errors.New("runtime drops endpoint not wired correctly")
				return
			}
			var snapshot hub.RuntimeStatsSnapshot
			if err := json.Unmarshal(dropsRec.Body.Bytes(), &snapshot); err != nil {
				probeDone <- err
				return
			}
			if snapshot.EventBusDrops != 0 || snapshot.NativeQueueDrops != 0 || snapshot.DecisionQueueDrops != 0 || snapshot.WebSocketOutboundDrops != 0 {
				probeDone <- errors.New("unexpected non-zero runtime drop counters")
				return
			}
			probeDone <- nil
		},
	}

	rt.newMQTT = func(*zap.Logger, string) (mqttRuntimeClient, error) {
		return fakeMQTTClient, nil
	}
	rt.newHTTPServer = func(_ string, handler http.Handler) httpServer {
		server.handler = handler
		return server
	}
	rt.newEventBus = func() *hub.EventBus { return nil }
	rt.newService = func(*zap.Logger, sqlcgen.Querier, *hub.EventBus, hub.Config) (*hub.Service, error) {
		return &hub.Service{}, nil
	}
	rt.newRPCBridge = func(*zap.Logger, rpc.Publisher, rpc.Config) (rpcRuntimeBridge, error) {
		return fakeRuntimeRPCBridge{}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- runWithRuntime(ctx, rt)
	}()

	select {
	case err := <-probeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for health probe")
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for run to exit")
	}

	if !fakeMQTTClient.subscribed(mqtt.TopicLocationPubWildcard()) {
		t.Fatalf("expected location subscription %q", mqtt.TopicLocationPubWildcard())
	}
	if !fakeMQTTClient.subscribed(mqtt.TopicProximityWildcard()) {
		t.Fatalf("expected proximity subscription %q", mqtt.TopicProximityWildcard())
	}
	if !server.shutdownCalled {
		t.Fatal("expected graceful shutdown")
	}
}

func TestFirstNonWhitespaceByte(t *testing.T) {
	t.Parallel()

	if got := firstNonWhitespaceByte([]byte(" \n\t[{")); got != '[' {
		t.Fatalf("unexpected array prefix: %q", got)
	}
	if got := firstNonWhitespaceByte([]byte("\r\n {")); got != '{' {
		t.Fatalf("unexpected object prefix: %q", got)
	}
	if got := firstNonWhitespaceByte([]byte(" \n\t")); got != 0 {
		t.Fatalf("expected zero for whitespace-only payload, got %q", got)
	}
}

func TestRunReturnsMQTTSubscriptionFailure(t *testing.T) {
	t.Parallel()

	rt := stubRuntimeForTest(t)

	wantErr := errors.New("subscribe failed")
	rt.newMQTT = func(*zap.Logger, string) (mqttRuntimeClient, error) {
		return &fakeRuntimeMQTT{subscribeErrByFilter: map[string]error{
			mqtt.TopicProximityWildcard(): wantErr,
		}}, nil
	}
	rt.newHTTPServer = func(_ string, handler http.Handler) httpServer {
		_ = handler
		t.Fatal("server should not start when mqtt subscription fails")
		return nil
	}
	rt.newEventBus = func() *hub.EventBus { return nil }
	rt.newService = func(*zap.Logger, sqlcgen.Querier, *hub.EventBus, hub.Config) (*hub.Service, error) {
		return &hub.Service{}, nil
	}
	rt.newRPCBridge = func(*zap.Logger, rpc.Publisher, rpc.Config) (rpcRuntimeBridge, error) {
		return fakeRuntimeRPCBridge{}, nil
	}

	err := runWithRuntime(context.Background(), rt)
	if err == nil || !strings.Contains(err.Error(), "mqtt proximity subscription failed: subscribe failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunEventPublisherPublishesEvents(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan hub.Event, 1)
	donePublishing := make(chan struct{}, 1)
	var (
		mu        sync.Mutex
		published []hub.Event
	)

	done := runEventPublisher(ctx, zap.NewNop(), events, func(_ context.Context, event hub.Event) error {
		mu.Lock()
		published = append(published, event)
		mu.Unlock()
		donePublishing <- struct{}{}
		return nil
	})

	expected := hub.Event{Kind: hub.EventLocation, ProviderID: "provider-1"}
	events <- expected

	select {
	case <-donePublishing:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event publication")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for publisher shutdown")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(published) != 1 {
		t.Fatalf("expected 1 published event, got %d", len(published))
	}
	if published[0].Kind != expected.Kind || published[0].ProviderID != expected.ProviderID {
		t.Fatalf("published event mismatch: got %+v want %+v", published[0], expected)
	}
}

func TestRunEventPublisherStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan hub.Event)

	done := runEventPublisher(ctx, zap.NewNop(), events, func(_ context.Context, event hub.Event) error {
		t.Fatalf("unexpected published event: %+v", event)
		return nil
	})

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for publisher shutdown after context cancellation")
	}
}

func stubRuntimeForTest(t *testing.T) runtimeDeps {
	t.Helper()

	return runtimeDeps{
		loadConfig: func() (config.Config, error) {
			return config.Config{
				HTTPListenAddr:                       ":0",
				HTTPRequestBodyLimitBytes:            1024,
				LogLevel:                             "info",
				HubID:                                "4f630dd4-e5f2-4398-9970-c63cad9bc109",
				HubLabel:                             "hub-test",
				PostgresURL:                          "postgres://test",
				MQTTBrokerURL:                        "tcp://localhost:1883",
				WebSocketWriteTimeout:                time.Second,
				WebSocketOutboundBuffer:              2,
				StateLocationTTL:                     time.Minute,
				StateProximityTTL:                    time.Minute,
				StateDedupTTL:                        time.Minute,
				MetadataReconcileInterval:            time.Minute,
				RPCTimeout:                           time.Second,
				RPCAnnouncementInterval:              time.Minute,
				RPCHandlerID:                         "hub-test",
				CollisionStateTTL:                    time.Minute,
				ProximityResolutionExitGraceDuration: time.Second,
				ProximityResolutionPositionMode:      "zone_position",
				ProximityResolutionStaleStateTTL:     time.Minute,
				Auth: config.AuthConfig{
					Enabled: false,
					Mode:    "none",
				},
			}, nil
		},
		newLogger: func(string) (*zap.Logger, func(), error) {
			return zap.NewNop(), func() {}, nil
		},
		openQueries: func(context.Context, string) (sqlcgen.Querier, runtimeCloser, error) {
			return nil, runtimeCloserFunc(func() error { return nil }), nil
		},
		newMQTT: func(*zap.Logger, string) (mqttRuntimeClient, error) {
			return &fakeRuntimeMQTT{}, nil
		},
		newAuthenticator: func(context.Context, config.AuthConfig) (auth.Authenticator, error) {
			return fakeAuthenticator{}, nil
		},
		loadRegistry: func(string) (*auth.Registry, error) {
			return nil, nil
		},
		resolveHubMetadata: func(context.Context, sqlcgen.Querier, config.Config) (hubmeta.Metadata, error) {
			return hubmeta.Metadata{HubID: "4f630dd4-e5f2-4398-9970-c63cad9bc109", Label: "hub-test"}, nil
		},
		newEventBus: func() *hub.EventBus { return nil },
		newService: func(*zap.Logger, sqlcgen.Querier, *hub.EventBus, hub.Config) (*hub.Service, error) {
			return &hub.Service{}, nil
		},
		eventPublisherHandle: func(mqttRuntimeClient) func(context.Context, hub.Event) error {
			return func(context.Context, hub.Event) error { return nil }
		},
		newRPCBridge: func(*zap.Logger, rpc.Publisher, rpc.Config) (rpcRuntimeBridge, error) {
			return fakeRuntimeRPCBridge{}, nil
		},
		newHTTPServer: func(_ string, handler http.Handler) httpServer {
			return &fakeHTTPServer{handler: handler}
		},
	}
}

func TestRunReturnsHubMetadataBootstrapFailure(t *testing.T) {
	t.Parallel()

	rt := stubRuntimeForTest(t)
	rt.resolveHubMetadata = func(context.Context, sqlcgen.Querier, config.Config) (hubmeta.Metadata, error) {
		return hubmeta.Metadata{}, errors.New("mismatch")
	}

	err := runWithRuntime(context.Background(), rt)
	if err == nil || !strings.Contains(err.Error(), "hub metadata init failed: mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

type fakeRuntimeMQTT struct {
	mu                   sync.Mutex
	filters              []string
	subscribeErrByFilter map[string]error
}

func (f *fakeRuntimeMQTT) PublishJSON(context.Context, string, any, bool) error { return nil }
func (f *fakeRuntimeMQTT) Subscribe(filter string, handler mqtt.MessageHandler) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.filters = append(f.filters, filter)
	if handler == nil {
		return errors.New("missing handler")
	}
	return f.subscribeErrByFilter[filter]
}
func (f *fakeRuntimeMQTT) AddOnConnectListener(func(context.Context)) {}
func (f *fakeRuntimeMQTT) Close() error                               { return nil }
func (f *fakeRuntimeMQTT) subscribed(filter string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, candidate := range f.filters {
		if candidate == filter {
			return true
		}
	}
	return false
}

type fakeRuntimeRPCBridge struct{}

func (fakeRuntimeRPCBridge) AvailableMethods(context.Context) (gen.RpcAvailableMethods, error) {
	return nil, nil
}
func (fakeRuntimeRPCBridge) Invoke(context.Context, gen.JsonRpcRequest) (json.RawMessage, bool, error) {
	return nil, false, nil
}
func (fakeRuntimeRPCBridge) Close() error { return nil }

type fakeAuthenticator struct{}

func (fakeAuthenticator) Authenticate(context.Context, string) (*auth.Principal, error) {
	return &auth.Principal{}, nil
}

type fakeHTTPServer struct {
	onListen       func(http.Handler)
	handler        http.Handler
	shutdown       chan struct{}
	shutdownCalled bool
}

func (f *fakeHTTPServer) ListenAndServe() error {
	if f.shutdown == nil {
		f.shutdown = make(chan struct{})
	}
	if f.onListen != nil {
		f.onListen(f.handler)
	}
	<-f.shutdown
	return http.ErrServerClosed
}

func (f *fakeHTTPServer) Shutdown(context.Context) error {
	f.shutdownCalled = true
	if f.shutdown != nil {
		close(f.shutdown)
	}
	return nil
}
