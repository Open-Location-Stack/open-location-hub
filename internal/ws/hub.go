package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/auth"
	"github.com/formation-res/open-rtls-hub/internal/config"
	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/formation-res/open-rtls-hub/internal/hub"
	"github.com/formation-res/open-rtls-hub/internal/observability"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

const (
	errUnknownEvent        = 10000
	errUnknownTopic        = 10001
	errSubscribeFailed     = 10002
	errUnsubscribeFailed   = 10003
	errUnauthorized        = 10004
	errInvalidPayload      = 10005
	topicLocationUpdates   = "location_updates"
	topicLocationGeoJSON   = "location_updates:geojson"
	topicCollisionEvents   = "collision_events"
	topicFenceEvents       = "fence_events"
	topicFenceGeoJSON      = "fence_events:geojson"
	topicTrackableMotions  = "trackable_motions"
	topicProximityUpdates  = "proximity_updates"
	topicMetadataChanges   = "metadata_changes"
	eventDispatchBatchSize = 256
	eventDispatchFlushWait = 5 * time.Millisecond
)

type wrapper struct {
	Event          string          `json:"event"`
	Topic          string          `json:"topic,omitempty"`
	SubscriptionID *int            `json:"subscription_id,omitempty"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	Params         map[string]any  `json:"params,omitempty"`
	Code           *int            `json:"code,omitempty"`
	Description    *string         `json:"description,omitempty"`
}

type Hub struct {
	logger            *zap.Logger
	service           *hub.Service
	bus               *hub.EventBus
	authenticator     auth.Authenticator
	registry          *auth.Registry
	authCfg           config.AuthConfig
	writeTimeout      time.Duration
	outboundBuffer    int
	collisionsEnabled bool
	readTimeout       time.Duration
	pingInterval      time.Duration
	upgrader          websocket.Upgrader
	stats             *hub.RuntimeStats
	mu                sync.RWMutex
	connections       map[*connection]struct{}
}

type connection struct {
	hub      *Hub
	conn     *websocket.Conn
	send     chan []byte
	mu       sync.RWMutex
	nextID   int
	subs     map[int]subscription
	closed   bool
	closeMux sync.Mutex
	done     chan struct{}
}

type subscription struct {
	id     int
	topic  string
	filter any
}

type locationFilter struct {
	ProviderID   string
	ProviderType string
	Source       string
	CRS          string
	ZoneID       string
	Floor        *float32
	AccuracyLTE  *float64
}

type fenceFilter struct {
	FenceID     string
	ForeignID   string
	ProviderID  string
	TrackableID string
	EventType   string
	ObjectType  string
}

type motionFilter struct {
	ID          string
	ProviderID  string
	CRS         string
	ZoneID      string
	Floor       *float32
	AccuracyLTE *float64
}

type collisionFilter struct {
	ObjectID1     string
	ObjectID2     string
	CollisionType string
	CRS           string
	ZoneID        string
	Floor         *float32
}

type metadataFilter struct {
	ID        string
	Type      string
	Operation string
}

// New constructs a WebSocket hub and starts bus fan-out.
func New(logger *zap.Logger, service *hub.Service, bus *hub.EventBus, authenticator auth.Authenticator, registry *auth.Registry, authCfg config.AuthConfig, writeTimeout, readTimeout, pingInterval time.Duration, outboundBuffer, subscriberBuffer int, collisionsEnabled bool) *Hub {
	if writeTimeout <= 0 {
		writeTimeout = 5 * time.Second
	}
	if readTimeout <= 0 {
		readTimeout = time.Minute
	}
	if pingInterval <= 0 {
		pingInterval = 30 * time.Second
	}
	if readTimeout <= pingInterval {
		readTimeout = pingInterval + pingInterval/2
	}
	h := &Hub{
		logger:            logger,
		service:           service,
		bus:               bus,
		authenticator:     authenticator,
		registry:          registry,
		authCfg:           authCfg,
		writeTimeout:      writeTimeout,
		outboundBuffer:    outboundBuffer,
		collisionsEnabled: collisionsEnabled,
		readTimeout:       readTimeout,
		pingInterval:      pingInterval,
		stats:             runtimeStatsFromBus(bus),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(*http.Request) bool { return true },
		},
		connections: map[*connection]struct{}{},
	}
	if bus != nil {
		ch, _ := bus.Subscribe(subscriberBuffer)
		go h.consume(ch)
	}
	return h
}

func runtimeStatsFromBus(bus *hub.EventBus) *hub.RuntimeStats {
	if bus != nil && bus.Stats() != nil {
		return bus.Stats()
	}
	return nil
}

func (h *Hub) telemetry() *observability.Runtime {
	return observability.Global()
}

// Handle upgrades the request to a WebSocket connection and serves the OMLOX
// wrapper protocol on it.
func (h *Hub) Handle(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &connection{
		hub:    h,
		conn:   conn,
		send:   make(chan []byte, h.outboundBuffer),
		subs:   map[int]subscription{},
		nextID: 1,
		done:   make(chan struct{}),
	}
	c.configureSocket()
	h.mu.Lock()
	h.connections[c] = struct{}{}
	if h.stats != nil {
		h.stats.SetWebSocketConnections(int64(len(h.connections)))
	}
	h.mu.Unlock()

	go c.writeLoop()
	c.readLoop()
}

func (h *Hub) consume(ch <-chan hub.Event) {
	batch := make([]hub.Event, 0, eventDispatchBatchSize)
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	timerActive := false
	flush := func() {
		if len(batch) == 0 {
			return
		}
		out := append([]hub.Event(nil), batch...)
		batch = batch[:0]
		h.broadcastBatch(out)
	}
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, event)
			if len(batch) == 1 && !timerActive {
				timer.Reset(eventDispatchFlushWait)
				timerActive = true
			}
			if len(batch) >= eventDispatchBatchSize {
				if timerActive {
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timerActive = false
				}
				flush()
			}
		case <-timer.C:
			timerActive = false
			flush()
		}
	}
}

func (h *Hub) broadcastBatch(events []hub.Event) {
	if len(events) == 0 {
		return
	}
	h.mu.RLock()
	conns := make([]*connection, 0, len(h.connections))
	for conn := range h.connections {
		conns = append(conns, conn)
	}
	h.mu.RUnlock()
	for _, conn := range conns {
		conn.deliverBatch(events)
	}
}

func (c *connection) readLoop() {
	defer c.close()
	for {
		_, payload, err := c.conn.ReadMessage()
		if err != nil {
			if !isExpectedClose(err) && !isNetClosed(err) {
				c.hub.logger.Debug("websocket read loop ended", zap.Error(err))
			}
			return
		}
		_ = c.conn.SetReadDeadline(time.Now().Add(c.hub.readTimeout))
		var msg wrapper
		if err := json.Unmarshal(payload, &msg); err != nil {
			c.sendError(errInvalidPayload, "invalid websocket wrapper")
			continue
		}
		switch msg.Event {
		case "subscribe":
			c.handleSubscribe(msg)
		case "unsubscribe":
			c.handleUnsubscribe(msg)
		case "message":
			c.handleMessage(msg)
		default:
			c.sendError(errUnknownEvent, "unknown event")
		}
	}
}

func (c *connection) writeLoop() {
	ticker := time.NewTicker(c.hub.pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case payload, ok := <-c.send:
			if !ok {
				return
			}
			if c.hub.stats != nil {
				c.hub.stats.AddWebSocketOutboundDepth(-1)
			}
			if err := c.writeFrame(websocket.TextMessage, payload); err != nil {
				if !isExpectedClose(err) && !isNetClosed(err) {
					c.hub.logger.Debug("websocket write loop ended", zap.Error(err))
				}
				c.close()
				return
			}
		case <-ticker.C:
			if err := c.writeFrame(websocket.PingMessage, nil); err != nil {
				if !isExpectedClose(err) && !isNetClosed(err) {
					c.hub.logger.Debug("websocket ping failed", zap.Error(err))
				}
				c.close()
				return
			}
		}
	}
}

func (c *connection) close() {
	c.closeMux.Lock()
	if c.closed {
		c.closeMux.Unlock()
		return
	}
	c.closed = true
	close(c.done)
	c.closeMux.Unlock()
	c.hub.mu.Lock()
	delete(c.hub.connections, c)
	if c.hub.stats != nil {
		c.hub.stats.SetWebSocketConnections(int64(len(c.hub.connections)))
	}
	c.hub.mu.Unlock()
	_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(c.hub.writeTimeout))
	_ = c.conn.Close()
}

func (c *connection) handleSubscribe(msg wrapper) {
	if !knownTopic(msg.Topic) {
		c.sendError(errUnknownTopic, "unknown topic")
		return
	}
	if msg.Topic == topicCollisionEvents && !c.hub.collisionsEnabled {
		c.sendError(errSubscribeFailed, "collision_events is disabled by configuration")
		return
	}
	if _, err := c.authenticate(msg.Params, true, msg.Topic); err != nil {
		c.sendError(errUnauthorized, err.Error())
		return
	}
	filter, err := parseFilter(msg.Topic, msg.Params)
	if err != nil {
		c.sendError(errInvalidPayload, err.Error())
		return
	}
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	c.subs[id] = subscription{id: id, topic: msg.Topic, filter: filter}
	c.mu.Unlock()
	c.sendWrapper(wrapper{Event: "subscribed", Topic: msg.Topic, SubscriptionID: &id})
}

func (c *connection) handleUnsubscribe(msg wrapper) {
	if msg.SubscriptionID == nil {
		c.sendError(errUnsubscribeFailed, "subscription_id is required")
		return
	}
	c.mu.Lock()
	sub, ok := c.subs[*msg.SubscriptionID]
	if ok {
		delete(c.subs, *msg.SubscriptionID)
	}
	c.mu.Unlock()
	if !ok {
		c.sendError(errUnsubscribeFailed, "subscription not found")
		return
	}
	c.sendWrapper(wrapper{Event: "unsubscribed", Topic: sub.topic, SubscriptionID: msg.SubscriptionID})
}

func (c *connection) handleMessage(msg wrapper) {
	if !knownTopic(msg.Topic) {
		c.sendError(errUnknownTopic, "unknown topic")
		return
	}
	principal, err := c.authenticate(msg.Params, false, msg.Topic)
	if err != nil {
		c.sendError(errUnauthorized, err.Error())
		return
	}
	ctx := context.Background()
	if principal != nil {
		ctx = auth.WithPrincipal(ctx, principal)
	}
	ctx = observability.WithIngestTransport(ctx, "websocket")
	switch msg.Topic {
	case topicLocationUpdates:
		var body []gen.Location
		if err := json.Unmarshal(msg.Payload, &body); err != nil {
			c.sendError(errInvalidPayload, "invalid location payload")
			return
		}
		if err := c.hub.service.ProcessLocations(ctx, body); err != nil {
			c.sendError(errInvalidPayload, err.Error())
		}
	case topicProximityUpdates:
		var body []gen.Proximity
		if err := json.Unmarshal(msg.Payload, &body); err != nil {
			c.sendError(errInvalidPayload, "invalid proximity payload")
			return
		}
		if err := c.hub.service.ProcessProximities(ctx, body); err != nil {
			c.sendError(errInvalidPayload, err.Error())
		}
	default:
		c.sendError(errInvalidPayload, "topic does not accept client publication")
	}
}

func (c *connection) authenticate(params map[string]any, subscribe bool, topic string) (*auth.Principal, error) {
	if !c.hub.authCfg.Enabled || c.hub.authCfg.Mode == "none" {
		return &auth.Principal{}, nil
	}
	token, _ := params["token"].(string)
	if strings.TrimSpace(token) == "" {
		return nil, authErr("missing params.token")
	}
	principal, err := c.hub.authenticator.Authenticate(context.Background(), token)
	if err != nil {
		return nil, err
	}
	if c.hub.registry != nil {
		if subscribe {
			if err := c.hub.registry.AuthorizeWebSocketSubscribe(principal, topic); err != nil {
				return nil, err
			}
		} else {
			if err := c.hub.registry.AuthorizeWebSocketPublish(principal, topic); err != nil {
				return nil, err
			}
		}
	}
	return principal, nil
}

func (c *connection) deliverBatch(events []hub.Event) {
	start := time.Now()
	c.mu.RLock()
	subs := make([]subscription, 0, len(c.subs))
	for _, sub := range c.subs {
		subs = append(subs, sub)
	}
	c.mu.RUnlock()
	for _, sub := range subs {
		payload, ok := payloadBatchForSubscription(sub, events)
		if !ok {
			continue
		}
		c.sendWrapper(wrapper{Event: "message", Topic: sub.topic, SubscriptionID: &sub.id, Payload: payload})
		c.hub.telemetry().RecordWebSocketDispatch(context.Background(), sub.topic, "sent", time.Since(start))
	}
}

func (c *connection) sendWrapper(msg wrapper) {
	start := time.Now()
	raw, err := json.Marshal(msg)
	if err != nil {
		return
	}
	c.closeMux.Lock()
	closed := c.closed
	c.closeMux.Unlock()
	if closed {
		return
	}
	select {
	case <-c.done:
		return
	case c.send <- raw:
		if c.hub.stats != nil {
			c.hub.stats.AddWebSocketOutboundDepth(1)
		}
		c.hub.telemetry().RecordWebSocketDispatch(context.Background(), msg.Topic, "queued", time.Since(start))
	default:
		if c.hub.stats != nil {
			c.hub.stats.IncWebSocketOutboundDrops()
		}
		c.hub.telemetry().RecordWebSocketDispatch(context.Background(), msg.Topic, "dropped", time.Since(start))
		c.hub.logger.Debug("websocket outbound buffer full; dropping outbound payload")
	}
}

func (c *connection) sendError(code int, description string) {
	c.sendWrapper(wrapper{Event: "error", Code: &code, Description: &description})
}

func knownTopic(topic string) bool {
	switch topic {
	case topicLocationUpdates, topicLocationGeoJSON, topicCollisionEvents, topicFenceEvents, topicFenceGeoJSON, topicTrackableMotions, topicProximityUpdates, topicMetadataChanges:
		return true
	default:
		return false
	}
}

func parseFilter(topic string, params map[string]any) (any, error) {
	switch topic {
	case topicLocationUpdates, topicLocationGeoJSON:
		return parseLocationFilter(params), nil
	case topicFenceEvents, topicFenceGeoJSON:
		return parseFenceFilter(params), nil
	case topicTrackableMotions:
		return parseMotionFilter(params), nil
	case topicCollisionEvents:
		return parseCollisionFilter(params), nil
	case topicProximityUpdates:
		return struct{}{}, nil
	case topicMetadataChanges:
		return parseMetadataFilter(params), nil
	default:
		return nil, authErr("unknown topic")
	}
}

func payloadBatchForSubscription(sub subscription, events []hub.Event) (json.RawMessage, bool) {
	switch sub.topic {
	case topicLocationUpdates:
		items := make([]gen.Location, 0, len(events))
		for _, event := range events {
			if event.Kind != hub.EventLocation {
				continue
			}
			envelope, ok := event.Payload.(hub.LocationEnvelope)
			if !ok || !matchLocation(sub.filter.(locationFilter), envelope.Location) {
				continue
			}
			items = append(items, envelope.Location)
		}
		if len(items) == 0 {
			return nil, false
		}
		return marshalPayload(items)
	case topicLocationGeoJSON:
		items := make([]hub.GeoJSONFeatureCollection, 0, len(events))
		for _, event := range events {
			if event.Kind != hub.EventLocation {
				continue
			}
			envelope, ok := event.Payload.(hub.LocationEnvelope)
			if !ok || !matchLocation(sub.filter.(locationFilter), envelope.Location) {
				continue
			}
			items = append(items, envelope.GeoJSON)
		}
		if len(items) == 0 {
			return nil, false
		}
		return marshalPayload(items)
	case topicProximityUpdates:
		items := make([]gen.Proximity, 0, len(events))
		for _, event := range events {
			if event.Kind != hub.EventProximity {
				continue
			}
			envelope, ok := event.Payload.(hub.ProximityEnvelope)
			if !ok {
				continue
			}
			items = append(items, envelope.Proximity)
		}
		if len(items) == 0 {
			return nil, false
		}
		return marshalPayload(items)
	case topicTrackableMotions:
		items := make([]gen.TrackableMotion, 0, len(events))
		for _, event := range events {
			if event.Kind != hub.EventTrackableMotion {
				continue
			}
			envelope, ok := event.Payload.(hub.TrackableMotionEnvelope)
			if !ok || !matchMotion(sub.filter.(motionFilter), envelope.Motion) {
				continue
			}
			items = append(items, envelope.Motion)
		}
		if len(items) == 0 {
			return nil, false
		}
		return marshalPayload(items)
	case topicFenceEvents:
		items := make([]gen.FenceEvent, 0, len(events))
		for _, event := range events {
			if event.Kind != hub.EventFenceEvent {
				continue
			}
			envelope, ok := event.Payload.(hub.FenceEventEnvelope)
			if !ok || !matchFence(sub.filter.(fenceFilter), envelope.Event) {
				continue
			}
			items = append(items, envelope.Event)
		}
		if len(items) == 0 {
			return nil, false
		}
		return marshalPayload(items)
	case topicFenceGeoJSON:
		items := make([]hub.GeoJSONFeatureCollection, 0, len(events))
		for _, event := range events {
			if event.Kind != hub.EventFenceEvent {
				continue
			}
			envelope, ok := event.Payload.(hub.FenceEventEnvelope)
			if !ok || !matchFence(sub.filter.(fenceFilter), envelope.Event) {
				continue
			}
			items = append(items, envelope.GeoJSON)
		}
		if len(items) == 0 {
			return nil, false
		}
		return marshalPayload(items)
	case topicCollisionEvents:
		items := make([]gen.CollisionEvent, 0, len(events))
		for _, event := range events {
			if event.Kind != hub.EventCollisionEvent {
				continue
			}
			envelope, ok := event.Payload.(hub.CollisionEnvelope)
			if !ok || !matchCollision(sub.filter.(collisionFilter), envelope.Event) {
				continue
			}
			items = append(items, envelope.Event)
		}
		if len(items) == 0 {
			return nil, false
		}
		return marshalPayload(items)
	case topicMetadataChanges:
		items := make([]hub.MetadataChange, 0, len(events))
		for _, event := range events {
			if event.Kind != hub.EventMetadataChange {
				continue
			}
			change, ok := event.Payload.(hub.MetadataChange)
			if !ok || !matchMetadata(sub.filter.(metadataFilter), change) {
				continue
			}
			items = append(items, change)
		}
		if len(items) == 0 {
			return nil, false
		}
		return marshalPayload(items)
	default:
		return nil, false
	}
}

func marshalPayload(value any) (json.RawMessage, bool) {
	raw, err := json.Marshal(value)
	return raw, err == nil
}

func parseLocationFilter(params map[string]any) locationFilter {
	filter := locationFilter{
		ProviderID:   stringParam(params, "provider_id"),
		ProviderType: stringParam(params, "provider_type"),
		Source:       stringParam(params, "source"),
		CRS:          stringParam(params, "crs"),
		ZoneID:       stringParam(params, "zone_id"),
	}
	filter.Floor = float32Param(params, "floor")
	filter.AccuracyLTE = float64Param(params, "accuracy")
	return filter
}

func parseFenceFilter(params map[string]any) fenceFilter {
	return fenceFilter{
		FenceID:     stringParam(params, "fence_id"),
		ForeignID:   stringParam(params, "foreign_id"),
		ProviderID:  stringParam(params, "provider_id"),
		TrackableID: stringParam(params, "trackable_id"),
		EventType:   stringParam(params, "event_type"),
		ObjectType:  stringParam(params, "object_type"),
	}
}

func parseMotionFilter(params map[string]any) motionFilter {
	filter := motionFilter{
		ID:         stringParam(params, "id"),
		ProviderID: stringParam(params, "provider_id"),
		CRS:        stringParam(params, "crs"),
		ZoneID:     stringParam(params, "zone_id"),
	}
	filter.Floor = float32Param(params, "floor")
	filter.AccuracyLTE = float64Param(params, "accuracy")
	return filter
}

func parseCollisionFilter(params map[string]any) collisionFilter {
	filter := collisionFilter{
		ObjectID1:     stringParam(params, "object_id_1"),
		ObjectID2:     stringParam(params, "object_id_2"),
		CollisionType: stringParam(params, "collision_type"),
		CRS:           stringParam(params, "crs"),
		ZoneID:        stringParam(params, "zone_id"),
	}
	filter.Floor = float32Param(params, "floor")
	return filter
}

func parseMetadataFilter(params map[string]any) metadataFilter {
	return metadataFilter{
		ID:        stringParam(params, "id"),
		Type:      stringParam(params, "type"),
		Operation: stringParam(params, "operation"),
	}
}

func stringParam(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	value, _ := params[key].(string)
	return strings.TrimSpace(value)
}

func float32Param(params map[string]any, key string) *float32 {
	if params == nil {
		return nil
	}
	switch v := params[key].(type) {
	case float64:
		out := float32(v)
		return &out
	default:
		return nil
	}
}

func float64Param(params map[string]any, key string) *float64 {
	if params == nil {
		return nil
	}
	switch v := params[key].(type) {
	case float64:
		out := v
		return &out
	default:
		return nil
	}
}

func matchLocation(filter locationFilter, location gen.Location) bool {
	if filter.ProviderID != "" && filter.ProviderID != location.ProviderId {
		return false
	}
	if filter.ProviderType != "" && filter.ProviderType != location.ProviderType {
		return false
	}
	if filter.Source != "" && filter.Source != location.Source {
		return false
	}
	if filter.CRS != "" && (location.Crs == nil || filter.CRS != *location.Crs) {
		return false
	}
	if filter.ZoneID != "" && filter.ZoneID != location.Source {
		return false
	}
	if filter.AccuracyLTE != nil && (location.Accuracy == nil || float64(*location.Accuracy) > *filter.AccuracyLTE) {
		return false
	}
	return true
}

func matchFence(filter fenceFilter, event gen.FenceEvent) bool {
	if filter.FenceID != "" && filter.FenceID != event.FenceId.String() {
		return false
	}
	if filter.ForeignID != "" && (event.ForeignId == nil || filter.ForeignID != *event.ForeignId) {
		return false
	}
	if filter.ProviderID != "" && (event.ProviderId == nil || filter.ProviderID != *event.ProviderId) {
		return false
	}
	if filter.TrackableID != "" && (event.TrackableId == nil || filter.TrackableID != *event.TrackableId) {
		return false
	}
	if filter.EventType != "" && filter.EventType != string(event.EventType) {
		return false
	}
	return true
}

func matchMotion(filter motionFilter, motion gen.TrackableMotion) bool {
	if filter.ID != "" && filter.ID != motion.Id {
		return false
	}
	if filter.ProviderID != "" && filter.ProviderID != motion.Location.ProviderId {
		return false
	}
	if filter.CRS != "" && (motion.Location.Crs == nil || filter.CRS != *motion.Location.Crs) {
		return false
	}
	if filter.ZoneID != "" && filter.ZoneID != motion.Location.Source {
		return false
	}
	if filter.AccuracyLTE != nil && (motion.Location.Accuracy == nil || float64(*motion.Location.Accuracy) > *filter.AccuracyLTE) {
		return false
	}
	return true
}

func matchCollision(filter collisionFilter, event gen.CollisionEvent) bool {
	if filter.CollisionType != "" && filter.CollisionType != string(event.CollisionType) {
		return false
	}
	if filter.ObjectID1 != "" || filter.ObjectID2 != "" {
		ids := map[string]struct{}{}
		for _, collision := range event.Collisions {
			ids[collision.Id.String()] = struct{}{}
		}
		if filter.ObjectID1 != "" {
			if _, ok := ids[filter.ObjectID1]; !ok {
				return false
			}
		}
		if filter.ObjectID2 != "" {
			if _, ok := ids[filter.ObjectID2]; !ok {
				return false
			}
		}
	}
	return true
}

func matchMetadata(filter metadataFilter, change hub.MetadataChange) bool {
	if filter.ID != "" && filter.ID != change.ID {
		return false
	}
	if filter.Type != "" && filter.Type != change.Type {
		return false
	}
	if filter.Operation != "" && filter.Operation != change.Operation {
		return false
	}
	return true
}

type simpleError string

func (e simpleError) Error() string { return string(e) }

func authErr(msg string) error { return simpleError(msg) }

func (c *connection) configureSocket() {
	_ = c.conn.SetReadDeadline(time.Now().Add(c.hub.readTimeout))
	c.conn.SetReadLimit(1 << 20)
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(c.hub.readTimeout))
	})
	c.conn.SetPingHandler(func(appData string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(c.hub.readTimeout))
		return c.conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(c.hub.writeTimeout))
	})
}

func (c *connection) writeFrame(messageType int, payload []byte) error {
	_ = c.conn.SetWriteDeadline(time.Now().Add(c.hub.writeTimeout))
	return c.conn.WriteMessage(messageType, payload)
}

func isExpectedClose(err error) bool {
	return websocket.IsCloseError(err,
		websocket.CloseNormalClosure,
		websocket.CloseGoingAway,
		websocket.CloseNoStatusReceived,
		websocket.CloseAbnormalClosure,
	)
}

func isNetClosed(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "connection aborted")
}
