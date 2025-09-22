// Module path suggestion:
//   github.com/plentymarkets/newrelic-otel-shim/newrelic
// Then in your app's go.mod add a replace directive:
//   replace github.com/newrelic/go-agent/v3/newrelic => github.com/plentymarkets/newrelic-otel-shim/newrelic v0.0.0
// This lets you keep the existing import path and only swap the dependency.

package newrelic

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ==============================
// Public surface compatible with common newrelic v3 API
// (subset sufficient for most apps)
// ==============================

// Config mirrors the commonly used fields of newrelic.Config.
type Config struct {
	AppName string
	License string // ignored in this shim
	Enabled bool

	// NR v3 has DistributedTracer.Enabled; keep a flat flag for convenience.
	DistributedTracerEnabled bool

	// OTel exporter (to your Collector) via gRPC.
	OTLPEndpoint string            // e.g. "otel-collector:4317"
	Insecure     bool              // plaintext for in-cluster collectors
	Headers      map[string]string // optional gRPC headers (sent per RPC)
}

// Configuration option functions (functional options pattern like New Relic v3)
type ConfigOption func(*Config)

// ConfigFromEnvironment reads configuration from environment variables following OpenTelemetry standards
func ConfigFromEnvironment() ConfigOption {
	return func(c *Config) {
		// OTEL_EXPORTER_OTLP_ENDPOINT - Standard OTel environment variable
		if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
			c.OTLPEndpoint = endpoint
		}

		// OTEL_EXPORTER_OTLP_INSECURE - Standard OTel environment variable for insecure connections
		if insecure := os.Getenv("OTEL_EXPORTER_OTLP_INSECURE"); insecure != "" {
			if parsed, err := strconv.ParseBool(insecure); err == nil {
				c.Insecure = parsed
			}
		}

		// NEW_RELIC_APP_NAME - Traditional New Relic environment variable
		if appName := os.Getenv("NEW_RELIC_APP_NAME"); appName != "" {
			c.AppName = appName
		}

		// OTEL_SERVICE_NAME - Standard OTel environment variable for service name
		if serviceName := os.Getenv("OTEL_SERVICE_NAME"); serviceName != "" {
			c.AppName = serviceName
		}

		// NEW_RELIC_ENABLED - Traditional New Relic environment variable
		if enabled := os.Getenv("NEW_RELIC_ENABLED"); enabled != "" {
			if parsed, err := strconv.ParseBool(enabled); err == nil {
				c.Enabled = parsed
			}
		}

		// OTEL_ENABLED - Standard OTel environment variable
		if otelEnabled := os.Getenv("OTEL_ENABLED"); otelEnabled != "" {
			if parsed, err := strconv.ParseBool(otelEnabled); err == nil {
				c.Enabled = parsed
			}
		}

		// OTEL_EXPORTER_OTLP_HEADERS - Standard OTel environment variable for gRPC headers
		// Format: "key1=value1,key2=value2"
		if headers := os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"); headers != "" {
			if c.Headers == nil {
				c.Headers = make(map[string]string)
			}
			pairs := strings.Split(headers, ",")
			for _, pair := range pairs {
				if kv := strings.SplitN(strings.TrimSpace(pair), "=", 2); len(kv) == 2 {
					c.Headers[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
				}
			}
		}
	}
}

// ConfigAppName sets the application name
func ConfigAppName(name string) ConfigOption {
	return func(c *Config) {
		c.AppName = name
	}
}

func ConfigOtlpEndpoint(endpoint string) ConfigOption {
	return func(c *Config) {
		c.OTLPEndpoint = endpoint
	}
}

func ConfigOtlpInsecure(insecure bool) ConfigOption {
	return func(c *Config) {
		c.Insecure = insecure
	}
}

func ConfigOtlpHeaders(headers map[string]string) ConfigOption {
	return func(c *Config) {
		c.Headers = headers
	}
}

// ConfigLicense sets the license key (ignored in this shim)
func ConfigLicense(key string) ConfigOption {
	return func(c *Config) {
		c.License = key
	}
}

// ConfigEnabled sets whether the agent is enabled
func ConfigEnabled(enabled bool) ConfigOption {
	return func(c *Config) {
		c.Enabled = enabled
	}
}

// ConfigDistributedTracerEnabled sets whether distributed tracing is enabled
func ConfigDistributedTracerEnabled(enabled bool) ConfigOption {
	return func(c *Config) {
		// Ignored in this shim; always enabled
	}
}

// ConfigAppLogForwardingEnabled sets whether application log forwarding is enabled
// This is a noop in this shim; included for API compatibility
func ConfigAppLogForwardingEnabled(enabled bool) ConfigOption {
	return func(c *Config) {
		// Noop - log forwarding not implemented in this shim
		// Included for drop-in replacement compatibility
	}
}

// Application mirrors newrelic.Application (subset).
type Application struct {
	cfg      Config
	tracer   trace.Tracer
	shutdown func(context.Context) error

	// OpenTelemetry metrics
	meter metric.Meter

	// Metrics registry to prevent duplicate metric registration
	metricsRegistry map[string]interface{} // stores OTel metrics by name
	metricsMutex    sync.RWMutex
}

func (a *Application) Config() Config {
	return a.cfg
}

// NewApplication creates a new Application with functional options (like New Relic v3)
func NewApplication(opts ...ConfigOption) (*Application, error) {
	// Default configuration
	cfg := Config{
		AppName:                  "default-app",
		License:                  "",
		Enabled:                  true,
		DistributedTracerEnabled: true,
		OTLPEndpoint:             "otel-collector:4317",
		Insecure:                 true,
		Headers:                  make(map[string]string),
	}

	// Apply configuration options
	for _, opt := range opts {
		opt(&cfg)
	}

	if !cfg.Enabled {
		otel.SetTracerProvider(noop.NewTracerProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
		return &Application{
			cfg:             cfg,
			tracer:          otel.Tracer("noop"),
			meter:           otel.Meter("noop"),
			shutdown:        func(context.Context) error { return nil },
			metricsRegistry: make(map[string]interface{}),
		}, nil
	}

	ctx := context.Background()
	var dial []grpc.DialOption
	if cfg.Insecure {
		dial = append(dial, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	if len(cfg.Headers) != 0 {
		dial = append(dial, grpc.WithPerRPCCredentials(headerCreds(cfg.Headers)))
	}
	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
		otlptracegrpc.WithDialOption(dial...),
	)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithProcess(), resource.WithOS(), resource.WithHost(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String(cfg.AppName),
		),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	return &Application{
		cfg:             cfg,
		tracer:          tp.Tracer("newrelic-otel-shim"),
		meter:           otel.Meter("newrelic-otel-shim"),
		shutdown:        tp.Shutdown,
		metricsRegistry: make(map[string]interface{}),
	}, nil
}

// Shutdown mirrors app.Shutdown(timeout) semantics (New Relic v3 uses time.Duration).
func (a *Application) Shutdown(timeout time.Duration) error {
	if a == nil || a.shutdown == nil {
		return nil // Silently succeed if application is nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return a.shutdown(ctx)
}

// StartTransaction mirrors newrelic.Application.StartTransaction.
func (a *Application) StartTransaction(name string) *Transaction {
	if a == nil || a.tracer == nil {
		// Return a noop transaction that's safe to use
		noopTracer := otel.GetTracerProvider().Tracer("noop")
		ctx, span := noopTracer.Start(context.Background(), name)
		return &Transaction{
			tracer: noopTracer,
			ctx:    ctx,
			span:   span,
		}
	}
	ctx, span := a.tracer.Start(context.Background(), name)
	return &Transaction{tracer: a.tracer, ctx: ctx, span: span}
}

// RecordCustomEvent maps to a span event on the root span.
func (a *Application) RecordCustomEvent(name string, attrs map[string]interface{}) error {
	if a == nil || a.tracer == nil {
		return nil // Silently ignore if application is nil
	}
	// Create a short-lived span to attach the event if no active txn
	_, span := a.tracer.Start(context.Background(), "custom.event")
	defer span.End()
	var kvs []attribute.KeyValue
	for k, v := range attrs {
		kvs = append(kvs, anyToAttr(k, v))
	}
	span.AddEvent(name, trace.WithAttributes(kvs...))
	return nil
}

// ==============================
// Context helpers (match v3 signatures)
// ==============================

type ctxKey struct{}

func NewContext(ctx context.Context, txn *Transaction) context.Context {
	return context.WithValue(ctx, ctxKey{}, txn)
}
func FromContext(ctx context.Context) *Transaction {
	if v := ctx.Value(ctxKey{}); v != nil {
		if t, ok := v.(*Transaction); ok {
			return t
		}
	}
	return nil
}

// ==============================
// OpenTelemetry Context Propagation Helpers
// ==============================

// StartOTelSpan creates a new OpenTelemetry span as a child of the transaction's span.
// This allows seamless integration between NR shim API and native OTel SDK.
// The returned context can be used for further OTel operations within the same trace.
func (t *Transaction) StartOTelSpan(name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if t == nil || t.tracer == nil {
		// Fallback to global tracer if transaction is nil
		return otel.Tracer("fallback").Start(context.Background(), name, opts...)
	}
	return t.tracer.Start(t.ctx, name, opts...)
}

// ContextWithOTelSpan combines a New Relic transaction context with an existing OTel span.
// This is useful when you have an OTel span from outside the shim that you want to
// associate with a NR transaction for proper context propagation.
func ContextWithOTelSpan(txnCtx context.Context, span trace.Span) context.Context {
	return trace.ContextWithSpan(txnCtx, span)
}

// SpanFromOTelContext extracts the active OTel span from a context, if any.
// This is a convenience wrapper around trace.SpanFromContext for consistency.
func SpanFromOTelContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}

// ==============================
// Transaction (struct to match v3)
// ==============================

type Transaction struct {
	tracer  trace.Tracer
	ctx     context.Context
	span    trace.Span
	name    atomic.Value // string
	ignored atomic.Bool
}

func (t *Transaction) End() {
	if t != nil && t.span != nil {
		t.span.End()
	}
}
func (t *Transaction) NoticeError(err error) {
	if t == nil || err == nil || t.span == nil {
		return
	}
	t.span.RecordError(err)
	t.span.SetStatus(codes.Error, err.Error())
}
func (t *Transaction) AddAttribute(key string, val interface{}) {
	if t == nil || t.span == nil {
		return
	}
	t.span.SetAttributes(anyToAttr(key, val))
}

// StartSegment is the preferred API in v3; StartSegmentNow is also provided below.
func (t *Transaction) StartSegment(name string) *Segment {
	if t == nil {
		return nil
	}
	ss := t.StartSegmentNow()
	return &Segment{Name: name, StartTime: ss, txn: t}
}

// StartSegmentNow returns a token used for Segment/Datastore/External segments.
type SegmentStartTime struct{ t time.Time }

func (t *Transaction) StartSegmentNow() SegmentStartTime {
	if t == nil {
		return SegmentStartTime{t: time.Now()} // Return valid time even if transaction is nil
	}
	return SegmentStartTime{t: time.Now()}
}

// NewGoroutine returns a new Transaction reference in v3; here, we just reuse the context.
func (t *Transaction) NewGoroutine() *Transaction {
	if t == nil {
		return nil
	}
	return &Transaction{tracer: t.tracer, ctx: t.ctx, span: t.span}
}

// SetName matches v3 API.
func (t *Transaction) SetName(name string) {
	if t != nil {
		t.name.Store(name)
	}
}

// Ignore marks the transaction as ignored; here we set an attribute to aid filtering.
func (t *Transaction) Ignore() {
	if t == nil {
		return
	}
	t.ignored.Store(true)
	if t.span != nil {
		t.span.SetAttributes(attribute.Bool("nr.ignored", true))
	}
}

// SetWebRequest mirrors the v3 convenience wrapping for non-HTTP frameworks.
func (t *Transaction) SetWebRequest(wr WebRequest) {
	if t == nil || t.span == nil {
		return
	}
	if wr.Method != "" {
		t.span.SetAttributes(semconv.HTTPRequestMethodKey.String(wr.Method))
	}
	if wr.URL != nil {
		t.span.SetAttributes(semconv.URLPath(wr.URL.Path))
	}
}

// SetWebRequestHTTP sets web request attributes from *http.Request.
func (t *Transaction) SetWebRequestHTTP(r *http.Request) {
	if t == nil || t.span == nil || r == nil {
		return
	}
	t.span.SetAttributes(
		semconv.HTTPRequestMethodKey.String(r.Method),
		semconv.URLPath(r.URL.Path),
	)
}

// SetWebResponse wraps an http.ResponseWriter similarly to NR. We return the original writer.
func (t *Transaction) SetWebResponse(w WebResponse) http.ResponseWriter { return w.ResponseWriter }

// SetWebResponseHTTP mirrors v3: returns a possibly-wrapped ResponseWriter.
func (t *Transaction) SetWebResponseHTTP(w http.ResponseWriter) http.ResponseWriter { return w }

func (t *Transaction) Context() context.Context {
	if t == nil {
		return context.Background()
	}
	return NewContext(t.ctx, t)
}

// ==============================
// OpenTelemetry Breakout Methods
// ==============================

// OTelSpan returns the underlying OpenTelemetry span for this transaction.
// This allows mixing New Relic shim API with native OTel SDK calls.
func (t *Transaction) OTelSpan() trace.Span {
	if t == nil || t.span == nil {
		// Return a no-op span for nil transactions
		_, span := otel.GetTracerProvider().Tracer("noop").Start(context.Background(), "noop")
		return span
	}
	return t.span
}

// OTelContext returns the OpenTelemetry context containing the active span.
// Use this when you need to pass context to native OTel SDK operations.
func (t *Transaction) OTelContext() context.Context {
	if t == nil {
		return context.Background()
	}
	return t.ctx
}

// OTelTracer returns the OpenTelemetry tracer used by this transaction.
// This allows creating child spans or other OTel operations within the same trace.
func (t *Transaction) OTelTracer() trace.Tracer {
	if t == nil {
		return nil
	}
	return t.tracer
}

// ==============================
// Segment (generic code block)
// ==============================

type Segment struct {
	StartTime SegmentStartTime
	Name      string
	// internals
	txn  *Transaction
	span trace.Span
}

func (s *Segment) AddAttribute(key string, val interface{}) {
	if s == nil || s.span == nil {
		return
	}
	s.span.SetAttributes(anyToAttr(key, val))
}

func (s *Segment) End() {
	if s == nil || s.txn == nil {
		return
	}
	_, span := s.txn.tracer.Start(s.txn.ctx, s.Name, trace.WithTimestamp(s.StartTime.t))
	s.span = span
	span.End(trace.WithTimestamp(time.Now()))
}

// OTelSpan returns the underlying OpenTelemetry span for this segment (available after End() is called).
func (s *Segment) OTelSpan() trace.Span {
	if s == nil || s.span == nil {
		return nil
	}
	return s.span
}

// OTelContext returns the OpenTelemetry context for this segment.
func (s *Segment) OTelContext() context.Context {
	if s == nil || s.txn == nil {
		return context.Background()
	}
	return s.txn.ctx
}

// Convenience alias to match deprecated free function.
func StartSegment(txn *Transaction, name string) *Segment {
	if txn == nil {
		return nil
	}
	return txn.StartSegment(name)
}

// ==============================
// DatastoreSegment
// ==============================

type DatastoreProduct string

const (
	DatastoreMySQL    DatastoreProduct = "MySQL"
	DatastorePostgres DatastoreProduct = "Postgres"
	DatastoreMongoDB  DatastoreProduct = "MongoDB"
	DatastoreDynamoDB DatastoreProduct = "DynamoDB"
)

type DatastoreSegment struct {
	StartTime          SegmentStartTime
	Product            DatastoreProduct
	Collection         string
	Operation          string
	ParameterizedQuery string
	QueryParameters    map[string]interface{}
	Host               string
	PortPathOrID       string
	DatabaseName       string

	// internals
	txn  *Transaction
	span trace.Span
}

func (ds *DatastoreSegment) End() {
	if ds == nil || ds.txn == nil {
		return
	}
	attrs := []attribute.KeyValue{
		semconv.DBSystemKey.String(strings.ToLower(string(ds.Product))),
	}
	if ds.Collection != "" {
		attrs = append(attrs, semconv.DBSQLTableKey.String(ds.Collection))
	}
	if ds.Operation != "" {
		attrs = append(attrs, semconv.DBSystemKey.String(strings.ToLower(ds.Operation)))
	}
	if ds.ParameterizedQuery != "" {
		attrs = append(attrs, semconv.DBStatementKey.String(ds.ParameterizedQuery))
	}
	if ds.Host != "" {
		attrs = append(attrs, semconv.ServerAddress(ds.Host))
	}
	if ds.PortPathOrID != "" {
		attrs = append(attrs, semconv.ServerPortKey.String(ds.PortPathOrID))
	}
	if ds.DatabaseName != "" {
		attrs = append(attrs, semconv.DBNameKey.String(ds.DatabaseName))
	}
	for k, v := range ds.QueryParameters {
		attrs = append(attrs, anyToAttr("db.param."+k, v))
	}

	_, span := ds.txn.tracer.Start(ds.txn.ctx, fmt.Sprintf("DB %s %s", ds.Product, ds.Operation), trace.WithTimestamp(ds.StartTime.t))
	ds.span = span
	span.SetAttributes(attrs...)
	span.End(trace.WithTimestamp(time.Now()))
}

// OTelSpan returns the underlying OpenTelemetry span for this datastore segment (available after End() is called).
func (ds *DatastoreSegment) OTelSpan() trace.Span {
	if ds == nil {
		return nil
	}
	return ds.span
}

// ==============================
// ExternalSegment (HTTP clients)
// ==============================

type ExternalSegment struct {
	StartTime SegmentStartTime
	URL       string
	Request   *http.Request
	Response  *http.Response

	// internals
	txn  *Transaction
	span trace.Span
}

// StartExternalSegment matches v3 helper.
func StartExternalSegment(txn *Transaction, req *http.Request) *ExternalSegment {
	if txn == nil {
		return nil
	}
	seg := &ExternalSegment{StartTime: txn.StartSegmentNow(), Request: req, txn: txn}
	if req != nil {
		seg.URL = req.URL.String()
	}
	return seg
}

func (es *ExternalSegment) SetStatusCode(code int) {
	if es == nil || es.span == nil {
		return
	}
	es.span.SetAttributes(semconv.HTTPResponseStatusCode(code))
}

func (es *ExternalSegment) End() {
	if es == nil || es.txn == nil {
		return
	}
	name := "external"
	if es.Request != nil {
		name = es.Request.Method + " " + es.Request.URL.Host
	}
	_, span := es.txn.tracer.Start(es.txn.ctx, name, trace.WithTimestamp(es.StartTime.t))
	es.span = span
	if es.URL != "" {
		span.SetAttributes(semconv.URLFull(es.URL))
	}
	if es.Response != nil {
		span.SetAttributes(semconv.HTTPResponseStatusCode(es.Response.StatusCode))
	}
	span.End(trace.WithTimestamp(time.Now()))
}

// OTelSpan returns the underlying OpenTelemetry span for this external segment (available after End() is called).
func (es *ExternalSegment) OTelSpan() trace.Span {
	if es == nil {
		return nil
	}
	return es.span
}

// NewRoundTripper matches v3 helper for auto external segments.
func NewRoundTripper(original http.RoundTripper) http.RoundTripper {
	if original == nil {
		original = http.DefaultTransport
	}
	return otelhttp.NewTransport(original)
}

// ==============================
// HTTP server helpers
// ==============================

func WrapHandle(h http.Handler) http.Handler { return otelhttp.NewHandler(h, "http.server") }

// WrapHandleFunc wraps an http.HandlerFunc with OpenTelemetry instrumentation
// Compatible with both standalone usage and router patterns like Gorilla Mux
// Usage: router.HandleFunc(newrelic.WrapHandleFunc(app, "/logout", handlerFunc)).Methods("GET")
func WrapHandleFunc(app *Application, pattern string, hf http.HandlerFunc) (string, http.HandlerFunc) {
	if app == nil {
		// Return original handler if app is nil
		return pattern, hf
	}

	wrappedFunc := func(w http.ResponseWriter, r *http.Request) {
		// Start a transaction for this request
		txnName := r.Method + " " + pattern
		txn := app.StartTransaction(txnName)
		defer txn.End()

		// Set web request details
		txn.SetWebRequestHTTP(r)

		// Add the transaction to the request context
		ctx := NewContext(r.Context(), txn)
		r = r.WithContext(ctx)

		// Wrap the response writer to capture status codes
		wrappedWriter := &responseWriterWrapper{
			ResponseWriter: w,
			txn:            txn,
		}

		// Call the original handler
		hf(wrappedWriter, r)

		// Set response status if captured
		if wrappedWriter.statusCode > 0 {
			txn.AddAttribute("http.response.status_code", wrappedWriter.statusCode)
		}
	}

	return pattern, wrappedFunc
}

// Legacy WrapHandleFunc for backward compatibility (without app parameter)
func WrapHandleFuncLegacy(hf http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		otelhttp.NewHandler(hf, "http.server").ServeHTTP(w, r)
	}
}

// responseWriterWrapper captures the status code for transaction attribution
type responseWriterWrapper struct {
	http.ResponseWriter
	txn        *Transaction
	statusCode int
}

func (w *responseWriterWrapper) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriterWrapper) Write(data []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}

// WebRequest mirrors newrelic.WebRequest (subset used in examples).
type WebRequest struct {
	Header    http.Header
	URL       *url.URL
	Method    string
	Transport Transport
}

type Transport int // only for type compatibility; no behavior required here.

// WebResponse mirrors the struct wrapper NR returns from SetWebResponse.
type WebResponse struct{ ResponseWriter http.ResponseWriter }

// ==============================
// Messaging segments
// ==============================

// DestinationType constants to mimic NR usage (queue/topic).
const (
	DestinationTypeQueue = "queue"
	DestinationTypeTopic = "topic"
)

// MessageProducerSegment mirrors newrelic.MessageProducerSegment (common fields).
type MessageProducerSegment struct {
	StartTime       SegmentStartTime
	Library         string            // e.g. "kafka", "rabbitmq", "sqs"
	DestinationType string            // "queue" or "topic"
	DestinationName string            // queue/topic name
	Headers         map[string]string // optional, mapped as attributes

	// internals
	txn  *Transaction
	span trace.Span
}

func (m *MessageProducerSegment) End() {
	if m == nil || m.txn == nil {
		return
	}
	name := "message publish"
	if m.DestinationName != "" {
		name = "publish " + m.DestinationName
	}
	_, span := m.txn.tracer.Start(m.txn.ctx, name, trace.WithTimestamp(m.StartTime.t))
	m.span = span
	if m.Library != "" {
		span.SetAttributes(attribute.String("messaging.system", strings.ToLower(m.Library)))
	}
	if m.DestinationName != "" {
		span.SetAttributes(attribute.String("messaging.destination.name", m.DestinationName))
	}
	if m.DestinationType != "" {
		span.SetAttributes(attribute.String("messaging.destination.kind", strings.ToLower(m.DestinationType)))
	}
	span.SetAttributes(attribute.String("messaging.operation", "publish"))
	for k, v := range m.Headers {
		span.SetAttributes(attribute.String("messaging.header."+k, v))
	}
	span.End(trace.WithTimestamp(time.Now()))
}

// OTelSpan returns the underlying OpenTelemetry span for this message producer segment (available after End() is called).
func (m *MessageProducerSegment) OTelSpan() trace.Span {
	if m == nil {
		return nil
	}
	return m.span
}

// MessageConsumerSegment mirrors newrelic.MessageConsumerSegment (common fields).
type MessageConsumerSegment struct {
	StartTime       SegmentStartTime
	Library         string
	DestinationType string
	DestinationName string
	QueueName       string // alias of DestinationName for some clients

	// internals
	txn  *Transaction
	span trace.Span
}

func (m *MessageConsumerSegment) End() {
	if m == nil || m.txn == nil {
		return
	}
	name := "message consume"
	if m.DestinationName != "" {
		name = "consume " + m.DestinationName
	}
	_, span := m.txn.tracer.Start(m.txn.ctx, name, trace.WithTimestamp(m.StartTime.t))
	m.span = span
	if m.Library != "" {
		span.SetAttributes(attribute.String("messaging.system", strings.ToLower(m.Library)))
	}
	if m.DestinationName != "" {
		span.SetAttributes(attribute.String("messaging.destination.name", m.DestinationName))
	}
	if m.DestinationType != "" {
		span.SetAttributes(attribute.String("messaging.destination.kind", strings.ToLower(m.DestinationType)))
	}
	span.SetAttributes(attribute.String("messaging.operation", "receive"))
	span.End(trace.WithTimestamp(time.Now()))
}

// OTelSpan returns the underlying OpenTelemetry span for this message consumer segment (available after End() is called).
func (m *MessageConsumerSegment) OTelSpan() trace.Span {
	if m == nil {
		return nil
	}
	return m.span
}

// Helpers to construct segments with the transaction like NR usage.
func (t *Transaction) MessageProducerSegment() *MessageProducerSegment {
	if t == nil {
		return nil
	}
	return &MessageProducerSegment{StartTime: t.StartSegmentNow(), txn: t}
}
func (t *Transaction) MessageConsumerSegment() *MessageConsumerSegment {
	if t == nil {
		return nil
	}
	return &MessageConsumerSegment{StartTime: t.StartSegmentNow(), txn: t}
}

// ==============================
// Util
// ==============================

func anyToAttr(k string, v interface{}) attribute.KeyValue {
	switch vv := v.(type) {
	case string:
		return attribute.String(k, vv)
	case int:
		return attribute.Int(k, vv)
	case int64:
		return attribute.Int64(k, vv)
	case float64:
		return attribute.Float64(k, vv)
	case bool:
		return attribute.Bool(k, vv)
	default:
		return attribute.String(k, fmt.Sprintf("%v", vv))
	}
}

type headerCreds map[string]string

func (h headerCreds) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return h, nil
}
func (h headerCreds) RequireTransportSecurity() bool { return !true /* allow with insecure too */ }

// ==============================
// Metrics API (Prometheus Backend)
// ==============================

// MetricType represents different types of metrics
type MetricType string

const (
	MetricTypeCounter   MetricType = "counter"
	MetricTypeGauge     MetricType = "gauge"
	MetricTypeHistogram MetricType = "histogram"
	MetricTypeSummary   MetricType = "summary"
)

// Metric represents a New Relic metric that's backed by OpenTelemetry
type Metric struct {
	name       string
	metricType MetricType
	help       string
	labels     []string

	// OpenTelemetry metric instances
	int64Counter     metric.Int64Counter
	float64Counter   metric.Float64Counter
	int64Gauge       metric.Int64Gauge
	float64Gauge     metric.Float64Gauge
	int64Histogram   metric.Int64Histogram
	float64Histogram metric.Float64Histogram
}

// RecordCustomMetric records a custom metric value (New Relic v3 API)
func (app *Application) RecordCustomMetric(name string, value float64) error {
	if app == nil {
		return nil
	}

	sanitizedName := sanitizeMetricName("custom_" + name)

	app.metricsMutex.Lock()
	defer app.metricsMutex.Unlock()

	// Check if metric already exists
	if existing, exists := app.metricsRegistry[sanitizedName]; exists {
		if gauge, ok := existing.(*Metric); ok && gauge.metricType == MetricTypeGauge {
			// Metric exists, just set the value
			gauge.Set(value)
			return nil
		}
	}

	// Metric doesn't exist, create new gauge metric
	otelGauge, err := app.meter.Float64Gauge(
		sanitizedName,
		metric.WithDescription("Custom metric: "+name),
	)
	if err != nil {
		return err
	}

	// Create and store metric wrapper
	m := &Metric{
		name:         name,
		metricType:   MetricTypeGauge,
		help:         "Custom metric: " + name,
		float64Gauge: otelGauge,
	}
	app.metricsRegistry[sanitizedName] = m

	// Now set the value on the newly created metric
	m.Set(value)
	return nil
}

// NewCounterMetric creates a new counter metric
func (app *Application) NewCounterMetric(name, help string, labels ...string) *Metric {
	if app == nil {
		return &Metric{} // Return empty metric for nil app
	}

	sanitizedName := sanitizeMetricName(name)
	registryKey := sanitizedName + "_counter"

	app.metricsMutex.Lock()
	defer app.metricsMutex.Unlock()

	// Check if metric already exists
	if existing, exists := app.metricsRegistry[registryKey]; exists {
		if metric, ok := existing.(*Metric); ok {
			return metric
		}
	}

	// Metric doesn't exist, create new counter metric
	m := &Metric{
		name:       name,
		metricType: MetricTypeCounter,
		help:       help,
		labels:     labels,
	}

	if len(labels) == 0 {
		// Create simple counter
		counter, err := app.meter.Int64Counter(
			sanitizedName,
			metric.WithDescription(help),
		)
		if err == nil {
			m.int64Counter = counter
		}
	}
	// Note: OpenTelemetry doesn't have built-in label vectors like Prometheus
	// Labels are passed at record time via attributes

	// Store in registry
	app.metricsRegistry[registryKey] = m
	return m
}

// NewGaugeMetric creates a new gauge metric
func (app *Application) NewGaugeMetric(name, help string, labels ...string) *Metric {
	if app == nil {
		return &Metric{}
	}

	sanitizedName := sanitizeMetricName(name)
	registryKey := sanitizedName + "_gauge"

	app.metricsMutex.Lock()
	defer app.metricsMutex.Unlock()

	// Check if metric already exists
	if existing, exists := app.metricsRegistry[registryKey]; exists {
		if metric, ok := existing.(*Metric); ok {
			return metric
		}
	}

	// Metric doesn't exist, create new gauge metric
	m := &Metric{
		name:       name,
		metricType: MetricTypeGauge,
		help:       help,
		labels:     labels,
	}

	if len(labels) == 0 {
		// Create simple gauge
		gauge, err := app.meter.Float64Gauge(
			sanitizedName,
			metric.WithDescription(help),
		)
		if err == nil {
			m.float64Gauge = gauge
		}
	}
	// Note: OpenTelemetry doesn't have built-in label vectors like Prometheus
	// Labels are passed at record time via attributes

	// Store in registry
	app.metricsRegistry[registryKey] = m
	return m
}

// NewHistogramMetric creates a new histogram metric
func (app *Application) NewHistogramMetric(name, help string, buckets []float64, labels ...string) *Metric {
	if app == nil {
		return &Metric{}
	}

	sanitizedName := sanitizeMetricName(name)
	registryKey := sanitizedName + "_histogram"

	app.metricsMutex.Lock()
	defer app.metricsMutex.Unlock()

	// Check if metric already exists
	if existing, exists := app.metricsRegistry[registryKey]; exists {
		if metric, ok := existing.(*Metric); ok {
			return metric
		}
	}

	// Metric doesn't exist, create new histogram metric
	m := &Metric{
		name:       name,
		metricType: MetricTypeHistogram,
		help:       help,
		labels:     labels,
	}

	if len(labels) == 0 {
		// Create simple histogram
		histogram, err := app.meter.Float64Histogram(
			sanitizedName,
			metric.WithDescription(help),
		)
		if err == nil {
			m.float64Histogram = histogram
		}
	}
	// Note: OpenTelemetry doesn't use bucket configuration like Prometheus
	// Buckets are handled automatically by the backend

	// Store in registry
	app.metricsRegistry[registryKey] = m
	return m
}

// Increment increments a counter metric
func (m *Metric) Increment() {
	if m == nil || m.metricType != MetricTypeCounter {
		return
	}
	if m.int64Counter != nil {
		m.int64Counter.Add(context.Background(), 1)
	} else if m.float64Counter != nil {
		m.float64Counter.Add(context.Background(), 1.0)
	}
}

// IncrementWithLabels increments a counter metric with labels
func (m *Metric) IncrementWithLabels(labelValues ...string) {
	if m == nil || m.metricType != MetricTypeCounter || len(m.labels) == 0 {
		return
	}

	// Convert labels to attributes
	attrs := make([]attribute.KeyValue, min(len(m.labels), len(labelValues)))
	for i := range attrs {
		attrs[i] = attribute.String(m.labels[i], labelValues[i])
	}

	if m.int64Counter != nil {
		m.int64Counter.Add(context.Background(), 1, metric.WithAttributes(attrs...))
	} else if m.float64Counter != nil {
		m.float64Counter.Add(context.Background(), 1.0, metric.WithAttributes(attrs...))
	}
} // Add adds a value to a counter metric
func (m *Metric) Add(value float64) {
	if m == nil || m.metricType != MetricTypeCounter {
		return
	}
	if m.int64Counter != nil {
		m.int64Counter.Add(context.Background(), int64(value))
	} else if m.float64Counter != nil {
		m.float64Counter.Add(context.Background(), value)
	}
}

// AddWithLabels adds a value to a counter metric with labels
func (m *Metric) AddWithLabels(value float64, labelValues ...string) {
	if m == nil || m.metricType != MetricTypeCounter || len(m.labels) == 0 {
		return
	}

	// Convert labels to attributes
	attrs := make([]attribute.KeyValue, min(len(m.labels), len(labelValues)))
	for i := range attrs {
		attrs[i] = attribute.String(m.labels[i], labelValues[i])
	}

	if m.int64Counter != nil {
		m.int64Counter.Add(context.Background(), int64(value), metric.WithAttributes(attrs...))
	} else if m.float64Counter != nil {
		m.float64Counter.Add(context.Background(), value, metric.WithAttributes(attrs...))
	}
}

// Set sets a gauge metric value
func (m *Metric) Set(value float64) {
	if m == nil || m.metricType != MetricTypeGauge {
		return
	}
	if m.int64Gauge != nil {
		m.int64Gauge.Record(context.Background(), int64(value))
	} else if m.float64Gauge != nil {
		m.float64Gauge.Record(context.Background(), value)
	}
}

// SetWithLabels sets a gauge metric value with labels
func (m *Metric) SetWithLabels(value float64, labelValues ...string) {
	if m == nil || m.metricType != MetricTypeGauge || len(m.labels) == 0 {
		return
	}

	// Convert labels to attributes
	attrs := make([]attribute.KeyValue, min(len(m.labels), len(labelValues)))
	for i := range attrs {
		attrs[i] = attribute.String(m.labels[i], labelValues[i])
	}

	if m.int64Gauge != nil {
		m.int64Gauge.Record(context.Background(), int64(value), metric.WithAttributes(attrs...))
	} else if m.float64Gauge != nil {
		m.float64Gauge.Record(context.Background(), value, metric.WithAttributes(attrs...))
	}
}

// Observe observes a value for histogram metrics
func (m *Metric) Observe(value float64) {
	if m == nil || m.metricType != MetricTypeHistogram {
		return
	}
	if m.int64Histogram != nil {
		m.int64Histogram.Record(context.Background(), int64(value))
	} else if m.float64Histogram != nil {
		m.float64Histogram.Record(context.Background(), value)
	}
}

// ObserveWithLabels observes a value for histogram metrics with labels
func (m *Metric) ObserveWithLabels(value float64, labelValues ...string) {
	if m == nil || m.metricType != MetricTypeHistogram || len(m.labels) == 0 {
		return
	}

	// Convert labels to attributes
	attrs := make([]attribute.KeyValue, min(len(m.labels), len(labelValues)))
	for i := range attrs {
		attrs[i] = attribute.String(m.labels[i], labelValues[i])
	}

	if m.int64Histogram != nil {
		m.int64Histogram.Record(context.Background(), int64(value), metric.WithAttributes(attrs...))
	} else if m.float64Histogram != nil {
		m.float64Histogram.Record(context.Background(), value, metric.WithAttributes(attrs...))
	}
}

// sanitizeMetricName converts a metric name to be Prometheus-compatible
func sanitizeMetricName(name string) string {
	// Replace invalid characters with underscores
	sanitized := strings.ReplaceAll(name, ".", "_")
	sanitized = strings.ReplaceAll(sanitized, "-", "_")
	sanitized = strings.ReplaceAll(sanitized, " ", "_")

	// Ensure it starts with a letter or underscore
	if len(sanitized) > 0 && !((sanitized[0] >= 'a' && sanitized[0] <= 'z') ||
		(sanitized[0] >= 'A' && sanitized[0] <= 'Z') || sanitized[0] == '_') {
		sanitized = "_" + sanitized
	}

	return sanitized
}

// min returns the smaller of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
