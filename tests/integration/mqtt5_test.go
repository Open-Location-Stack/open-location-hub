package integration

import (
	"context"
	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/formation-res/open-location-hub/internal/mqtt"
	"net/url"
	"testing"
	"time"
)

func TestMQTT5RetainedAvailabilityExpiry(t *testing.T) {
	_, _, brokerURL := startHubNoAuth(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	u, err := url.Parse(brokerURL)
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan *paho.Publish, 8)
	conn, err := autopaho.NewConnection(ctx, autopaho.ClientConfig{ServerUrls: []*url.URL{u}, KeepAlive: 10, ClientConfig: paho.ClientConfig{ClientID: "expiry-test", OnPublishReceived: []func(paho.PublishReceived) (bool, error){func(p paho.PublishReceived) (bool, error) {
		select {
		case received <- p.Packet:
		default:
		}
		return true, nil
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Disconnect(context.Background()) }()
	if err = conn.AwaitConnection(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = conn.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: mqtt.TopicRPCAvailable("com.omlox.ping"), QoS: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case packet := <-received:
		if !packet.Retain {
			t.Fatal("availability was not retained")
		}
		if packet.Properties == nil || packet.Properties.MessageExpiry == nil || *packet.Properties.MessageExpiry == 0 || *packet.Properties.MessageExpiry > 120 {
			t.Fatalf("missing bounded MQTT 5 availability expiry: %+v", packet.Properties)
		}
	case <-ctx.Done():
		t.Fatal("no retained availability announcement")
	}
}
