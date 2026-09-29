package mqtt

import (
	"context"
	"errors"
	"github.com/eclipse/paho.golang/packets"
	"github.com/eclipse/paho.golang/paho"
	"testing"
	"time"
)

type fakeConnection struct {
	packet        *paho.Publish
	err           error
	reason        byte
	subscriptions int
	closed        bool
}

func (f *fakeConnection) Publish(_ context.Context, p *paho.Publish) (*paho.PublishResponse, error) {
	f.packet = p
	return &paho.PublishResponse{ReasonCode: f.reason}, f.err
}
func (f *fakeConnection) Subscribe(context.Context, *paho.Subscribe) (*paho.Suback, error) {
	f.subscriptions++
	return &paho.Suback{Reasons: []byte{f.reason}}, f.err
}
func (f *fakeConnection) Disconnect(context.Context) error { f.closed = true; return nil }

func TestMQTT5AvailabilityExpiry(t *testing.T) {
	f := &fakeConnection{}
	c := &Client{inner: f}
	for _, tc := range []struct {
		topic           string
		retain, expires bool
	}{{TopicRPCAvailable("test"), true, true}, {TopicLocationEPSG4326("provider"), false, false}, {TopicRPCAvailable("test"), false, false}} {
		if err := c.PublishJSON(context.Background(), tc.topic, map[string]string{"id": "handler"}, tc.retain); err != nil {
			t.Fatal(err)
		}
		p := f.packet
		if p.QoS != 1 || p.Retain != tc.retain {
			t.Fatalf("wrong publication: %+v", p)
		}
		if tc.expires {
			if p.Properties == nil || p.Properties.MessageExpiry == nil || *p.Properties.MessageExpiry != 120 {
				t.Fatal("missing 120-second MQTT 5 expiry")
			}
		} else if p.Properties != nil {
			t.Fatal("unexpected expiry")
		}
	}
}
func TestBrokerFailuresAreReturned(t *testing.T) {
	for _, tc := range []struct {
		err    error
		reason byte
	}{{errors.New("broker failure"), 0}, {nil, 0x87}} {
		c := &Client{inner: &fakeConnection{err: tc.err, reason: tc.reason}}
		c.connected.Store(true)
		if c.PublishRaw(context.Background(), "topic", nil, false) == nil {
			t.Fatal("expected publish failure")
		}
		if c.Subscribe("topic", func(context.Context, string, []byte) error { return nil }) == nil {
			t.Fatal("expected subscribe failure")
		}
	}
	if (&Client{}).PublishJSON(context.Background(), "topic", make(chan int), false) == nil {
		t.Fatal("expected marshal failure")
	}
}
func TestResubscribeDoesNotDuplicateHandlers(t *testing.T) {
	f := &fakeConnection{}
	c := &Client{inner: f}
	called := make(chan string, 2)
	if err := c.Subscribe("topic/+", func(_ context.Context, topic string, payload []byte) error {
		called <- topic + ":" + string(payload)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if f.subscriptions != 0 {
		t.Fatal("should defer broker subscription while disconnected")
	}
	c.resubscribe(f)
	c.resubscribe(f)
	c.router.Route(&packets.Publish{Topic: "topic/1", Payload: []byte("hello"), Properties: &packets.Properties{}})
	select {
	case v := <-called:
		if v != "topic/1:hello" {
			t.Fatal(v)
		}
	case <-time.After(time.Second):
		t.Fatal("no routed message")
	}
	select {
	case <-called:
		t.Fatal("duplicate callback")
	case <-time.After(10 * time.Millisecond):
	}
	c.connected.Store(true)
	hook := false
	c.AddOnConnectListener(func(context.Context) { hook = true })
	if !hook {
		t.Fatal("missing immediate hook")
	}
	if err := c.Close(); err != nil || !f.closed {
		t.Fatal("did not close")
	}
}
func TestConnectionConfigPreservesTLSAndCredentials(t *testing.T) {
	for _, scheme := range []string{"tls", "ssl", "mqtts", "wss"} {
		cfg, err := connectionConfig(scheme + "://user:secret@example.test:8883")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ServerUrls[0].User != nil || cfg.ConnectUsername != "user" || string(cfg.ConnectPassword) != "secret" {
			t.Fatal("credentials not transferred")
		}
		if cfg.ServerUrls[0].Scheme == "mqtt" {
			t.Fatal("TLS downgraded")
		}
	}
	if _, err := connectionConfig("http://example.test"); err == nil {
		t.Fatal("accepted invalid scheme")
	}
}
func TestTopicMapping(t *testing.T) {
	if TopicLocationPub("abc") != "/omlox/json/location_updates/pub/abc" || TopicRPCResponse("echo", "caller") != "/omlox/jsonrpc/rpc/echo/response/caller" {
		t.Fatal("wrong topic")
	}
}
