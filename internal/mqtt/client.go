package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/formation-res/open-rtls-hub/internal/observability"
	"go.uber.org/zap"
)

// MessageHandler handles a single inbound MQTT message.
type MessageHandler func(ctx context.Context, topic string, payload []byte) error

type subscription struct {
	filter  string
	handler MessageHandler
}

// Client manages MQTT connectivity, subscriptions, and publication for the
// hub.
type Client struct {
	logger        *zap.Logger
	BrokerURL     string
	inner         pahomqtt.Client
	mu            sync.RWMutex
	subscriptions []subscription
	onConnect     []func(context.Context)
}

// NewClient connects to the configured MQTT broker and prepares automatic
// resubscription behavior.
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
		observability.Global().RecordDependencyEvent(context.Background(), "mqtt", "connect", "success")
		c.resubscribe(client)
		c.runOnConnectHooks(context.Background())
	}
	opts.OnConnectionLost = func(_ pahomqtt.Client, err error) {
		observability.Global().RecordDependencyEvent(context.Background(), "mqtt", "connection_lost", "failure")
		c.logger.Warn("mqtt connection lost", zap.Any("context", context.Background()), zap.Error(err))
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

// AddOnConnectListener registers a callback that runs after successful broker
// connection and resubscription.
func (c *Client) AddOnConnectListener(fn func(context.Context)) {
	c.mu.Lock()
	c.onConnect = append(c.onConnect, fn)
	c.mu.Unlock()
	if c.inner != nil && c.inner.IsConnected() {
		fn(context.Background())
	}
}

// Close disconnects from the broker.
func (c *Client) Close() error {
	if c.inner != nil && c.inner.IsConnected() {
		c.inner.Disconnect(250)
	}
	return nil
}

// Subscribe registers a handler for the supplied topic filter.
func (c *Client) Subscribe(filter string, handler MessageHandler) error {
	c.mu.Lock()
	c.subscriptions = append(c.subscriptions, subscription{filter: filter, handler: handler})
	c.mu.Unlock()
	if c.inner == nil || !c.inner.IsConnected() {
		return nil
	}
	return c.subscribe(c.inner, filter, handler)
}

// PublishJSON marshals payload as JSON and publishes it with QoS 1.
func (c *Client) PublishJSON(ctx context.Context, topic string, payload any, retained bool) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return c.PublishRaw(ctx, topic, raw, retained)
}

// PublishRaw publishes the provided byte payload with QoS 1.
func (c *Client) PublishRaw(_ context.Context, topic string, payload []byte, retained bool) error {
	start := time.Now()
	token := c.inner.Publish(topic, 1, retained, payload)
	if !token.WaitTimeout(10 * time.Second) {
		observability.Global().RecordMQTTPublish(context.Background(), "timeout", time.Since(start))
		return fmt.Errorf("mqtt publish timed out for %s", topic)
	}
	err := token.Error()
	if err != nil {
		observability.Global().RecordMQTTPublish(context.Background(), "failure", time.Since(start))
		return err
	}
	observability.Global().RecordMQTTPublish(context.Background(), "success", time.Since(start))
	return nil
}

func (c *Client) resubscribe(client pahomqtt.Client) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, sub := range c.subscriptions {
		if err := c.subscribe(client, sub.filter, sub.handler); err != nil {
			observability.Global().RecordDependencyEvent(context.Background(), "mqtt", "subscribe", "failure")
			c.logger.Warn("mqtt subscribe failed", zap.Any("context", context.Background()), zap.Error(err), zap.String("filter", sub.filter))
		}
	}
}

func (c *Client) runOnConnectHooks(ctx context.Context) {
	c.mu.RLock()
	hooks := append([]func(context.Context){}, c.onConnect...)
	c.mu.RUnlock()
	for _, hook := range hooks {
		hook(ctx)
	}
}

func (c *Client) subscribe(client pahomqtt.Client, filter string, handler MessageHandler) error {
	token := client.Subscribe(filter, 1, func(_ pahomqtt.Client, msg pahomqtt.Message) {
		if err := handler(context.Background(), msg.Topic(), msg.Payload()); err != nil {
			observability.Global().RecordDependencyEvent(context.Background(), "mqtt", "handler", "failure")
			c.logger.Warn("mqtt handler failed", zap.Any("context", context.Background()), zap.Error(err), zap.String("topic", msg.Topic()))
		}
	})
	if !token.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("mqtt subscribe timed out for %s", filter)
	}
	return token.Error()
}

// TopicLocationPub returns the OMLOX MQTT topic for provider-supplied
// published locations.
func TopicLocationPub(providerID string) string {
	return fmt.Sprintf("/omlox/json/location_updates/pub/%s", providerID)
}

// TopicLocationLocal returns the OMLOX MQTT topic for local-coordinate
// location publication.
func TopicLocationLocal(providerID string) string {
	return fmt.Sprintf("/omlox/json/location_updates/local/%s", providerID)
}

// TopicLocationEPSG4326 returns the OMLOX MQTT topic for WGS84 location
// publication.
func TopicLocationEPSG4326(providerID string) string {
	return fmt.Sprintf("/omlox/json/location_updates/epsg4326/%s", providerID)
}

// TopicProximity returns the OMLOX MQTT topic for proximity updates.
func TopicProximity(source, providerID string) string {
	return fmt.Sprintf("/omlox/json/proximity_updates/%s/%s", source, providerID)
}

// TopicLocationPubWildcard returns the wildcard subscription topic for
// provider-supplied published locations.
func TopicLocationPubWildcard() string {
	return "/omlox/json/location_updates/pub/+"
}

// TopicProximityWildcard returns the wildcard subscription topic for proximity
// updates.
func TopicProximityWildcard() string {
	return "/omlox/json/proximity_updates/+/#"
}

// TopicFenceEvent returns the OMLOX MQTT topic for a fence event stream.
func TopicFenceEvent(fenceID string) string {
	return fmt.Sprintf("/omlox/json/fence_events/%s", fenceID)
}

// TopicFenceEventTrackable returns the OMLOX MQTT topic for fence events keyed
// by trackable.
func TopicFenceEventTrackable(trackableID string) string {
	return fmt.Sprintf("/omlox/json/fence_events/trackables/%s", trackableID)
}

// TopicFenceEventProvider returns the OMLOX MQTT topic for fence events keyed
// by provider.
func TopicFenceEventProvider(providerID string) string {
	return fmt.Sprintf("/omlox/json/fence_events/providers/%s", providerID)
}

// TopicTrackableMotionLocal returns the OMLOX MQTT topic for local-coordinate
// trackable motion updates.
func TopicTrackableMotionLocal(trackableID string) string {
	return fmt.Sprintf("/omlox/json/trackable_motions/local/%s", trackableID)
}

// TopicTrackableMotionEPSG4326 returns the OMLOX MQTT topic for WGS84
// trackable motion updates.
func TopicTrackableMotionEPSG4326(trackableID string) string {
	return fmt.Sprintf("/omlox/json/trackable_motions/epsg4326/%s", trackableID)
}

// TopicCollisionEventEPSG4326 returns the OMLOX MQTT topic for collision
// events. The OMLOX MQTT mapping only defines the WGS84 topic.
func TopicCollisionEventEPSG4326() string {
	return "/omlox/json/collision_events/epsg4326"
}

// TopicRPCAvailable returns the retained OMLOX MQTT topic for RPC method
// availability.
func TopicRPCAvailable(method string) string {
	return fmt.Sprintf("/omlox/jsonrpc/rpc/available/%s", method)
}

// TopicRPCAvailableWildcard returns the wildcard subscription topic for RPC
// method availability announcements.
func TopicRPCAvailableWildcard() string {
	return "/omlox/jsonrpc/rpc/available/+"
}

// TopicRPCRequest returns the OMLOX MQTT topic for RPC requests for a method.
func TopicRPCRequest(method string) string {
	return fmt.Sprintf("/omlox/jsonrpc/rpc/%s/request", method)
}

// TopicRPCRequestHandler returns the OMLOX MQTT topic for RPC requests routed
// to a specific handler.
func TopicRPCRequestHandler(method, handlerID string) string {
	return fmt.Sprintf("/omlox/jsonrpc/rpc/%s/request/%s", method, handlerID)
}

// TopicRPCResponse returns the OMLOX MQTT topic for RPC responses addressed to
// a caller.
func TopicRPCResponse(method, callerID string) string {
	return fmt.Sprintf("/omlox/jsonrpc/rpc/%s/response/%s", method, callerID)
}

// TopicRPCResponseWildcard returns the wildcard subscription topic for RPC
// responses.
func TopicRPCResponseWildcard() string {
	return "/omlox/jsonrpc/rpc/+/response/+"
}

// TopicRPCXCMDResponseBroadcast returns the OMLOX MQTT topic for XCMD broadcast
// messages emitted by method handlers.
func TopicRPCXCMDResponseBroadcast() string {
	return "/omlox/jsonrpc/rpc/com.omlox.core.xcmd/broadcast"
}
