package mqtt

import (
	"context"
	"sync/atomic"

	"github.com/formation-res/open-location-hub/internal/observability"
	"go.uber.org/zap"
)

// HandlerConfig bounds concurrent inbound handlers and queued MQTT messages.
// Zero values select four workers and a queue of 1024 messages.
type HandlerConfig struct {
	Workers int
	Buffer  int
}

type inboundMessage struct {
	ctx     context.Context
	handler MessageHandler
	topic   string
	payload []byte
}

type messageDispatcher struct {
	ctx     context.Context
	cancel  context.CancelFunc
	logger  *zap.Logger
	queue   chan inboundMessage
	dropped atomic.Uint64
}

func newMessageDispatcher(ctx context.Context, logger *zap.Logger, cfg HandlerConfig) *messageDispatcher {
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.Buffer <= 0 {
		cfg.Buffer = 1024
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	ctx, cancel := context.WithCancel(ctx)
	d := &messageDispatcher{ctx: ctx, cancel: cancel, logger: logger, queue: make(chan inboundMessage, cfg.Buffer)}
	for range cfg.Workers {
		go d.run()
	}
	return d
}

// Paho callbacks must not block: a handler may need Paho to receive its publish
// acknowledgment. Queue saturation therefore drops new work with diagnostics.
func (d *messageDispatcher) submit(message inboundMessage) {
	if d.ctx.Err() != nil {
		return
	}
	select {
	case <-d.ctx.Done():
	case d.queue <- message:
	default:
		dropped := d.dropped.Add(1)
		observability.Global().RecordRuntimeDrop(d.ctx, "mqtt_handler", "queue_full")
		if dropped == 1 || dropped%100 == 0 {
			d.logger.Warn("mqtt handler queue full; dropping inbound message", zap.Uint64("dropped", dropped), zap.String("topic", message.topic))
		}
	}
}

func (d *messageDispatcher) run() {
	for {
		select {
		case <-d.ctx.Done():
			return
		case message := <-d.queue:
			if d.ctx.Err() != nil {
				return
			}
			if err := message.handler(message.ctx, message.topic, message.payload); err != nil {
				observability.Global().RecordDependencyEvent(message.ctx, "mqtt", "handler", "failure")
				d.logger.Warn("mqtt handler failed", zap.Error(err), zap.String("topic", message.topic))
			}
		}
	}
}
