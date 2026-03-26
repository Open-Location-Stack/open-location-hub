package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/auth"
	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/formation-res/open-rtls-hub/internal/mqtt"
	"go.uber.org/zap"
)

type fakeMQTT struct {
	mu       sync.RWMutex
	handlers map[string]mqtt.MessageHandler
	publish  func(topic string, payload any, retained bool)
}

func (f *fakeMQTT) PublishJSON(_ context.Context, topic string, payload any, retained bool) error {
	f.mu.RLock()
	publish := f.publish
	f.mu.RUnlock()
	if publish != nil {
		publish(topic, payload, retained)
	}
	return nil
}

func (f *fakeMQTT) Subscribe(filter string, handler mqtt.MessageHandler) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.handlers == nil {
		f.handlers = map[string]mqtt.MessageHandler{}
	}
	f.handlers[filter] = handler
	return nil
}

func (f *fakeMQTT) setPublish(fn func(topic string, payload any, retained bool)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publish = fn
}

func (f *fakeMQTT) handler(filter string) mqtt.MessageHandler {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.handlers[filter]
}

func TestAvailableMethodsTracksLocalAndExternalAnnouncements(t *testing.T) {
	fake := &fakeMQTT{}
	bridge, err := NewBridge(zap.NewNop(), fake, Config{Timeout: time.Second})
	if err != nil {
		t.Fatalf("bridge init failed: %v", err)
	}
	t.Cleanup(func() { _ = bridge.Close() })

	payload, _ := json.Marshal(map[string]any{
		"id":          "handler-a",
		"method_name": "com.vendor.echo",
	})
	if err := fake.handler(mqtt.TopicRPCAvailableWildcard())(context.Background(), mqtt.TopicRPCAvailable("com.vendor.echo"), payload); err != nil {
		t.Fatalf("available handler failed: %v", err)
	}

	methods, err := bridge.AvailableMethods(context.Background())
	if err != nil {
		t.Fatalf("available methods failed: %v", err)
	}
	if len(methods["com.omlox.ping"].HandlerId) != 1 || methods["com.omlox.ping"].HandlerId[0] != defaultHandlerID {
		t.Fatalf("expected built-in method announcement, got %#v", methods["com.omlox.ping"])
	}
	if len(methods["com.vendor.echo"].HandlerId) != 1 || methods["com.vendor.echo"].HandlerId[0] != "handler-a" {
		t.Fatalf("unexpected external methods: %#v", methods["com.vendor.echo"])
	}
}

func TestInvokeReturnsLocalPingSuccess(t *testing.T) {
	bridge, err := NewBridge(zap.NewNop(), nil, Config{Timeout: time.Second})
	if err != nil {
		t.Fatalf("bridge init failed: %v", err)
	}
	t.Cleanup(func() { _ = bridge.Close() })

	request := testRequest(t, "com.omlox.ping")
	mode := gen.UnderscoreReturnFirstSuccess
	request.Params = &gen.JsonRpcRequest_Params{UnderscoreAggregation: &mode}
	raw, notifyOnly, err := bridge.Invoke(context.Background(), request)
	if err != nil {
		t.Fatalf("invoke failed: %v", err)
	}
	if notifyOnly {
		t.Fatal("expected request-response invocation")
	}
	assertResultContains(t, raw, "message", "pong")
}

func TestInvokeBridgesExternalMethodAndReturnsFirstSuccess(t *testing.T) {
	fake := &fakeMQTT{}
	bridge, err := NewBridge(zap.NewNop(), fake, Config{Timeout: time.Second})
	if err != nil {
		t.Fatalf("bridge init failed: %v", err)
	}
	t.Cleanup(func() { _ = bridge.Close() })

	announce(t, fake, "com.vendor.echo", "handler-a")
	fake.setPublish(func(topic string, payload any, retained bool) {
		if retained {
			return
		}
		req := payload.(gen.JsonRpcRequest)
		callerID := *req.Params.UnderscoreCallerId
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      rawID(t, req.Id),
			"result":  map[string]any{"ok": true},
		}
		raw, _ := json.Marshal(response)
		_ = fake.handler(mqtt.TopicRPCResponseWildcard())(context.Background(), mqtt.TopicRPCResponse(req.Method, callerID), raw)
		if topic != mqtt.TopicRPCRequest("com.vendor.echo") {
			t.Fatalf("unexpected publish topic %s", topic)
		}
	})

	request := testRequest(t, "com.vendor.echo")
	mode := gen.UnderscoreReturnFirstSuccess
	request.Params = &gen.JsonRpcRequest_Params{UnderscoreAggregation: &mode}
	raw, notifyOnly, err := bridge.Invoke(context.Background(), request)
	if err != nil {
		t.Fatalf("invoke failed: %v", err)
	}
	if notifyOnly {
		t.Fatal("expected request-response invocation")
	}
	assertResultContains(t, raw, "ok", true)
}

func TestInvokeRejectsInvalidAggregationCombination(t *testing.T) {
	bridge, err := NewBridge(zap.NewNop(), nil, Config{Timeout: time.Second})
	if err != nil {
		t.Fatalf("bridge init failed: %v", err)
	}
	t.Cleanup(func() { _ = bridge.Close() })

	request := testRequest(t, "com.omlox.ping")
	mode := gen.UnderscoreReturnFirstSuccess
	handlerID := "handler-a"
	request.Params = &gen.JsonRpcRequest_Params{
		UnderscoreAggregation: &mode,
		UnderscoreHandlerId:   &handlerID,
	}
	raw, _, err := bridge.Invoke(context.Background(), request)
	if err != nil {
		t.Fatalf("invoke failed: %v", err)
	}
	assertErrorCode(t, raw, errCodeInvalidParams)
}

func TestInvokeAllWithinTimeoutCollectsAllResponses(t *testing.T) {
	fake := &fakeMQTT{}
	bridge, err := NewBridge(zap.NewNop(), fake, Config{Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("bridge init failed: %v", err)
	}
	t.Cleanup(func() { _ = bridge.Close() })

	announce(t, fake, "com.vendor.echo", "handler-a")
	fake.setPublish(func(_ string, payload any, retained bool) {
		if retained {
			return
		}
		req := payload.(gen.JsonRpcRequest)
		callerID := *req.Params.UnderscoreCallerId
		for _, body := range []map[string]any{
			{"jsonrpc": "2.0", "id": rawID(t, req.Id), "result": map[string]any{"from": "a"}},
			{"jsonrpc": "2.0", "id": rawID(t, req.Id), "result": map[string]any{"from": "b"}},
		} {
			raw, _ := json.Marshal(body)
			_ = fake.handler(mqtt.TopicRPCResponseWildcard())(context.Background(), mqtt.TopicRPCResponse(req.Method, callerID), raw)
		}
	})

	request := testRequest(t, "com.vendor.echo")
	mode := gen.UnderscoreAllWithinTimeout
	timeoutMs := float32(10)
	request.Params = &gen.JsonRpcRequest_Params{
		UnderscoreAggregation: &mode,
		UnderscoreTimeout:     &timeoutMs,
	}
	raw, _, err := bridge.Invoke(context.Background(), request)
	if err != nil {
		t.Fatalf("invoke failed: %v", err)
	}
	var body struct {
		Result struct {
			Responses []json.RawMessage `json:"responses"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(body.Result.Responses) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(body.Result.Responses))
	}
}

func TestInvokeReturnFirstErrorPrefersErrorPayload(t *testing.T) {
	fake := &fakeMQTT{}
	bridge, err := NewBridge(zap.NewNop(), fake, Config{Timeout: time.Second})
	if err != nil {
		t.Fatalf("bridge init failed: %v", err)
	}
	t.Cleanup(func() { _ = bridge.Close() })

	announce(t, fake, "com.vendor.echo", "handler-a")
	fake.setPublish(func(_ string, payload any, retained bool) {
		if retained {
			return
		}
		req := payload.(gen.JsonRpcRequest)
		callerID := *req.Params.UnderscoreCallerId
		raw, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      rawID(t, req.Id),
			"error":   map[string]any{"code": -32090, "message": "boom"},
		})
		_ = fake.handler(mqtt.TopicRPCResponseWildcard())(context.Background(), mqtt.TopicRPCResponse(req.Method, callerID), raw)
	})

	request := testRequest(t, "com.vendor.echo")
	mode := gen.UnderscoreReturnFirstError
	request.Params = &gen.JsonRpcRequest_Params{UnderscoreAggregation: &mode}
	raw, _, err := bridge.Invoke(context.Background(), request)
	if err != nil {
		t.Fatalf("invoke failed: %v", err)
	}
	assertErrorCode(t, raw, -32090)
}

func TestInvokeRejectsUnauthorizedMethod(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "permissions.yaml")
	if err := os.WriteFile(path, []byte(`
reader@example.com:
  /v2/rpc:
    - UPDATE_ANY
  /v2/rpc/available:
    - READ_ANY
  rpc:
    discover: true
    invoke:
      com.omlox.ping: true
`), 0o644); err != nil {
		t.Fatalf("write permissions failed: %v", err)
	}
	registry, err := auth.LoadRegistry(path)
	if err != nil {
		t.Fatalf("load registry failed: %v", err)
	}
	bridge, err := NewBridge(zap.NewNop(), nil, Config{Timeout: time.Second, Authorizer: registry})
	if err != nil {
		t.Fatalf("bridge init failed: %v", err)
	}
	t.Cleanup(func() { _ = bridge.Close() })

	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Roles: []string{"reader@example.com"}})
	raw, _, err := bridge.Invoke(ctx, testRequest(t, "com.omlox.identify"))
	if err == nil {
		t.Fatal("expected authorization error")
	}
	if raw != nil {
		t.Fatalf("expected no json-rpc payload on authorization failure, got %s", string(raw))
	}
}

func TestInvokeXCMDWithoutAdapterReturnsDeterministicError(t *testing.T) {
	bridge, err := NewBridge(zap.NewNop(), nil, Config{Timeout: time.Second})
	if err != nil {
		t.Fatalf("bridge init failed: %v", err)
	}
	t.Cleanup(func() { _ = bridge.Close() })

	request := testRequest(t, "com.omlox.core.xcmd")
	mode := gen.UnderscoreReturnFirstError
	request.Params = &gen.JsonRpcRequest_Params{UnderscoreAggregation: &mode}
	raw, _, err := bridge.Invoke(context.Background(), request)
	if err != nil {
		t.Fatalf("invoke failed: %v", err)
	}
	assertErrorCode(t, raw, errCodeUnsupported)
}

func announce(t *testing.T, fake *fakeMQTT, method, handlerID string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"id":          handlerID,
		"method_name": method,
	})
	if err := fake.handler(mqtt.TopicRPCAvailableWildcard())(context.Background(), mqtt.TopicRPCAvailable(method), payload); err != nil {
		t.Fatalf("announce failed: %v", err)
	}
}

func testRequest(t *testing.T, method string) gen.JsonRpcRequest {
	t.Helper()
	var id gen.JsonRpcRequest_Id
	if err := id.FromJsonRpcRequestId0("request-1"); err != nil {
		t.Fatalf("id setup failed: %v", err)
	}
	return gen.JsonRpcRequest{
		Id:      &id,
		Jsonrpc: "2.0",
		Method:  method,
		Params:  &gen.JsonRpcRequest_Params{},
	}
}

func rawID(t *testing.T, id *gen.JsonRpcRequest_Id) any {
	t.Helper()
	raw, err := id.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal id failed: %v", err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal id failed: %v", err)
	}
	return out
}

func assertResultContains(t *testing.T, raw json.RawMessage, key string, want any) {
	t.Helper()
	var body struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if got := body.Result[key]; got != want {
		t.Fatalf("unexpected result[%s]: got=%v want=%v body=%s", key, got, want, string(raw))
	}
}

func assertErrorCode(t *testing.T, raw json.RawMessage, want int) {
	t.Helper()
	var body struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if body.Error.Code != want {
		t.Fatalf("unexpected error code: got=%d want=%d body=%s", body.Error.Code, want, string(raw))
	}
}
