package hub

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"go.uber.org/zap"
)

const decisionQueueBatchSize = 64

type derivedLocationWork struct {
	Context    context.Context
	Location   gen.Location
	EnqueuedAt time.Time
}

type derivedLocationSubmitter interface {
	Submit(work derivedLocationWork)
}

type derivedLocationProcessor struct {
	service        *Service
	logger         *zap.Logger
	queue          chan derivedLocationWork
	label          string
	onDrop         func() uint64
	onProcess      func(context.Context, gen.Location) error
	onProcessBatch func(context.Context, []derivedLocationWork) error
	maxBatchSize   int
	dropped        atomic.Uint64
}

func startDerivedLocationProcessor(
	ctx context.Context,
	service *Service,
	buffer int,
	label string,
	onDrop func() uint64,
	onProcess func(context.Context, gen.Location) error,
) derivedLocationSubmitter {
	if service == nil || buffer <= 0 {
		return nil
	}
	processor := &derivedLocationProcessor{
		service:   service,
		logger:    service.logger,
		queue:     make(chan derivedLocationWork, buffer),
		label:     label,
		onDrop:    onDrop,
		onProcess: onProcess,
	}
	if label == "decision location queue" {
		processor.maxBatchSize = decisionQueueBatchSize
		processor.onProcessBatch = service.processDecisionLocationBatch
	}
	go processor.run(ctx)
	return processor
}

func (p *derivedLocationProcessor) Submit(work derivedLocationWork) {
	select {
	case p.queue <- work:
		p.updateDepth()
	default:
		dropped := p.dropped.Add(1)
		if p.service != nil && p.service.stats != nil {
			depth := int64(len(p.queue))
			switch p.label {
			case "native location queue":
				dropped = p.service.stats.RecordNativeQueueDrop(work, depth)
			case "decision location queue":
				dropped = p.service.stats.RecordDecisionQueueDrop(work, depth)
			}
		} else if p.onDrop != nil {
			dropped = p.onDrop()
		}
		if dropped == 1 || dropped%100 == 0 {
			p.logger.Warn(
				p.label+" full; dropping location work",
				zap.Uint64("dropped", dropped),
				zap.String("provider_id", work.Location.ProviderId),
				zap.String("source", work.Location.Source),
				zap.Int("queue_depth", len(p.queue)),
			)
		}
	}
}

func (p *derivedLocationProcessor) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case work := <-p.queue:
			if p.onProcessBatch != nil && p.maxBatchSize > 1 {
				batch := p.dequeueBatch(work)
				if err := p.processBatch(ctx, batch); err != nil {
					p.logger.Warn(p.label+" batch processing failed", zap.Any("context", ctx), zap.Int("batch_size", len(batch)), zap.Error(err))
				}
				continue
			}
			p.updateDepth()
			if !work.EnqueuedAt.IsZero() {
				p.service.telemetry().RecordQueueWait(work.Context, p.label, time.Since(work.EnqueuedAt))
			}
			if err := p.onProcess(ctx, work.Location); err != nil {
				p.logger.Warn(p.label+" processing failed", zap.Any("context", work.Context), zap.Error(err), zap.String("provider_id", work.Location.ProviderId), zap.String("source", work.Location.Source))
			}
		}
	}
}

func (p *derivedLocationProcessor) dequeueBatch(first derivedLocationWork) []derivedLocationWork {
	batch := make([]derivedLocationWork, 0, p.maxBatchSize)
	batch = append(batch, first)
	for len(batch) < p.maxBatchSize {
		select {
		case work := <-p.queue:
			batch = append(batch, work)
		default:
			p.updateDepth()
			return batch
		}
	}
	p.updateDepth()
	return batch
}

func (p *derivedLocationProcessor) processBatch(ctx context.Context, batch []derivedLocationWork) error {
	start := time.Now()
	for _, work := range batch {
		if !work.EnqueuedAt.IsZero() {
			p.service.telemetry().RecordQueueWait(work.Context, p.label, time.Since(work.EnqueuedAt))
		}
	}
	err := p.onProcessBatch(ctx, batch)
	p.service.telemetry().RecordBatchProcessing(ctx, p.label, len(batch), time.Since(start))
	return err
}

func (p *derivedLocationProcessor) updateDepth() {
	if p.service == nil || p.service.stats == nil {
		return
	}
	depth := int64(len(p.queue))
	switch p.label {
	case "native location queue":
		p.service.stats.SetNativeQueueDepth(depth)
	case "decision location queue":
		p.service.stats.SetDecisionQueueDepth(depth)
	}
}

type decisionLocationStage interface {
	Process(context.Context, gen.Location) (gen.Location, bool, error)
}

type passthroughDecisionStage struct{}

func (passthroughDecisionStage) Process(_ context.Context, location gen.Location) (gen.Location, bool, error) {
	return location, true, nil
}

type derivedLocationView struct {
	service  *Service
	location gen.Location

	localOnce sync.Once
	localLoc  gen.Location
	localOK   bool
	localErr  error

	wgsOnce sync.Once
	wgsLoc  gen.Location
	wgsOK   bool
	wgsErr  error
}

func newDerivedLocationView(service *Service, location gen.Location) *derivedLocationView {
	return &derivedLocationView{service: service, location: location}
}

func (v *derivedLocationView) NativeScope() EventScope {
	return nativeLocationScope(v.location)
}

func (v *derivedLocationView) WGS84(ctx context.Context) (*gen.Location, bool, error) {
	v.wgsOnce.Do(func() {
		if locationCRS(v.location) == "EPSG:4326" {
			out, err := cloneLocation(v.location)
			if err != nil {
				v.wgsErr = err
				return
			}
			epsg := "EPSG:4326"
			out.Crs = &epsg
			v.wgsLoc = out
			v.wgsOK = true
			return
		}
		out, err := v.service.locationToWGS84(ctx, v.location)
		if err != nil {
			v.wgsErr = err
			return
		}
		v.wgsLoc = out
		v.wgsOK = true
	})
	if !v.wgsOK {
		return nil, false, v.wgsErr
	}
	return &v.wgsLoc, true, nil
}

func (v *derivedLocationView) Local(ctx context.Context) (*gen.Location, bool, error) {
	v.localOnce.Do(func() {
		if nativeLocationScope(v.location) == ScopeLocal {
			out, err := cloneLocation(v.location)
			if err != nil {
				v.localErr = err
				return
			}
			localCRS := "local"
			out.Crs = &localCRS
			v.localLoc = out
			v.localOK = true
			return
		}
		out, err := v.service.locationToLocal(ctx, v.location)
		if err != nil {
			v.localErr = err
			return
		}
		v.localLoc = out
		v.localOK = true
	})
	if !v.localOK {
		return nil, false, v.localErr
	}
	return &v.localLoc, true, nil
}
