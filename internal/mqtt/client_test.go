package mqtt

import (
	"context"
	"errors"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"go.uber.org/zap"
)

func TestAddOnConnectListenerRunsImmediatelyWhenConnected(t *testing.T) {
	t.Parallel()

	client := &Client{inner: &fakePahoClient{connected: true}}

	called := make(chan struct{}, 1)
	client.AddOnConnectListener(func(context.Context) {
		called <- struct{}{}
	})

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("expected listener to run immediately")
	}
}

func TestSubscribeDefersBrokerSubscriptionUntilConnected(t *testing.T) {
	t.Parallel()

	client := &Client{inner: &fakePahoClient{connected: false}}

	called := false
	err := client.Subscribe("topic/+", func(context.Context, string, []byte) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe returned error: %v", err)
	}
	if called {
		t.Fatal("message handler should not run during registration")
	}
	if len(client.subscriptions) != 1 {
		t.Fatalf("expected one registered subscription, got %d", len(client.subscriptions))
	}
}

func TestSubscribeReturnsBrokerErrorWhenConnected(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("subscribe failed")
	inner := &fakePahoClient{
		connected: true,
		subscribeToken: fakeToken{
			waitTimeout: true,
			err:         wantErr,
		},
	}
	client := &Client{inner: inner}

	err := client.Subscribe("topic/+", func(context.Context, string, []byte) error { return nil })
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected broker error, got %v", err)
	}
}

func TestPublishJSONReturnsMarshalError(t *testing.T) {
	t.Parallel()

	client := &Client{}
	err := client.PublishJSON(context.Background(), "topic", map[string]any{"invalid": make(chan int)}, false)
	if err == nil {
		t.Fatal("expected marshal error")
	}
}

func TestPublishRawTimeoutAndError(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		t.Parallel()

		client := &Client{inner: &fakePahoClient{
			publishToken: fakeToken{waitTimeout: false},
		}}
		err := client.PublishRaw(context.Background(), "topic", []byte("payload"), false)
		if err == nil || err.Error() != "mqtt publish timed out for topic" {
			t.Fatalf("unexpected timeout error: %v", err)
		}
	})

	t.Run("broker error", func(t *testing.T) {
		t.Parallel()

		wantErr := errors.New("publish failed")
		client := &Client{inner: &fakePahoClient{
			publishToken: fakeToken{waitTimeout: true, err: wantErr},
		}}
		err := client.PublishRaw(context.Background(), "topic", []byte("payload"), true)
		if !errors.Is(err, wantErr) {
			t.Fatalf("expected broker error, got %v", err)
		}
	})
}

func TestResubscribeRunsHandlersAndHooksOnConnect(t *testing.T) {
	t.Parallel()

	inner := &fakePahoClient{
		connected:      true,
		subscribeToken: fakeToken{waitTimeout: true},
	}
	client := &Client{
		logger: zap.NewNop(),
		inner:  inner,
		subscriptions: []subscription{{
			filter: "topic/+",
			handler: func(_ context.Context, topic string, payload []byte) error {
				if topic != "topic/1" || string(payload) != "hello" {
					t.Fatalf("unexpected message delivered: topic=%s payload=%s", topic, string(payload))
				}
				return nil
			},
		}},
	}
	hooks := make(chan struct{}, 2)
	client.AddOnConnectListener(func(context.Context) {
		hooks <- struct{}{}
	})

	client.resubscribe(inner)
	if inner.lastSubscriptionHandler == nil {
		t.Fatal("expected broker subscribe callback to be installed")
	}
	inner.lastSubscriptionHandler(inner, fakeMessage{topic: "topic/1", payload: []byte("hello")})

	client.runOnConnectHooks(context.Background())
	select {
	case <-hooks:
	case <-time.After(time.Second):
		t.Fatal("expected on-connect hook to run")
	}
}

func TestSubscribeTimeoutReturnsContextualError(t *testing.T) {
	t.Parallel()

	client := &Client{
		logger: zap.NewNop(),
		inner:  &fakePahoClient{},
	}
	err := client.subscribe(client.inner, "topic/+", func(context.Context, string, []byte) error { return nil })
	if err == nil || err.Error() != "mqtt subscribe timed out for topic/+" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCloseDisconnectsConnectedClient(t *testing.T) {
	t.Parallel()

	inner := &fakePahoClient{connected: true}
	client := &Client{inner: inner}

	if err := client.Close(); err != nil {
		t.Fatalf("close returned error: %v", err)
	}
	if inner.disconnectQuiesce != 250 {
		t.Fatalf("unexpected disconnect quiesce: %d", inner.disconnectQuiesce)
	}
}

func TestTopicMapping(t *testing.T) {
	t.Parallel()

	if got := TopicLocationPub("abc"); got != "/omlox/json/location_updates/pub/abc" {
		t.Fatalf("unexpected topic: %s", got)
	}
	if got := TopicProximity("src", "abc"); got != "/omlox/json/proximity_updates/src/abc" {
		t.Fatalf("unexpected topic: %s", got)
	}
	if got := TopicRPCResponse("echo", "caller"); got != "/omlox/jsonrpc/rpc/echo/response/caller" {
		t.Fatalf("unexpected response topic: %s", got)
	}
}

type fakePahoClient struct {
	connected               bool
	publishToken            fakeToken
	subscribeToken          fakeToken
	lastSubscriptionHandler pahomqtt.MessageHandler
	disconnectQuiesce       uint
}

func (f *fakePahoClient) IsConnected() bool      { return f.connected }
func (f *fakePahoClient) IsConnectionOpen() bool { return f.connected }
func (f *fakePahoClient) Connect() pahomqtt.Token {
	return fakeToken{waitTimeout: true}
}
func (f *fakePahoClient) Disconnect(quiesce uint) {
	f.disconnectQuiesce = quiesce
}
func (f *fakePahoClient) Publish(string, byte, bool, interface{}) pahomqtt.Token {
	return f.publishToken
}
func (f *fakePahoClient) Subscribe(_ string, _ byte, callback pahomqtt.MessageHandler) pahomqtt.Token {
	f.lastSubscriptionHandler = callback
	return f.subscribeToken
}
func (f *fakePahoClient) SubscribeMultiple(map[string]byte, pahomqtt.MessageHandler) pahomqtt.Token {
	return fakeToken{waitTimeout: true}
}
func (f *fakePahoClient) Unsubscribe(...string) pahomqtt.Token {
	return fakeToken{waitTimeout: true}
}
func (f *fakePahoClient) AddRoute(string, pahomqtt.MessageHandler) {}
func (f *fakePahoClient) OptionsReader() pahomqtt.ClientOptionsReader {
	return pahomqtt.ClientOptionsReader{}
}

type fakeToken struct {
	waitTimeout bool
	err         error
}

func (f fakeToken) Wait() bool                     { return f.waitTimeout }
func (f fakeToken) WaitTimeout(time.Duration) bool { return f.waitTimeout }
func (f fakeToken) Done() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
func (f fakeToken) Error() error { return f.err }

type fakeMessage struct {
	topic   string
	payload []byte
}

func (m fakeMessage) Duplicate() bool   { return false }
func (m fakeMessage) Qos() byte         { return 1 }
func (m fakeMessage) Retained() bool    { return false }
func (m fakeMessage) Topic() string     { return m.topic }
func (m fakeMessage) MessageID() uint16 { return 1 }
func (m fakeMessage) Payload() []byte   { return m.payload }
func (m fakeMessage) Ack()              {}
