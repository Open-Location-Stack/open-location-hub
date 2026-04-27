package hub

import (
	"context"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"go.uber.org/zap"
)

const decisionQueueBatchSize = 64
const defaultDecisionWorkerCount = 4

type derivedLocationWork struct {
	Context    context.Context
	Location   gen.Location
	EnqueuedAt time.Time
}

type collisionWork struct {
	Context    context.Context
	Motions    []gen.TrackableMotion
	EnqueuedAt time.Time
}

type derivedLocationSubmitter interface {
	Submit(work derivedLocationWork)
}

type collisionWorkSubmitter interface {
	Submit(work collisionWork)
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
	reportDepth    func(int64)
	queueDepth     func() int64
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
	processor.reportDepth = processor.defaultDepthReporter()
	processor.queueDepth = func() int64 { return int64(len(processor.queue)) }
	if label == "decision location queue" {
		processor.maxBatchSize = decisionQueueBatchSize
		processor.onProcessBatch = service.processDecisionLocationBatch
	}
	go processor.run(ctx)
	return processor
}

func startShardedDerivedLocationProcessor(
	ctx context.Context,
	service *Service,
	buffer int,
	workers int,
	label string,
	onDrop func() uint64,
	onProcess func(context.Context, gen.Location) error,
) derivedLocationSubmitter {
	if service == nil || buffer <= 0 {
		return nil
	}
	if workers <= 1 {
		return startDerivedLocationProcessor(ctx, service, buffer, label, onDrop, onProcess)
	}
	if workers > buffer {
		workers = buffer
	}
	if workers <= 1 {
		return startDerivedLocationProcessor(ctx, service, buffer, label, onDrop, onProcess)
	}
	group := &shardedDerivedLocationProcessor{
		shards:      make([]*derivedLocationProcessor, 0, workers),
		queueDepths: make([]atomic.Int64, workers),
	}
	perShardBuffer := max(1, (buffer+workers-1)/workers)
	for i := 0; i < workers; i++ {
		shard := &derivedLocationProcessor{
			service:   service,
			logger:    service.logger,
			queue:     make(chan derivedLocationWork, perShardBuffer),
			label:     label,
			onDrop:    onDrop,
			onProcess: onProcess,
		}
		if label == "decision location queue" {
			shard.maxBatchSize = decisionQueueBatchSize
			shard.onProcessBatch = service.processDecisionLocationBatch
		}
		index := i
		shard.reportDepth = func(depth int64) {
			group.queueDepths[index].Store(depth)
			group.reportDepth(service, label)
		}
		shard.queueDepth = func() int64 {
			return group.totalDepth()
		}
		group.shards = append(group.shards, shard)
		go shard.run(ctx)
	}
	return group
}

type shardedDerivedLocationProcessor struct {
	shards      []*derivedLocationProcessor
	queueDepths []atomic.Int64
}

func (p *shardedDerivedLocationProcessor) Submit(work derivedLocationWork) {
	if len(p.shards) == 0 {
		return
	}
	p.shards[decisionShardIndex(work, len(p.shards))].Submit(work)
}

func (p *shardedDerivedLocationProcessor) totalDepth() int64 {
	var total int64
	for i := range p.queueDepths {
		total += p.queueDepths[i].Load()
	}
	return total
}

func (p *shardedDerivedLocationProcessor) reportDepth(service *Service, label string) {
	if service == nil || service.stats == nil {
		return
	}
	switch label {
	case "decision location queue":
		service.stats.SetDecisionQueueDepth(p.totalDepth())
	case "native location queue":
		service.stats.SetNativeQueueDepth(p.totalDepth())
	}
}

func decisionShardIndex(work derivedLocationWork, count int) int {
	if count <= 1 {
		return 0
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(work.Location.ProviderId))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(work.Location.Source))
	return int(hasher.Sum32() % uint32(count))
}

func (p *derivedLocationProcessor) Submit(work derivedLocationWork) {
	select {
	case p.queue <- work:
		p.updateDepth()
	default:
		dropped := p.dropped.Add(1)
		if p.service != nil && p.service.stats != nil {
			depth := int64(len(p.queue))
			if p.queueDepth != nil {
				depth = p.queueDepth()
			}
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
	depth := int64(len(p.queue))
	if p.reportDepth != nil {
		p.reportDepth(depth)
		return
	}
	if p.service == nil || p.service.stats == nil {
		return
	}
}

func (p *derivedLocationProcessor) defaultDepthReporter() func(int64) {
	return func(depth int64) {
		if p.service == nil || p.service.stats == nil {
			return
		}
		switch p.label {
		case "native location queue":
			p.service.stats.SetNativeQueueDepth(depth)
		case "decision location queue":
			p.service.stats.SetDecisionQueueDepth(depth)
		}
	}
}

type collisionMotionProcessor struct {
	service   *Service
	logger    *zap.Logger
	queue     chan collisionWork
	queueWait string
}

func startCollisionMotionProcessor(ctx context.Context, service *Service, buffer int) collisionWorkSubmitter {
	if service == nil || buffer <= 0 {
		return nil
	}
	processor := &collisionMotionProcessor{
		service:   service,
		logger:    service.logger,
		queue:     make(chan collisionWork, buffer),
		queueWait: "collision queue",
	}
	go processor.run(ctx)
	return processor
}

func (p *collisionMotionProcessor) Submit(work collisionWork) {
	if len(work.Motions) == 0 {
		return
	}
	select {
	case p.queue <- work:
	default:
		p.logger.Debug("collision queue full; dropping collision work", zap.Int("motions", len(work.Motions)))
	}
}

func (p *collisionMotionProcessor) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case work := <-p.queue:
			if !work.EnqueuedAt.IsZero() {
				p.service.telemetry().RecordQueueWait(work.Context, p.queueWait, time.Since(work.EnqueuedAt))
			}
			if err := p.service.publishCollisionEvents(work.Context, work.Motions); err != nil {
				p.logger.Warn("collision queue processing failed", zap.Any("context", work.Context), zap.Int("motions", len(work.Motions)), zap.Error(err))
			}
		}
	}
}

func decisionWorkerCount() int {
	return defaultDecisionWorkerCount
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
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
