package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"go.uber.org/zap"
)

type MessageHandler func(ctx context.Context, topic string, payload []byte) error

type subscription struct {
	filter  string
	handler MessageHandler
}

type Client struct {
	logger        *zap.Logger
	BrokerURL     string
	inner         pahomqtt.Client
	mu            sync.RWMutex
	subscriptions []subscription
}

func NewClient(logger *zap.Logger, brokerURL string) (*Client, error) {
	c := &Client{
		logger:    logger,
		BrokerURL: brokerURL,
	}
	opts := pahomqtt.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID(fmt.Sprintf("open-rtls-hub-%d", time.Now().UnixNano())).
		SetAutoReconnect(true).
		SetCleanSession(false).
		SetResumeSubs(true).
		SetOrderMatters(false)
	opts.OnConnect = func(client pahomqtt.Client) {
		c.logger.Info("mqtt connected", zap.String("broker", brokerURL))
		c.resubscribe(client)
	}
	opts.OnConnectionLost = func(_ pahomqtt.Client, err error) {
		c.logger.Warn("mqtt connection lost", zap.Error(err))
	}
	c.inner = pahomqtt.NewClient(opts)
	token := c.inner.Connect()
	if !token.WaitTimeout(15 * time.Second) {
		return nil, fmt.Errorf("mqtt connect timed out")
	}
	if err := token.Error(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) Close() error {
	if c.inner != nil && c.inner.IsConnected() {
		c.inner.Disconnect(250)
	}
	return nil
}

func (c *Client) Subscribe(filter string, handler MessageHandler) error {
	c.mu.Lock()
	c.subscriptions = append(c.subscriptions, subscription{filter: filter, handler: handler})
	c.mu.Unlock()
	if c.inner == nil || !c.inner.IsConnected() {
		return nil
	}
	return c.subscribe(c.inner, filter, handler)
}

func (c *Client) PublishJSON(ctx context.Context, topic string, payload any, retained bool) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return c.PublishRaw(ctx, topic, raw, retained)
}

func (c *Client) PublishRaw(_ context.Context, topic string, payload []byte, retained bool) error {
	token := c.inner.Publish(topic, 1, retained, payload)
	if !token.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("mqtt publish timed out for %s", topic)
	}
	return token.Error()
}

func (c *Client) resubscribe(client pahomqtt.Client) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, sub := range c.subscriptions {
		if err := c.subscribe(client, sub.filter, sub.handler); err != nil {
			c.logger.Warn("mqtt subscribe failed", zap.Error(err), zap.String("filter", sub.filter))
		}
	}
}

func (c *Client) subscribe(client pahomqtt.Client, filter string, handler MessageHandler) error {
	token := client.Subscribe(filter, 1, func(_ pahomqtt.Client, msg pahomqtt.Message) {
		if err := handler(context.Background(), msg.Topic(), msg.Payload()); err != nil {
			c.logger.Warn("mqtt handler failed", zap.Error(err), zap.String("topic", msg.Topic()))
		}
	})
	if !token.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("mqtt subscribe timed out for %s", filter)
	}
	return token.Error()
}

func TopicLocationPub(providerID string) string {
	return fmt.Sprintf("/omlox/json/location_updates/pub/%s", providerID)
}

func TopicLocationLocal(providerID string) string {
	return fmt.Sprintf("/omlox/json/location_updates/local/%s", providerID)
}

func TopicLocationEPSG4326(providerID string) string {
	return fmt.Sprintf("/omlox/json/location_updates/epsg4326/%s", providerID)
}

func TopicProximity(source, providerID string) string {
	return fmt.Sprintf("/omlox/json/proximity_updates/%s/%s", source, providerID)
}

func TopicLocationPubWildcard() string {
	return "/omlox/json/location_updates/pub/+"
}

func TopicProximityWildcard() string {
	return "/omlox/json/proximity_updates/+/#"
}

func TopicFenceEvent(fenceID string) string {
	return fmt.Sprintf("/omlox/json/fence_events/%s", fenceID)
}

func TopicFenceEventTrackable(trackableID string) string {
	return fmt.Sprintf("/omlox/json/fence_events/trackables/%s", trackableID)
}

func TopicFenceEventProvider(providerID string) string {
	return fmt.Sprintf("/omlox/json/fence_events/providers/%s", providerID)
}

func TopicTrackableMotionLocal(trackableID string) string {
	return fmt.Sprintf("/omlox/json/trackable_motions/local/%s", trackableID)
}

func TopicTrackableMotionEPSG4326(trackableID string) string {
	return fmt.Sprintf("/omlox/json/trackable_motions/epsg4326/%s", trackableID)
}

func TopicRPCAvailable(method string) string {
	return fmt.Sprintf("/omlox/jsonrpc/rpc/available/%s", method)
}

func TopicRPCAvailableWildcard() string {
	return "/omlox/jsonrpc/rpc/available/+"
}

func TopicRPCRequest(method string) string {
	return fmt.Sprintf("/omlox/jsonrpc/rpc/%s/request", method)
}

func TopicRPCRequestHandler(method, handlerID string) string {
	return fmt.Sprintf("/omlox/jsonrpc/rpc/%s/request/%s", method, handlerID)
}

func TopicRPCResponse(method, callerID string) string {
	return fmt.Sprintf("/omlox/jsonrpc/rpc/%s/response/%s", method, callerID)
}

func TopicRPCResponseWildcard() string {
	return "/omlox/jsonrpc/rpc/+/response/+"
}
