package observability

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/formation-res/open-location-hub/internal/config"
	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otellog "go.opentelemetry.io/otel/log"
	globallog "go.opentelemetry.io/otel/log/global"
	lognoop "go.opentelemetry.io/otel/log/noop"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	oteltrace "go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

const instrumentationName = "github.com/formation-res/open-location-hub/internal/observability"

type runtimeKey struct{}

type ingestTransportKey struct{}

// RuntimeMetricsSnapshot is the stable callback shape used for asynchronous
// queue and connection gauges.
type RuntimeMetricsSnapshot struct {
	EventBusDrops          int64
	NativeQueueDrops       int64
	DecisionQueueDrops     int64
	WebSocketOutboundDrops int64
	NativeQueueDepth       int64
	DecisionQueueDepth     int64
	EventBusSubscribers    int64
	WebSocketConnections   int64
	WebSocketOutboundDepth int64
}

// RuntimeMetricsSource provides runtime queue and connection snapshots for
// observable gauges.
type RuntimeMetricsSource interface {
	TelemetrySnapshot() RuntimeMetricsSnapshot
}

// Runtime owns OpenTelemetry providers, log bridging, and repository-specific
// instrumentation helpers.
type Runtime struct {
	cfg           config.TelemetryConfig
	resource      *resource.Resource
	tracer        oteltrace.Tracer
	meter         metric.Meter
	logger        otellog.LoggerProvider
	traceProvider *sdktrace.TracerProvider
	meterProvider *sdkmetric.MeterProvider
	logProvider   *sdklog.LoggerProvider
	instruments   telemetryInstruments
	sourceMu      sync.Mutex
	sourceReg     metric.Registration
	source        RuntimeMetricsSource
}

type telemetryInstruments struct {
	httpDuration              metric.Float64Histogram
	ingestRecordsTotal        metric.Int64Counter
	streamHandledTotal        metric.Int64Counter
	streamServiceDuration     metric.Float64Histogram
	processingDuration        metric.Float64Histogram
	processingBatchSize       metric.Int64Histogram
	processingBatchDuration   metric.Float64Histogram
	queueWaitDuration         metric.Float64Histogram
	endToEndDuration          metric.Float64Histogram
	dependencyEventsTotal     metric.Int64Counter
	runtimeDropEventsTotal    metric.Int64Counter
	mqttPublishDuration       metric.Float64Histogram
	websocketDispatchDuration metric.Float64Histogram
	websocketDispatchTotal    metric.Int64Counter
	rpcDuration               metric.Float64Histogram
	rpcTotal                  metric.Int64Counter
	eventBusEmitDuration      metric.Float64Histogram
	eventBusDrops             metric.Int64ObservableCounter
	nativeQueueDrops          metric.Int64ObservableCounter
	decisionQueueDrops        metric.Int64ObservableCounter
	websocketOutboundDrops    metric.Int64ObservableCounter
	nativeQueueDepth          metric.Int64ObservableGauge
	decisionQueueDepth        metric.Int64ObservableGauge
	eventBusSubscribers       metric.Int64ObservableGauge
	websocketConnections      metric.Int64ObservableGauge
	websocketOutboundDepth    metric.Int64ObservableGauge
}

type runtimeHolder struct {
	ptr atomic.Pointer[Runtime]
}

var globalRuntime runtimeHolder

func init() {
	globalRuntime.ptr.Store(newNoopRuntime())
}

// NewRuntime constructs a telemetry runtime from config and hub identity.
func NewRuntime(ctx context.Context, cfg config.TelemetryConfig, hubID string) (*Runtime, error) {
	if !cfg.Enabled {
		return newNoopRuntime(), nil
	}

	resAttrs := []attribute.KeyValue{
		semconv.ServiceNameKey.String(cfg.ServiceName),
	}
	baseResource := resource.Default()
	if _, present := baseResource.Set().Value(semconv.ServiceInstanceIDKey); !present {
		resAttrs = append(resAttrs, semconv.ServiceInstanceIDKey.String(uuid.NewString()))
	}
	if cfg.ServiceVersion != "" {
		resAttrs = append(resAttrs, semconv.ServiceVersionKey.String(cfg.ServiceVersion))
	}
	if cfg.DeploymentEnvironment != "" {
		resAttrs = append(resAttrs, semconv.DeploymentEnvironmentNameKey.String(cfg.DeploymentEnvironment))
	}
	if hubID != "" {
		resAttrs = append(resAttrs, attribute.String("hub.id", hubID))
	}
	res, err := resource.Merge(baseResource, resource.NewSchemaless(resAttrs...))
	if err != nil {
		return nil, err
	}

	rt := &Runtime{
		cfg:      cfg,
		resource: res,
		tracer:   tracenoop.NewTracerProvider().Tracer(instrumentationName),
		meter:    metricnoop.NewMeterProvider().Meter(instrumentationName),
		logger:   lognoop.NewLoggerProvider(),
	}

	if err := rt.initProviders(ctx); err != nil {
		_ = rt.Shutdown(context.Background())
		return nil, err
	}
	rt.initInstruments()
	return rt, nil
}

func newNoopRuntime() *Runtime {
	return &Runtime{
		tracer: tracenoop.NewTracerProvider().Tracer(instrumentationName),
		meter:  metricnoop.NewMeterProvider().Meter(instrumentationName),
		logger: lognoop.NewLoggerProvider(),
	}
}

func (r *Runtime) initProviders(ctx context.Context) error {
	if r.cfg.TracesEnabled {
		traceExporter, err := otlptracehttp.New(ctx, r.traceOptions()...)
		if err != nil {
			return err
		}
		sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(r.cfg.TraceSampleRatio))
		r.traceProvider = sdktrace.NewTracerProvider(
			sdktrace.WithResource(r.resource),
			sdktrace.WithSampler(sampler),
			sdktrace.WithBatcher(traceExporter),
		)
		r.tracer = r.traceProvider.Tracer(instrumentationName)
		otel.SetTracerProvider(r.traceProvider)
	}

	if r.cfg.MetricsEnabled {
		metricExporter, err := otlpmetrichttp.New(ctx, r.metricOptions()...)
		if err != nil {
			return err
		}
		reader := sdkmetric.NewPeriodicReader(
			metricExporter,
			sdkmetric.WithInterval(r.cfg.MetricsExportInterval),
			sdkmetric.WithTimeout(r.cfg.MetricsExportTimeout),
		)
		r.meterProvider = sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(r.resource),
			sdkmetric.WithReader(reader),
			sdkmetric.WithCardinalityLimit(2000),
		)
		r.meter = r.meterProvider.Meter(instrumentationName)
		otel.SetMeterProvider(r.meterProvider)
	}

	if r.cfg.LogsEnabled {
		logExporter, err := otlploghttp.New(ctx, r.logOptions()...)
		if err != nil {
			return err
		}
		r.logProvider = sdklog.NewLoggerProvider(
			sdklog.WithResource(r.resource),
			sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
		)
		r.logger = r.logProvider
		globallog.SetLoggerProvider(r.logProvider)
	}

	return nil
}

func (r *Runtime) traceOptions() []otlptracehttp.Option {
	opts := make([]otlptracehttp.Option, 0, 4)
	if endpoint := firstNonEmpty(r.cfg.OTLPTracesEndpoint, r.cfg.OTLPEndpoint); endpoint != "" {
		opts = append(opts, endpointOption(endpoint, func(v string) otlptracehttp.Option {
			return otlptracehttp.WithEndpoint(v)
		}, func(v string) otlptracehttp.Option {
			return otlptracehttp.WithEndpointURL(v)
		}))
	}
	if r.cfg.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	if len(r.cfg.OTLPHeaders) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(r.cfg.OTLPHeaders))
	}
	opts = append(opts, otlptracehttp.WithTimeout(r.cfg.ExportTimeout))
	return opts
}

func (r *Runtime) metricOptions() []otlpmetrichttp.Option {
	opts := make([]otlpmetrichttp.Option, 0, 4)
	if endpoint := firstNonEmpty(r.cfg.OTLPMetricsEndpoint, r.cfg.OTLPEndpoint); endpoint != "" {
		opts = append(opts, endpointOption(endpoint, func(v string) otlpmetrichttp.Option {
			return otlpmetrichttp.WithEndpoint(v)
		}, func(v string) otlpmetrichttp.Option {
			return otlpmetrichttp.WithEndpointURL(v)
		}))
	}
	if r.cfg.Insecure {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}
	if len(r.cfg.OTLPHeaders) > 0 {
		opts = append(opts, otlpmetrichttp.WithHeaders(r.cfg.OTLPHeaders))
	}
	opts = append(opts, otlpmetrichttp.WithTimeout(r.cfg.ExportTimeout))
	return opts
}

func (r *Runtime) logOptions() []otlploghttp.Option {
	opts := make([]otlploghttp.Option, 0, 4)
	if endpoint := firstNonEmpty(r.cfg.OTLPLogsEndpoint, r.cfg.OTLPEndpoint); endpoint != "" {
		opts = append(opts, endpointOption(endpoint, func(v string) otlploghttp.Option {
			return otlploghttp.WithEndpoint(v)
		}, func(v string) otlploghttp.Option {
			return otlploghttp.WithEndpointURL(v)
		}))
	}
	if r.cfg.Insecure {
		opts = append(opts, otlploghttp.WithInsecure())
	}
	if len(r.cfg.OTLPHeaders) > 0 {
		opts = append(opts, otlploghttp.WithHeaders(r.cfg.OTLPHeaders))
	}
	opts = append(opts, otlploghttp.WithTimeout(r.cfg.ExportTimeout))
	return opts
}

func endpointOption[T any](value string, raw func(string) T, url func(string) T) T {
	if parsed, ok := endpointURL(value); ok {
		if parsed.Path != "" && parsed.Path != "/" {
			return url(value)
		}
		return raw(parsed.Host)
	}
	return raw(value)
}

func endpointURL(value string) (*url.URL, bool) {
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, false
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, false
	}
	return parsed, true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (r *Runtime) initInstruments() {
	latencyBuckets := []float64{0.000001, 0.00001, 0.0001, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
	waitBuckets := append(append([]float64(nil), latencyBuckets...), 30, 60, 300, 1800, 3600, 21600)
	durationHistogram := func(name string, buckets []float64) metric.Float64Histogram {
		histogram, _ := r.meter.Float64Histogram(name, metric.WithUnit("s"),
			metric.WithExplicitBucketBoundaries(buckets...))
		return histogram
	}
	_, _ = r.meter.Int64ObservableGauge("hub.runtime.alive", metric.WithInt64Callback(
		func(_ context.Context, observer metric.Int64Observer) error {
			observer.Observe(1)
			return nil
		},
	))
	r.instruments.httpDuration = durationHistogram("hub.http.request.duration", latencyBuckets)
	r.instruments.ingestRecordsTotal, _ = r.meter.Int64Counter("hub.ingest.records_total")
	r.instruments.streamHandledTotal, _ = r.meter.Int64Counter("hub.work.items.handled")
	r.instruments.streamServiceDuration = durationHistogram("hub.work.service.duration", latencyBuckets)
	r.instruments.processingDuration = durationHistogram("hub.processing.duration", latencyBuckets)
	r.instruments.processingBatchSize, _ = r.meter.Int64Histogram("hub.processing.batch_size")
	r.instruments.processingBatchDuration = durationHistogram("hub.processing.batch_duration", latencyBuckets)
	r.instruments.queueWaitDuration = durationHistogram("hub.processing.queue_wait_duration", waitBuckets)
	r.instruments.endToEndDuration = durationHistogram("hub.processing.end_to_end_duration", waitBuckets)
	r.instruments.dependencyEventsTotal, _ = r.meter.Int64Counter("hub.runtime.dependency_events_total")
	r.instruments.runtimeDropEventsTotal, _ = r.meter.Int64Counter("hub.runtime.drop_events_total")
	r.instruments.mqttPublishDuration = durationHistogram("hub.mqtt.publish_duration", latencyBuckets)
	r.instruments.websocketDispatchDuration = durationHistogram("hub.websocket.dispatch_duration", latencyBuckets)
	r.instruments.websocketDispatchTotal, _ = r.meter.Int64Counter("hub.websocket.dispatch_total")
	r.instruments.rpcDuration = durationHistogram("hub.rpc.duration", latencyBuckets)
	r.instruments.rpcTotal, _ = r.meter.Int64Counter("hub.rpc.requests_total")
	r.instruments.eventBusEmitDuration = durationHistogram("hub.event_bus.emit_duration", latencyBuckets)
	r.instruments.eventBusDrops, _ = r.meter.Int64ObservableCounter("hub.runtime.event_bus_drops_total")
	r.instruments.nativeQueueDrops, _ = r.meter.Int64ObservableCounter("hub.runtime.native_queue_drops_total")
	r.instruments.decisionQueueDrops, _ = r.meter.Int64ObservableCounter("hub.runtime.decision_queue_drops_total")
	r.instruments.websocketOutboundDrops, _ = r.meter.Int64ObservableCounter("hub.runtime.websocket_outbound_drops_total")
	r.instruments.nativeQueueDepth, _ = r.meter.Int64ObservableGauge("hub.runtime.native_queue_depth")
	r.instruments.decisionQueueDepth, _ = r.meter.Int64ObservableGauge("hub.runtime.decision_queue_depth")
	r.instruments.eventBusSubscribers, _ = r.meter.Int64ObservableGauge("hub.runtime.event_bus_subscribers")
	r.instruments.websocketConnections, _ = r.meter.Int64ObservableGauge("hub.runtime.websocket_connections")
	r.instruments.websocketOutboundDepth, _ = r.meter.Int64ObservableGauge("hub.runtime.websocket_outbound_depth")
}

// SetGlobal installs rt as the process-wide telemetry runtime.
func SetGlobal(rt *Runtime) {
	if rt == nil {
		rt = newNoopRuntime()
	}
	globalRuntime.ptr.Store(rt)
}

// Global returns the active telemetry runtime.
func Global() *Runtime {
	rt := globalRuntime.ptr.Load()
	if rt == nil {
		return newNoopRuntime()
	}
	return rt
}

// Shutdown flushes all telemetry providers.
func (r *Runtime) Shutdown(ctx context.Context) error {
	var err error
	if r.sourceReg != nil {
		err = errors.Join(err, r.sourceReg.Unregister())
		r.sourceReg = nil
	}
	if r.meterProvider != nil {
		err = errors.Join(err, r.meterProvider.Shutdown(ctx))
	}
	if r.traceProvider != nil {
		err = errors.Join(err, r.traceProvider.Shutdown(ctx))
	}
	if r.logProvider != nil {
		err = errors.Join(err, r.logProvider.Shutdown(ctx))
	}
	return err
}

// LoggerProvider returns the OTEL log provider used by the Zap bridge.
func (r *Runtime) LoggerProvider() otellog.LoggerProvider {
	if r == nil || r.logger == nil {
		return lognoop.NewLoggerProvider()
	}
	return r.logger
}

func (r *Runtime) metricsReady() bool {
	return r != nil && r.meterProvider != nil
}

// LogsEnabled reports whether OTLP log export is enabled.
func (r *Runtime) LogsEnabled() bool {
	return r != nil && r.cfg.Enabled && r.cfg.LogsEnabled
}

// DebugIdentifiersEnabled reports whether richer debug identifier tagging is enabled.
func (r *Runtime) DebugIdentifiersEnabled() bool {
	return r != nil && r.cfg.DebugIdentifiers
}

// BridgeCore returns the OTEL Zap core for teeing logs to OTLP.
func (r *Runtime) BridgeCore(name string) *otelzap.Core {
	return otelzap.NewCore(name, otelzap.WithLoggerProvider(r.LoggerProvider()))
}

// StartSpan starts a repository-scoped span.
func (r *Runtime) StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	if r == nil {
		return tracenoop.NewTracerProvider().Tracer(instrumentationName).Start(ctx, name)
	}
	return r.tracer.Start(ctx, name, oteltrace.WithAttributes(attrs...))
}

// WithRuntime attaches a runtime to a context for downstream helpers.
func WithRuntime(ctx context.Context, rt *Runtime) context.Context {
	return context.WithValue(ctx, runtimeKey{}, rt)
}

// RuntimeFromContext returns the telemetry runtime from context or the global runtime.
func RuntimeFromContext(ctx context.Context) *Runtime {
	if ctx != nil {
		if rt, ok := ctx.Value(runtimeKey{}).(*Runtime); ok && rt != nil {
			return rt
		}
	}
	return Global()
}

// WithIngestTransport annotates a context with the ingest transport.
func WithIngestTransport(ctx context.Context, transport string) context.Context {
	return context.WithValue(ctx, ingestTransportKey{}, transport)
}

// IngestTransportFromContext returns the ingest transport name or "unknown".
func IngestTransportFromContext(ctx context.Context) string {
	if ctx != nil {
		if value, ok := ctx.Value(ingestTransportKey{}).(string); ok && value != "" {
			return value
		}
	}
	return "unknown"
}

// RegisterRuntimeMetricsSource installs a callback source for queue and connection gauges.
func (r *Runtime) RegisterRuntimeMetricsSource(source RuntimeMetricsSource) error {
	if !r.metricsReady() {
		return nil
	}
	r.sourceMu.Lock()
	defer r.sourceMu.Unlock()
	if r.sourceReg != nil {
		_ = r.sourceReg.Unregister()
		r.sourceReg = nil
	}
	r.source = source
	if source == nil {
		return nil
	}
	reg, err := r.meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		snapshot := source.TelemetrySnapshot()
		observer.ObserveInt64(r.instruments.eventBusDrops, snapshot.EventBusDrops)
		observer.ObserveInt64(r.instruments.nativeQueueDrops, snapshot.NativeQueueDrops)
		observer.ObserveInt64(r.instruments.decisionQueueDrops, snapshot.DecisionQueueDrops)
		observer.ObserveInt64(r.instruments.websocketOutboundDrops, snapshot.WebSocketOutboundDrops)
		observer.ObserveInt64(r.instruments.nativeQueueDepth, snapshot.NativeQueueDepth)
		observer.ObserveInt64(r.instruments.decisionQueueDepth, snapshot.DecisionQueueDepth)
		observer.ObserveInt64(r.instruments.eventBusSubscribers, snapshot.EventBusSubscribers)
		observer.ObserveInt64(r.instruments.websocketConnections, snapshot.WebSocketConnections)
		observer.ObserveInt64(r.instruments.websocketOutboundDepth, snapshot.WebSocketOutboundDepth)
		return nil
	},
		r.instruments.eventBusDrops,
		r.instruments.nativeQueueDrops,
		r.instruments.decisionQueueDrops,
		r.instruments.websocketOutboundDrops,
		r.instruments.nativeQueueDepth,
		r.instruments.decisionQueueDepth,
		r.instruments.eventBusSubscribers,
		r.instruments.websocketConnections,
		r.instruments.websocketOutboundDepth,
	)
	if err != nil {
		return err
	}
	r.sourceReg = reg
	return nil
}

// RecordIngestRecord records a bounded ingest outcome counter.
func (r *Runtime) RecordIngestRecord(ctx context.Context, signal, outcome string) {
	if !r.metricsReady() {
		return
	}
	r.instruments.ingestRecordsTotal.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("signal", signal),
			attribute.String("transport", IngestTransportFromContext(ctx)),
			attribute.String("outcome", outcome),
		))
}

// RecordHTTPRequest records one completed HTTP request by route template.
func (r *Runtime) RecordHTTPRequest(ctx context.Context, route, method string, status int, duration time.Duration) {
	if !r.metricsReady() {
		return
	}
	r.instruments.httpDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(
		attribute.String("route", route),
		attribute.String("method", method),
		attribute.Int("status", status),
	))
}

// RecordStreamHandled counts one completed stream work item by bounded operation and outcome.
func (r *Runtime) RecordStreamHandled(ctx context.Context, stage, operation, machine, mode, outcome string) {
	if !r.metricsReady() {
		return
	}
	r.instruments.streamHandledTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("stage", stage), attribute.String("operation", operation),
		attribute.String("machine", machine), attribute.String("traffic_mode", mode), attribute.String("outcome", outcome),
	))
}

// RecordStreamServiceTime observes the handler's service time separately from queueing and commit.
func (r *Runtime) RecordStreamServiceTime(ctx context.Context, stage, operation, machine, mode string, duration time.Duration) {
	if !r.metricsReady() {
		return
	}
	r.instruments.streamServiceDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(
		attribute.String("stage", stage), attribute.String("operation", operation),
		attribute.String("machine", machine), attribute.String("traffic_mode", mode),
	))
}

// RecordMachineEvaluation records an evaluation of a single state-machine key.
func (r *Runtime) RecordMachineEvaluation(ctx context.Context, machine, trigger, mode, outcome string, duration time.Duration, err error) {
	if err != nil {
		outcome = "failed"
	}
	r.RecordStreamHandled(ctx, "state_machine", trigger, machine, mode, outcome)
	r.RecordStreamServiceTime(ctx, "state_machine", trigger, machine, mode, duration)
}

// RecordProcessingDuration records a stage duration in seconds.
func (r *Runtime) RecordProcessingDuration(ctx context.Context, stage, signal string, duration time.Duration) {
	if !r.metricsReady() {
		return
	}
	r.instruments.processingDuration.Record(ctx, duration.Seconds(),
		metric.WithAttributes(
			attribute.String("stage", stage),
			attribute.String("signal", signal),
			attribute.String("transport", IngestTransportFromContext(ctx)),
		))
}

// RecordBatchProcessing records batch size and duration for low-cardinality stages.
func (r *Runtime) RecordBatchProcessing(ctx context.Context, stage string, size int, duration time.Duration) {
	if !r.metricsReady() {
		return
	}
	attrs := metric.WithAttributes(attribute.String("stage", stage))
	r.instruments.processingBatchSize.Record(ctx, int64(size), attrs)
	r.instruments.processingBatchDuration.Record(ctx, duration.Seconds(), attrs)
}

// RecordQueueWait records queue wait time for a stage.
func (r *Runtime) RecordQueueWait(ctx context.Context, stage string, duration time.Duration) {
	if !r.metricsReady() {
		return
	}
	r.instruments.queueWaitDuration.Record(ctx, max(0, duration).Seconds(),
		metric.WithAttributes(attribute.String("stage", stage)))
}

// RecordEndToEnd records the delta between event time and processing time.
func (r *Runtime) RecordEndToEnd(ctx context.Context, kind, scope string, duration time.Duration) {
	if !r.metricsReady() {
		return
	}
	if duration < 0 {
		duration = 0
	}
	r.instruments.endToEndDuration.Record(ctx, duration.Seconds(),
		metric.WithAttributes(
			attribute.String("event_kind", kind),
			attribute.String("scope", scope),
		))
}

// RecordDependencyEvent records dependency lifecycle events and outcomes.
func (r *Runtime) RecordDependencyEvent(ctx context.Context, dependency, event, outcome string) {
	if !r.metricsReady() {
		return
	}
	r.instruments.dependencyEventsTotal.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("dependency", dependency),
			attribute.String("event", event),
			attribute.String("outcome", outcome),
		))
}

// RecordRuntimeDrop records a dropped work item or event with bounded cause labels.
func (r *Runtime) RecordRuntimeDrop(ctx context.Context, stage, reason string) {
	if !r.metricsReady() {
		return
	}
	r.instruments.runtimeDropEventsTotal.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("stage", stage),
			attribute.String("reason", reason),
		))
}

// RecordMQTTPublish records MQTT publish duration and outcome.
func (r *Runtime) RecordMQTTPublish(ctx context.Context, outcome string, duration time.Duration) {
	if !r.metricsReady() {
		return
	}
	r.instruments.mqttPublishDuration.Record(ctx, duration.Seconds(),
		metric.WithAttributes(attribute.String("outcome", outcome)))
}

// RecordWebSocketDispatch records websocket outbound duration and outcome.
func (r *Runtime) RecordWebSocketDispatch(ctx context.Context, topic, outcome string, duration time.Duration) {
	if !r.metricsReady() {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("topic", topic),
		attribute.String("outcome", outcome),
	}
	r.instruments.websocketDispatchDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(attrs...))
	r.instruments.websocketDispatchTotal.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// RecordRPC records RPC duration and outcome.
func (r *Runtime) RecordRPC(ctx context.Context, method, outcome string, duration time.Duration) {
	if !r.metricsReady() {
		return
	}
	attrs := metric.WithAttributes(
		attribute.String("method", method),
		attribute.String("outcome", outcome),
	)
	r.instruments.rpcDuration.Record(ctx, duration.Seconds(), attrs)
	r.instruments.rpcTotal.Add(ctx, 1, attrs)
}

// RecordEventBusEmit records event-bus dispatch duration.
func (r *Runtime) RecordEventBusEmit(ctx context.Context, kind string, duration time.Duration) {
	if !r.metricsReady() {
		return
	}
	r.instruments.eventBusEmitDuration.Record(ctx, duration.Seconds(),
		metric.WithAttributes(attribute.String("event_kind", kind)))
}
