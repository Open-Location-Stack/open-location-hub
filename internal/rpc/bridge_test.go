package rpc

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/formation-res/open-rtls-hub/internal/mqtt"
	"go.uber.org/zap"
)

type fakeMQTT struct {
	handlers map[string]mqtt.MessageHandler
	publish  func(topic string, payload any)
}

func (f *fakeMQTT) PublishJSON(_ context.Context, topic string, payload any, _ bool) error {
	if f.publish != nil {
		f.publish(topic, payload)
	}
	return nil
}

func (f *fakeMQTT) Subscribe(filter string, handler mqtt.MessageHandler) error {
	if f.handlers == nil {
		f.handlers = map[string]mqtt.MessageHandler{}
	}
	f.handlers[filter] = handler
	return nil
}

func TestAvailableMethodsTracksAnnouncements(t *testing.T) {
	fake := &fakeMQTT{}
	bridge, err := NewBridge(zap.NewNop(), fake, time.Second)
	if err != nil {
		t.Fatalf("bridge init failed: %v", err)
	}

	payload, _ := json.Marshal(gen.RpcAvailableMethodsEntry{HandlerId: []string{"handler-a"}})
	if err := fake.handlers[mqtt.TopicRPCAvailableWildcard()](context.Background(), mqtt.TopicRPCAvailable("com.omlox.ping"), payload); err != nil {
		t.Fatalf("available handler failed: %v", err)
	}

	methods := bridge.AvailableMethods()
	if len(methods["com.omlox.ping"].HandlerId) != 1 || methods["com.omlox.ping"].HandlerId[0] != "handler-a" {
		t.Fatalf("unexpected methods: %#v", methods)
	}
}

func TestInvokeReturnsFirstSuccess(t *testing.T) {
	fake := &fakeMQTT{}
	bridge, err := NewBridge(zap.NewNop(), fake, time.Second)
	if err != nil {
		t.Fatalf("bridge init failed: %v", err)
	}
	fake.publish = func(topic string, payload any) {
		req := payload.(gen.JsonRpcRequest)
		callerID := *req.Params.UnderscoreCallerId
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      req.Id,
			"result":  map[string]any{"ok": true},
		}
		raw, _ := json.Marshal(response)
		_ = fake.handlers[mqtt.TopicRPCResponseWildcard()](context.Background(), mqtt.TopicRPCResponse(req.Method, callerID), raw)
	}

	var id gen.JsonRpcRequest_Id
	if err := id.FromJsonRpcRequestId0("request-1"); err != nil {
		t.Fatalf("id setup failed: %v", err)
	}
	request := gen.JsonRpcRequest{
		Id:      &id,
		Jsonrpc: "2.0",
		Method:  "com.omlox.ping",
		Params:  &gen.JsonRpcRequest_Params{},
	}
	raw, notifyOnly, err := bridge.Invoke(context.Background(), request)
	if err != nil {
		t.Fatalf("invoke failed: %v", err)
	}
	if notifyOnly {
		t.Fatal("expected request-response invocation")
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if _, ok := body["result"]; !ok {
		t.Fatalf("expected success response, got %#v", body)
	}
}
