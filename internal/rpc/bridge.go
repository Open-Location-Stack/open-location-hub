package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/auth"
	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/formation-res/open-rtls-hub/internal/mqtt"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	jsonRPCVersion        = "2.0"
	defaultPendingCap     = 32
	defaultHandlerID      = "open-rtls-hub"
	defaultAnnounceEvery  = time.Minute
	errCodeInvalidRequest = -32600
	errCodeInvalidParams  = -32602
	errCodeInternal       = -32603
	errCodeUnknownHandler = -32000
	errCodeTimeout        = -32001
	errCodeNoSuccess      = -32002
	errCodeMalformed      = -32003
	errCodeUnsupported    = -32050
)

// Publisher defines the MQTT operations required by the RPC bridge.
type Publisher interface {
	PublishJSON(ctx context.Context, topic string, payload any, retained bool) error
	Subscribe(filter string, handler mqtt.MessageHandler) error
}

// ConnectAwarePublisher exposes MQTT reconnect hooks used for method
// announcement refresh.
type ConnectAwarePublisher interface {
	AddOnConnectListener(func(context.Context))
}

// MethodHandler serves a hub-owned JSON-RPC method.
type MethodHandler interface {
	Handle(ctx context.Context, request gen.JsonRpcRequest) (map[string]any, error)
}

// XCMDAdapter executes OMLOX core.xcmd payloads against a downstream provider
// or controller integration.
type XCMDAdapter interface {
	ExecuteXCMD(ctx context.Context, request XCMDRequest) (map[string]any, []any, error)
}

// XCMDRequest is the normalized request forwarded to an XCMD adapter.
type XCMDRequest struct {
	Method    string
	CallerID  string
	HandlerID string
	Params    map[string]any
}

// IdentifyConfig controls the identify payload returned by the hub.
type IdentifyConfig struct {
	ServiceName string
	AuthMode    string
}

// Config groups runtime collaborators and knobs for the RPC bridge.
type Config struct {
	Timeout              time.Duration
	HandlerID            string
	AnnouncementInterval time.Duration
	Authorizer           *auth.Registry
	Identify             IdentifyConfig
	XCMDAdapter          XCMDAdapter
}

type methodSource string

const (
	methodSourceLocal    methodSource = "local"
	methodSourceExternal methodSource = "external"
)

type availabilityEntry struct {
	Source methodSource
}

type pendingRequest struct {
	method    string
	responses chan json.RawMessage
}

// Bridge tracks available RPC handlers, serves local OMLOX methods, and
// forwards JSON-RPC traffic over MQTT when external handlers are selected.
type Bridge struct {
	logger    *zap.Logger
	mqtt      Publisher
	timeout   time.Duration
	cfg       Config
	methods   map[string]MethodHandler
	mu        sync.RWMutex
	available map[string]map[string]availabilityEntry
	pending   map[string]*pendingRequest
	stopCh    chan struct{}
}

// NewBridge constructs an RPC bridge and subscribes it to method availability
// and response topics when MQTT is configured.
func NewBridge(logger *zap.Logger, mqttClient Publisher, cfg Config) (*Bridge, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if strings.TrimSpace(cfg.HandlerID) == "" {
		cfg.HandlerID = defaultHandlerID
	}
	if cfg.AnnouncementInterval <= 0 {
		cfg.AnnouncementInterval = defaultAnnounceEvery
	}
	if strings.TrimSpace(cfg.Identify.ServiceName) == "" {
		cfg.Identify.ServiceName = defaultHandlerID
	}

	b := &Bridge{
		logger:    logger,
		mqtt:      mqttClient,
		timeout:   cfg.Timeout,
		cfg:       cfg,
		methods:   map[string]MethodHandler{},
		available: map[string]map[string]availabilityEntry{},
		pending:   map[string]*pendingRequest{},
		stopCh:    make(chan struct{}),
	}
	b.registerBuiltins()
	if mqttClient != nil {
		if err := mqttClient.Subscribe(mqtt.TopicRPCAvailableWildcard(), b.handleAvailable); err != nil {
			return nil, err
		}
		if err := mqttClient.Subscribe(mqtt.TopicRPCResponseWildcard(), b.handleResponse); err != nil {
			return nil, err
		}
		if aware, ok := mqttClient.(ConnectAwarePublisher); ok {
			aware.AddOnConnectListener(func(ctx context.Context) {
				b.publishAnnouncements(ctx)
			})
		}
		go b.announcementLoop()
	}
	return b, nil
}

// Close stops background announcement work.
func (b *Bridge) Close() error {
	select {
	case <-b.stopCh:
	default:
		close(b.stopCh)
	}
	return nil
}

// AvailableMethods returns the discovered method registry after applying RPC
// discovery authorization when configured.
func (b *Bridge) AvailableMethods(ctx context.Context) (gen.RpcAvailableMethods, error) {
	if err := b.authorizeDiscover(ctx); err != nil {
		return nil, err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make(gen.RpcAvailableMethods, len(b.available))
	for method, handlers := range b.available {
		entry := gen.RpcAvailableMethodsEntry{HandlerId: make([]string, 0, len(handlers))}
		for handlerID := range handlers {
			entry.HandlerId = append(entry.HandlerId, handlerID)
		}
		slices.Sort(entry.HandlerId)
		out[method] = entry
	}
	return out, nil
}

// Invoke serves local methods and/or bridges JSON-RPC requests over MQTT.
func (b *Bridge) Invoke(ctx context.Context, request gen.JsonRpcRequest) (json.RawMessage, bool, error) {
	if err := b.authorizeInvoke(ctx, request.Method); err != nil {
		return nil, false, err
	}
	if raw, ok := b.validateRequest(request); !ok {
		return raw, false, nil
	}

	params := request.Params
	notifyOnly := request.Id == nil && (params == nil || params.UnderscoreCallerId == nil)
	targetLocal, targetExternal, rawErr := b.dispatchTargets(request)
	if rawErr != nil {
		return rawErr, false, nil
	}
	if !targetLocal && !targetExternal {
		return errorResponse(request.Id, errCodeUnknownHandler, "no handler available for method", nil), false, nil
	}

	if notifyOnly {
		if targetLocal {
			if _, err := b.invokeLocal(ctx, request); err != nil {
				return errorResponse(request.Id, errCodeInternal, err.Error(), nil), false, nil
			}
		}
		if targetExternal {
			topic := mqtt.TopicRPCRequest(request.Method)
			if params != nil && params.UnderscoreHandlerId != nil && strings.TrimSpace(*params.UnderscoreHandlerId) != "" {
				topic = mqtt.TopicRPCRequestHandler(request.Method, *params.UnderscoreHandlerId)
			}
			if err := b.publish(ctx, topic, request, false); err != nil {
				return errorResponse(request.Id, errCodeInternal, "failed to publish mqtt rpc request", nil), false, nil
			}
		}
		return nil, true, nil
	}

	callerID := b.ensureCallerID(&request)
	waitTimeout := b.timeout
	if request.Params != nil && request.Params.UnderscoreTimeout != nil && *request.Params.UnderscoreTimeout > 0 {
		waitTimeout = time.Duration(*request.Params.UnderscoreTimeout) * time.Millisecond
	}
	respCh := make(chan json.RawMessage, defaultPendingCap)
	b.mu.Lock()
	b.pending[callerID] = &pendingRequest{method: request.Method, responses: respCh}
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.pending, callerID)
		b.mu.Unlock()
	}()

	if targetExternal {
		topic := mqtt.TopicRPCRequest(request.Method)
		if request.Params != nil && request.Params.UnderscoreHandlerId != nil && strings.TrimSpace(*request.Params.UnderscoreHandlerId) != "" {
			topic = mqtt.TopicRPCRequestHandler(request.Method, *request.Params.UnderscoreHandlerId)
		}
		if err := b.publish(ctx, topic, request, false); err != nil {
			return errorResponse(request.Id, errCodeInternal, "failed to publish mqtt rpc request", nil), false, nil
		}
	}
	if targetLocal {
		if raw, err := b.invokeLocal(ctx, request); err != nil {
			return errorResponse(request.Id, errCodeInternal, err.Error(), nil), false, nil
		} else if len(raw) != 0 {
			select {
			case respCh <- raw:
			default:
				return errorResponse(request.Id, errCodeInternal, "local rpc response buffer is full", nil), false, nil
			}
		}
	}

	waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()
	return b.collectResponses(waitCtx, request, respCh), false, nil
}

func (b *Bridge) announcementLoop() {
	ticker := time.NewTicker(b.cfg.AnnouncementInterval)
	defer ticker.Stop()
	b.publishAnnouncements(context.Background())
	for {
		select {
		case <-b.stopCh:
			return
		case <-ticker.C:
			b.publishAnnouncements(context.Background())
		}
	}
}

func (b *Bridge) publishAnnouncements(ctx context.Context) {
	if b.mqtt == nil {
		return
	}
	for method := range b.methods {
		payload := map[string]any{
			"id":          b.cfg.HandlerID,
			"method_name": method,
		}
		if err := b.publish(ctx, mqtt.TopicRPCAvailable(method), payload, true); err != nil {
			b.logger.Warn("rpc method announcement failed", zap.Error(err), zap.String("method", method))
		}
	}
}

func (b *Bridge) registerBuiltins() {
	b.methods["com.omlox.ping"] = methodHandlerFunc(func(ctx context.Context, request gen.JsonRpcRequest) (map[string]any, error) {
		return map[string]any{
			"handler_id": b.cfg.HandlerID,
			"message":    "pong",
			"method":     request.Method,
			"timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
		}, nil
	})
	b.methods["com.omlox.identify"] = methodHandlerFunc(func(ctx context.Context, request gen.JsonRpcRequest) (map[string]any, error) {
		methods := make([]string, 0, len(b.methods))
		for method := range b.methods {
			methods = append(methods, method)
		}
		slices.Sort(methods)
		buildVersion := "dev"
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
			buildVersion = info.Main.Version
		}
		return map[string]any{
			"handler_id": b.cfg.HandlerID,
			"name":       b.cfg.Identify.ServiceName,
			"version":    buildVersion,
			"auth_mode":  b.cfg.Identify.AuthMode,
			"methods":    methods,
			"modes": map[string]any{
				"local_handlers": true,
				"mqtt_bridge":    b.mqtt != nil,
			},
			"request_method": request.Method,
		}, nil
	})
	b.methods["com.omlox.core.xcmd"] = methodHandlerFunc(func(ctx context.Context, request gen.JsonRpcRequest) (map[string]any, error) {
		if b.cfg.XCMDAdapter == nil {
			return nil, rpcError{Code: errCodeUnsupported, Message: "no xcmd adapter configured"}
		}
		params := map[string]any{}
		if request.Params != nil {
			for key, value := range request.Params.AdditionalProperties {
				params[key] = value
			}
		}
		callerID := ""
		handlerID := ""
		if request.Params != nil {
			if request.Params.UnderscoreCallerId != nil {
				callerID = *request.Params.UnderscoreCallerId
			}
			if request.Params.UnderscoreHandlerId != nil {
				handlerID = *request.Params.UnderscoreHandlerId
			}
		}
		result, broadcasts, err := b.cfg.XCMDAdapter.ExecuteXCMD(ctx, XCMDRequest{
			Method:    request.Method,
			CallerID:  callerID,
			HandlerID: handlerID,
			Params:    params,
		})
		if err != nil {
			var rpcErr rpcError
			if ok := asRPCError(err, &rpcErr); ok {
				return nil, rpcErr
			}
			return nil, rpcError{Code: errCodeUnsupported, Message: err.Error()}
		}
		for _, broadcast := range broadcasts {
			if err := b.publish(ctx, mqtt.TopicRPCXCMDResponseBroadcast(), broadcast, false); err != nil {
				b.logger.Warn("xcmd broadcast publish failed", zap.Error(err))
			}
		}
		if result == nil {
			result = map[string]any{}
		}
		if _, ok := result["handler_id"]; !ok {
			result["handler_id"] = b.cfg.HandlerID
		}
		return result, nil
	})
	for method := range b.methods {
		b.ensureAvailableLocked(method, b.cfg.HandlerID, methodSourceLocal)
	}
}

func (b *Bridge) authorizeDiscover(ctx context.Context) error {
	if b.cfg.Authorizer == nil {
		return nil
	}
	principal, _ := auth.PrincipalFromContext(ctx)
	return b.cfg.Authorizer.AuthorizeRPCDiscover(principal)
}

func (b *Bridge) authorizeInvoke(ctx context.Context, method string) error {
	if b.cfg.Authorizer == nil {
		return nil
	}
	principal, _ := auth.PrincipalFromContext(ctx)
	return b.cfg.Authorizer.AuthorizeRPCInvoke(principal, method)
}

func (b *Bridge) validateRequest(request gen.JsonRpcRequest) (json.RawMessage, bool) {
	if strings.TrimSpace(request.Method) == "" || request.Jsonrpc != jsonRPCVersion {
		return errorResponse(request.Id, errCodeInvalidRequest, "invalid json-rpc request", nil), false
	}
	if request.Params != nil && request.Params.UnderscoreAggregation != nil {
		if !request.Params.UnderscoreAggregation.Valid() {
			return errorResponse(request.Id, errCodeInvalidParams, "unknown aggregation strategy", nil), false
		}
		if request.Params.UnderscoreHandlerId != nil && strings.TrimSpace(*request.Params.UnderscoreHandlerId) != "" {
			return errorResponse(request.Id, errCodeInvalidParams, "_handler_id and _aggregation cannot be combined", nil), false
		}
	}
	return nil, true
}

func (b *Bridge) dispatchTargets(request gen.JsonRpcRequest) (bool, bool, json.RawMessage) {
	local := b.hasLocalMethod(request.Method)
	external := b.hasExternalMethod(request.Method)
	if request.Params == nil || request.Params.UnderscoreHandlerId == nil || strings.TrimSpace(*request.Params.UnderscoreHandlerId) == "" {
		return local, external, nil
	}
	handlerID := strings.TrimSpace(*request.Params.UnderscoreHandlerId)
	switch {
	case handlerID == b.cfg.HandlerID && local:
		return true, false, nil
	case b.hasExternalHandler(request.Method, handlerID):
		return false, true, nil
	default:
		return false, false, errorResponse(request.Id, errCodeUnknownHandler, "unknown handler id", nil)
	}
}

func (b *Bridge) invokeLocal(ctx context.Context, request gen.JsonRpcRequest) (json.RawMessage, error) {
	b.mu.RLock()
	handler := b.methods[request.Method]
	b.mu.RUnlock()
	if handler == nil {
		return nil, nil
	}
	result, err := handler.Handle(ctx, request)
	if err != nil {
		var rpcErr rpcError
		if ok := asRPCError(err, &rpcErr); ok {
			return errorResponse(request.Id, rpcErr.Code, rpcErr.Message, rpcErr.Data), nil
		}
		return nil, err
	}
	return successResponse(request.Id, result), nil
}

func (b *Bridge) publish(ctx context.Context, topic string, payload any, retained bool) error {
	if b.mqtt == nil {
		return fmt.Errorf("mqtt bridge is not configured")
	}
	return b.mqtt.PublishJSON(ctx, topic, payload, retained)
}

func (b *Bridge) handleAvailable(_ context.Context, topic string, payload []byte) error {
	parts := strings.Split(strings.Trim(topic, "/"), "/")
	if len(parts) < 5 {
		return nil
	}
	method := parts[len(parts)-1]
	if len(payload) == 0 {
		b.mu.Lock()
		delete(b.available, method)
		b.mu.Unlock()
		return nil
	}

	handlerIDs := map[string]struct{}{}
	var announcement struct {
		ID         string `json:"id"`
		MethodName string `json:"method_name"`
	}
	if err := json.Unmarshal(payload, &announcement); err == nil && strings.TrimSpace(announcement.ID) != "" {
		handlerIDs[strings.TrimSpace(announcement.ID)] = struct{}{}
		if strings.TrimSpace(announcement.MethodName) != "" {
			method = strings.TrimSpace(announcement.MethodName)
		}
	}

	var single struct {
		HandlerID string `json:"handler_id"`
	}
	if err := json.Unmarshal(payload, &single); err == nil && strings.TrimSpace(single.HandlerID) != "" {
		handlerIDs[strings.TrimSpace(single.HandlerID)] = struct{}{}
	}

	var entry gen.RpcAvailableMethodsEntry
	if err := json.Unmarshal(payload, &entry); err == nil {
		for _, id := range entry.HandlerId {
			if strings.TrimSpace(id) != "" {
				handlerIDs[strings.TrimSpace(id)] = struct{}{}
			}
		}
	}

	if len(handlerIDs) == 0 {
		handlerIDs[method] = struct{}{}
	}
	b.mu.Lock()
	for handlerID := range handlerIDs {
		b.ensureAvailableLocked(method, handlerID, methodSourceExternal)
	}
	b.mu.Unlock()
	return nil
}

func (b *Bridge) handleResponse(_ context.Context, topic string, payload []byte) error {
	parts := strings.Split(strings.Trim(topic, "/"), "/")
	if len(parts) < 6 {
		return nil
	}
	callerID := parts[len(parts)-1]
	b.mu.RLock()
	pending := b.pending[callerID]
	b.mu.RUnlock()
	if pending == nil {
		return nil
	}
	if !validRPCResponse(payload) {
		b.logger.Warn("dropping malformed rpc response", zap.String("caller_id", callerID), zap.ByteString("payload", payload))
		select {
		case pending.responses <- errorResponse(nil, errCodeMalformed, "malformed downstream rpc response", nil):
		default:
		}
		return nil
	}
	select {
	case pending.responses <- append(json.RawMessage(nil), payload...):
	default:
		b.logger.Warn("dropping rpc response because pending channel is full", zap.String("caller_id", callerID))
	}
	return nil
}

func (b *Bridge) collectResponses(ctx context.Context, request gen.JsonRpcRequest, respCh <-chan json.RawMessage) json.RawMessage {
	mode := gen.UnderscoreAllWithinTimeout
	if request.Params != nil && request.Params.UnderscoreAggregation != nil {
		mode = *request.Params.UnderscoreAggregation
	}

	var (
		firstSuccess json.RawMessage
		firstError   json.RawMessage
		all          []json.RawMessage
	)

	for {
		select {
		case <-ctx.Done():
			switch mode {
			case gen.UnderscoreAllWithinTimeout:
				body, _ := json.Marshal(map[string]any{
					"jsonrpc": jsonRPCVersion,
					"id":      rawIDValue(request.Id),
					"result":  map[string]any{"responses": all},
				})
				return body
			case gen.UnderscoreReturnFirstError:
				if len(firstError) != 0 {
					return firstError
				}
				if len(firstSuccess) != 0 {
					return firstSuccess
				}
			default:
				if len(firstSuccess) != 0 {
					return firstSuccess
				}
				if len(firstError) != 0 {
					return firstError
				}
			}
			if len(all) == 0 {
				return errorResponse(request.Id, errCodeTimeout, "rpc response timeout", nil)
			}
			return errorResponse(request.Id, errCodeNoSuccess, "no non-error response received", nil)
		case payload := <-respCh:
			if len(payload) == 0 {
				continue
			}
			all = append(all, payload)
			if responseIsError(payload) {
				if len(firstError) == 0 {
					firstError = payload
				}
				if mode == gen.UnderscoreReturnFirstError {
					return payload
				}
				continue
			}
			if len(firstSuccess) == 0 {
				firstSuccess = payload
			}
			if mode == gen.UnderscoreReturnFirstSuccess {
				return payload
			}
		}
	}
}

func (b *Bridge) ensureCallerID(request *gen.JsonRpcRequest) string {
	callerID := uuid.NewString()
	if request.Params == nil {
		request.Params = &gen.JsonRpcRequest_Params{}
	}
	if request.Params.UnderscoreCallerId != nil && strings.TrimSpace(*request.Params.UnderscoreCallerId) != "" {
		callerID = *request.Params.UnderscoreCallerId
	} else {
		request.Params.UnderscoreCallerId = &callerID
	}
	return callerID
}

func (b *Bridge) hasLocalMethod(method string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.methods[method]
	return ok
}

func (b *Bridge) hasExternalMethod(method string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for handlerID, entry := range b.available[method] {
		if entry.Source == methodSourceExternal && handlerID != b.cfg.HandlerID {
			return true
		}
	}
	return false
}

func (b *Bridge) hasExternalHandler(method, handlerID string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	entry, ok := b.available[method][handlerID]
	return ok && entry.Source == methodSourceExternal
}

func (b *Bridge) ensureAvailableLocked(method, handlerID string, source methodSource) {
	if b.available[method] == nil {
		b.available[method] = map[string]availabilityEntry{}
	}
	b.available[method][handlerID] = availabilityEntry{Source: source}
}

type methodHandlerFunc func(context.Context, gen.JsonRpcRequest) (map[string]any, error)

func (f methodHandlerFunc) Handle(ctx context.Context, request gen.JsonRpcRequest) (map[string]any, error) {
	return f(ctx, request)
}

// rpcError is a JSON-RPC error returned by local handlers.
type rpcError struct {
	Code    int
	Message string
	Data    map[string]any
}

func (e rpcError) Error() string { return e.Message }

func asRPCError(err error, target *rpcError) bool {
	if err == nil {
		return false
	}
	value, ok := err.(rpcError)
	if !ok {
		return false
	}
	*target = value
	return true
}

func successResponse(id *gen.JsonRpcRequest_Id, result map[string]any) json.RawMessage {
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": jsonRPCVersion,
		"id":      rawIDValue(id),
		"result":  result,
	})
	return body
}

func errorResponse(id *gen.JsonRpcRequest_Id, code int, message string, data map[string]any) json.RawMessage {
	errorBody := map[string]any{
		"code":    code,
		"message": message,
	}
	if data != nil {
		errorBody["data"] = data
	}
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": jsonRPCVersion,
		"id":      rawIDValue(id),
		"error":   errorBody,
	})
	return body
}

func rawIDValue(id *gen.JsonRpcRequest_Id) any {
	if id == nil {
		return nil
	}
	raw, err := id.MarshalJSON()
	if err != nil {
		return nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func validRPCResponse(payload []byte) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(payload, &probe); err != nil {
		return false
	}
	if _, ok := probe["jsonrpc"]; !ok {
		return false
	}
	if _, ok := probe["id"]; !ok {
		return false
	}
	_, hasResult := probe["result"]
	_, hasError := probe["error"]
	return hasResult || hasError
}

func responseIsError(payload []byte) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(payload, &probe); err != nil {
		return false
	}
	_, ok := probe["error"]
	return ok
}
