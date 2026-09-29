package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/formation-res/open-location-hub/internal/observability"
	"go.uber.org/zap"
)

// MessageHandler handles a single inbound MQTT message.
type MessageHandler func(context.Context, string, []byte) error

type subscription struct {
	filter  string
	handler MessageHandler
}
type mqttConnection interface {
	Publish(context.Context, *paho.Publish) (*paho.PublishResponse, error)
	Subscribe(context.Context, *paho.Subscribe) (*paho.Suback, error)
	Disconnect(context.Context) error
}

// Client manages MQTT 5 connectivity, subscriptions and publication.
type Client struct {
	logger        *zap.Logger
	BrokerURL     string
	inner         mqttConnection
	router        *paho.StandardRouter
	cancel        context.CancelFunc
	dispatcher    *messageDispatcher
	connected     atomic.Bool
	mu            sync.RWMutex
	subscriptions []subscription
	onConnect     []func(context.Context)
}

// NewClient connects using MQTT 5, starts the bounded inbound handler pool,
// and restores subscriptions after reconnection.
func NewClient(logger *zap.Logger, brokerURL string, handlers HandlerConfig) (*Client, error) {
	cfg, err := connectionConfig(brokerURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{logger: logger, BrokerURL: brokerURL, router: paho.NewStandardRouter(), cancel: cancel}
	c.dispatcher = newMessageDispatcher(ctx, logger, handlers)
	cfg.ClientConfig = paho.ClientConfig{ClientID: fmt.Sprintf("open-location-hub-%d", time.Now().UnixNano()), Router: c.router}
	cfg.OnConnectionUp = func(manager *autopaho.ConnectionManager, _ *paho.Connack) {
		c.connected.Store(true)
		observability.Global().RecordDependencyEvent(ctx, "mqtt", "connect", "success")
		go func() { c.resubscribe(manager); c.runOnConnectHooks(ctx) }()
	}
	cfg.OnConnectionDown = func() bool {
		c.connected.Store(false)
		observability.Global().RecordDependencyEvent(ctx, "mqtt", "connection_lost", "failure")
		return true
	}
	manager, err := autopaho.NewConnection(ctx, cfg)
	if err != nil {
		cancel()
		return nil, err
	}
	c.inner = manager
	wait, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	if err = manager.AwaitConnection(wait); err != nil {
		cancel()
		return nil, fmt.Errorf("mqtt connection failed: %w", err)
	}
	return c, nil
}

func connectionConfig(brokerURL string) (autopaho.ClientConfig, error) {
	u, err := url.Parse(brokerURL)
	if err != nil || u.Host == "" {
		return autopaho.ClientConfig{}, fmt.Errorf("invalid MQTT broker URL")
	}
	switch u.Scheme {
	case "tcp":
		u.Scheme = "mqtt"
	case "ssl":
		u.Scheme = "tls"
	case "mqtt", "tls", "mqtts", "ws", "wss":
	default:
		return autopaho.ClientConfig{}, fmt.Errorf("unsupported MQTT broker URL scheme")
	}
	cfg := autopaho.ClientConfig{KeepAlive: 30, CleanStartOnInitialConnection: true, SessionExpiryInterval: 120, ConnectTimeout: 10 * time.Second}
	if u.User != nil {
		cfg.ConnectUsername = u.User.Username()
		password, _ := u.User.Password()
		cfg.ConnectPassword = []byte(password)
		u.User = nil
	}
	cfg.ServerUrls = []*url.URL{u}
	return cfg, nil
}

// AddOnConnectListener registers a callback after connection and resubscription.
func (c *Client) AddOnConnectListener(fn func(context.Context)) {
	c.mu.Lock()
	c.onConnect = append(c.onConnect, fn)
	c.mu.Unlock()
	if c.connected.Load() {
		fn(context.Background())
	}
}

// Close cancels inbound handlers, stops reconnection, and disconnects from the broker.
func (c *Client) Close() error {
	c.mu.RLock()
	dispatcher := c.dispatcher
	c.mu.RUnlock()
	if dispatcher != nil {
		dispatcher.cancel()
	}
	if c.cancel != nil {
		defer c.cancel()
	}
	c.connected.Store(false)
	if c.inner == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return c.inner.Disconnect(ctx)
}

// Subscribe installs the handler before requesting broker delivery.
func (c *Client) Subscribe(filter string, handler MessageHandler) error {
	c.mu.Lock()
	if c.router == nil {
		c.router = paho.NewStandardRouter()
	}
	if c.dispatcher == nil {
		c.dispatcher = newMessageDispatcher(context.Background(), c.logger, HandlerConfig{})
	}
	dispatcher := c.dispatcher
	c.router.RegisterHandler(filter, func(msg *paho.Publish) {
		handlerCtx := dispatcher.ctx
		if msg.Properties != nil && msg.Properties.MessageExpiry != nil {
			handlerCtx = context.WithValue(handlerCtx, expiryKey{}, time.Duration(*msg.Properties.MessageExpiry)*time.Second)
		}
		payload := append([]byte(nil), msg.Payload...)
		dispatcher.submit(inboundMessage{ctx: handlerCtx, handler: handler, topic: msg.Topic, payload: payload})
	})
	c.subscriptions = append(c.subscriptions, subscription{filter, handler})
	c.mu.Unlock()
	if c.inner == nil || !c.connected.Load() {
		return nil
	}
	return c.subscribe(c.inner, filter)
}

// PublishJSON marshals and publishes JSON with QoS 1.
func (c *Client) PublishJSON(ctx context.Context, topic string, payload any, retained bool) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return c.PublishRaw(ctx, topic, raw, retained)
}

// PublishRawJSON publishes pre-marshaled JSON with QoS 1.
func (c *Client) PublishRawJSON(ctx context.Context, topic string, payload json.RawMessage, retained bool) error {
	return c.PublishRaw(ctx, topic, payload, retained)
}

// PublishRaw publishes with QoS 1. Retained RPC availability expires after 120s
// as required by OMLOX, so a stopped handler cannot remain advertised indefinitely.
func (c *Client) PublishRaw(ctx context.Context, topic string, payload []byte, retained bool) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	packet := &paho.Publish{QoS: 1, Retain: retained, Topic: topic, Payload: payload}
	if retained && strings.HasPrefix(topic, "/omlox/jsonrpc/rpc/available/") {
		expiry := uint32(120)
		packet.Properties = &paho.PublishProperties{MessageExpiry: &expiry}
	}
	start := time.Now()
	response, err := c.inner.Publish(ctx, packet)
	if err == nil && response != nil && response.ReasonCode >= 0x80 {
		err = fmt.Errorf("MQTT publish rejected: reason %d", response.ReasonCode)
	}
	status := "success"
	if err != nil {
		status = "failure"
	}
	observability.Global().RecordMQTTPublish(ctx, status, time.Since(start))
	return err
}

func (c *Client) resubscribe(client mqttConnection) {
	c.mu.RLock()
	subs := append([]subscription(nil), c.subscriptions...)
	c.mu.RUnlock()
	for _, sub := range subs {
		if err := c.subscribe(client, sub.filter); err != nil && c.logger != nil {
			c.logger.Warn("mqtt subscribe failed", zap.Error(err), zap.String("filter", sub.filter))
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
func (c *Client) subscribe(client mqttConnection, filter string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ack, err := client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: filter, QoS: 1}}})
	if err != nil {
		return err
	}
	if ack == nil || len(ack.Reasons) != 1 || ack.Reasons[0] >= 0x80 {
		return fmt.Errorf("MQTT subscription rejected for %s", filter)
	}
	return nil
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

type expiryKey struct{}

// MessageExpiry returns the broker-supplied remaining lifetime of an MQTT 5 message.
func MessageExpiry(ctx context.Context) (time.Duration, bool) {
	expiry, ok := ctx.Value(expiryKey{}).(time.Duration)
	return expiry, ok
}
