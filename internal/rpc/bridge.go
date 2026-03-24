package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/formation-res/open-rtls-hub/internal/mqtt"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type Publisher interface {
	PublishJSON(ctx context.Context, topic string, payload any, retained bool) error
	Subscribe(filter string, handler mqtt.MessageHandler) error
}

type Bridge struct {
	logger    *zap.Logger
	mqtt      Publisher
	timeout   time.Duration
	mu        sync.RWMutex
	available map[string]map[string]struct{}
	pending   map[string]chan json.RawMessage
}

func NewBridge(logger *zap.Logger, mqttClient Publisher, timeout time.Duration) (*Bridge, error) {
	b := &Bridge{
		logger:    logger,
		mqtt:      mqttClient,
		timeout:   timeout,
		available: map[string]map[string]struct{}{},
		pending:   map[string]chan json.RawMessage{},
	}
	if mqttClient != nil {
		if err := mqttClient.Subscribe(mqtt.TopicRPCAvailableWildcard(), b.handleAvailable); err != nil {
			return nil, err
		}
		if err := mqttClient.Subscribe(mqtt.TopicRPCResponseWildcard(), b.handleResponse); err != nil {
			return nil, err
		}
	}
	return b, nil
}

func (b *Bridge) AvailableMethods() gen.RpcAvailableMethods {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make(gen.RpcAvailableMethods, len(b.available))
	for method, handlers := range b.available {
		entry := gen.RpcAvailableMethodsEntry{HandlerId: make([]string, 0, len(handlers))}
		for handlerID := range handlers {
			entry.HandlerId = append(entry.HandlerId, handlerID)
		}
		out[method] = entry
	}
	return out
}

func (b *Bridge) Invoke(ctx context.Context, request gen.JsonRpcRequest) (json.RawMessage, bool, error) {
	if b.mqtt == nil {
		return nil, false, fmt.Errorf("mqtt bridge is not configured")
	}
	if strings.TrimSpace(request.Method) == "" || request.Jsonrpc != "2.0" {
		return nil, false, fmt.Errorf("invalid json-rpc request")
	}
	params := request.Params
	notifyOnly := request.Id == nil && (params == nil || params.UnderscoreCallerId == nil)
	topic := mqtt.TopicRPCRequest(request.Method)
	if params != nil && params.UnderscoreHandlerId != nil && strings.TrimSpace(*params.UnderscoreHandlerId) != "" {
		topic = mqtt.TopicRPCRequestHandler(request.Method, *params.UnderscoreHandlerId)
	}
	if notifyOnly {
		if err := b.mqtt.PublishJSON(ctx, topic, request, false); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	}

	callerID := uuid.NewString()
	if request.Params == nil {
		request.Params = &gen.JsonRpcRequest_Params{}
	}
	if request.Params.UnderscoreCallerId != nil && strings.TrimSpace(*request.Params.UnderscoreCallerId) != "" {
		callerID = *request.Params.UnderscoreCallerId
	} else {
		request.Params.UnderscoreCallerId = &callerID
	}

	waitTimeout := b.timeout
	if request.Params.UnderscoreTimeout != nil && *request.Params.UnderscoreTimeout > 0 {
		waitTimeout = time.Duration(*request.Params.UnderscoreTimeout) * time.Millisecond
	}
	respCh := make(chan json.RawMessage, 8)
	b.mu.Lock()
	b.pending[callerID] = respCh
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.pending, callerID)
		b.mu.Unlock()
	}()

	if err := b.mqtt.PublishJSON(ctx, topic, request, false); err != nil {
		return nil, false, err
	}

	waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()
	return b.collectResponses(waitCtx, request, respCh)
}

func (b *Bridge) handleAvailable(_ context.Context, topic string, payload []byte) error {
	parts := strings.Split(strings.Trim(topic, "/"), "/")
	if len(parts) < 5 {
		return nil
	}
	method := parts[len(parts)-1]
	handlerIDs := map[string]struct{}{}

	var single struct {
		HandlerID string `json:"handler_id"`
	}
	if err := json.Unmarshal(payload, &single); err == nil && strings.TrimSpace(single.HandlerID) != "" {
		handlerIDs[single.HandlerID] = struct{}{}
	}

	var entry gen.RpcAvailableMethodsEntry
	if err := json.Unmarshal(payload, &entry); err == nil {
		for _, id := range entry.HandlerId {
			if strings.TrimSpace(id) != "" {
				handlerIDs[id] = struct{}{}
			}
		}
	}

	if len(handlerIDs) == 0 {
		handlerIDs[method] = struct{}{}
	}
	b.mu.Lock()
	b.available[method] = handlerIDs
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
	respCh := b.pending[callerID]
	b.mu.RUnlock()
	if respCh == nil {
		return nil
	}
	select {
	case respCh <- append(json.RawMessage(nil), payload...):
	default:
		b.logger.Warn("dropping rpc response because pending channel is full", zap.String("caller_id", callerID))
	}
	return nil
}

func (b *Bridge) collectResponses(ctx context.Context, request gen.JsonRpcRequest, respCh <-chan json.RawMessage) (json.RawMessage, bool, error) {
	mode := gen.UnderscoreReturnFirstSuccess
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
				body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.Id, "result": map[string]any{"responses": all}})
				return body, false, nil
			case gen.UnderscoreReturnFirstError:
				if len(firstError) != 0 {
					return firstError, false, nil
				}
				if len(firstSuccess) != 0 {
					return firstSuccess, false, nil
				}
			default:
				if len(firstSuccess) != 0 {
					return firstSuccess, false, nil
				}
				if len(firstError) != 0 {
					return firstError, false, nil
				}
			}
			return nil, false, fmt.Errorf("rpc response timeout")
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
					return payload, false, nil
				}
				continue
			}
			if len(firstSuccess) == 0 {
				firstSuccess = payload
			}
			if mode == gen.UnderscoreReturnFirstSuccess {
				return payload, false, nil
			}
		}
	}
}

func responseIsError(payload []byte) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(payload, &probe); err != nil {
		return false
	}
	_, ok := probe["error"]
	return ok
}
