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
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
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
	OTLPEndpoint string // e.g. "otel-collector:4317"
	Insecure     bool   // plaintext for in-cluster collectors
	Headers      map[string]string // optional gRPC headers (sent per RPC)
}

// Application mirrors newrelic.Application (subset).
type Application struct {
	cfg      Config
	tracer   trace.Tracer
	shutdown func(context.Context) error
}

// NewApplication matches the v3 signature.
func NewApplication(cfg Config) (*Application, error) {
	if !cfg.Enabled {
		otel.SetTracerProvider(noop.NewTracerProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
		return &Application{cfg: cfg, tracer: otel.Tracer("noop"), shutdown: func(context.Context) error { return nil }}, nil
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
	if err != nil { return nil, err }

	res, err := resource.New(ctx,
		resource.WithProcess(), resource.WithOS(), resource.WithHost(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String(cfg.AppName),
		),
	)
	if err != nil { return nil, err }

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	return &Application{cfg: cfg, tracer: tp.Tracer("newrelic-otel-shim"), shutdown: tp.Shutdown}, nil
}

// Shutdown mirrors app.Shutdown(ctx) semantics.
func (a *Application) Shutdown(ctx context.Context) error {
	if a.shutdown != nil { return a.shutdown(ctx) }
	return nil
}

// StartTransaction mirrors newrelic.Application.StartTransaction.
func (a *Application) StartTransaction(name string) *Transaction {
	ctx, span := a.tracer.Start(context.Background(), name)
	return &Transaction{tracer: a.tracer, ctx: ctx, span: span}
}

// RecordCustomEvent maps to a span event on the root span.
func (a *Application) RecordCustomEvent(name string, attrs map[string]interface{}) error {
	// Create a short-lived span to attach the event if no active txn
	_, span := a.tracer.Start(context.Background(), "custom.event")
	defer span.End()
	var kvs []attribute.KeyValue
	for k, v := range attrs { kvs = append(kvs, anyToAttr(k, v)) }
	span.AddEvent(name, trace.WithAttributes(kvs...))
	return nil
}

// ==============================
// Context helpers (match v3 signatures)
// ==============================

type ctxKey struct{}

func NewContext(ctx context.Context, txn *Transaction) context.Context { return context.WithValue(ctx, ctxKey{}, txn) }
func FromContext(ctx context.Context) *Transaction {
	if v := ctx.Value(ctxKey{}); v != nil { if t, ok := v.(*Transaction); ok { return t } }
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
	tracer trace.Tracer
	ctx    context.Context
	span   trace.Span
	name   atomic.Value // string
	ignored atomic.Bool
}

func (t *Transaction) End() { if t != nil && t.span != nil { t.span.End() } }
func (t *Transaction) NoticeError(err error) {
	if t == nil || err == nil || t.span == nil { return }
	t.span.RecordError(err)
	t.span.SetStatus(codes.Error, err.Error())
}
func (t *Transaction) AddAttribute(key string, val interface{}) {
	if t == nil || t.span == nil { return }
	t.span.SetAttributes(anyToAttr(key, val))
}

// StartSegment is the preferred API in v3; StartSegmentNow is also provided below.
func (t *Transaction) StartSegment(name string) *Segment {
	if t == nil { return nil }
	ss := t.StartSegmentNow()
	return &Segment{Name: name, StartTime: ss, txn: t}
}

// StartSegmentNow returns a token used for Segment/Datastore/External segments.
type SegmentStartTime struct{ t time.Time }
func (t *Transaction) StartSegmentNow() SegmentStartTime { return SegmentStartTime{t: time.Now()} }

// NewGoroutine returns a new Transaction reference in v3; here, we just reuse the context.
func (t *Transaction) NewGoroutine() *Transaction { 
	if t == nil { return nil }
	return &Transaction{tracer: t.tracer, ctx: t.ctx, span: t.span} 
}

// SetName matches v3 API.
func (t *Transaction) SetName(name string) { if t != nil { t.name.Store(name); } }

// Ignore marks the transaction as ignored; here we set an attribute to aid filtering.
func (t *Transaction) Ignore() { 
	if t == nil { return }
	t.ignored.Store(true)
	if t.span != nil { t.span.SetAttributes(attribute.Bool("nr.ignored", true)) } 
}

// SetWebRequest mirrors the v3 convenience wrapping for non-HTTP frameworks.
func (t *Transaction) SetWebRequest(wr WebRequest) {
	if t == nil || t.span == nil { return }
	if wr.Method != "" { t.span.SetAttributes(semconv.HTTPRequestMethodKey.String(wr.Method)) }
	if wr.URL != nil { t.span.SetAttributes(semconv.URLPath(wr.URL.Path)) }
}

// SetWebRequestHTTP sets web request attributes from *http.Request.
func (t *Transaction) SetWebRequestHTTP(r *http.Request) {
	if t == nil || t.span == nil || r == nil { return }
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
	if t == nil { return context.Background() }
	return NewContext(t.ctx, t) 
}

// ==============================
// OpenTelemetry Breakout Methods
// ==============================

// OTelSpan returns the underlying OpenTelemetry span for this transaction.
// This allows mixing New Relic shim API with native OTel SDK calls.
func (t *Transaction) OTelSpan() trace.Span {
	if t == nil { return nil }
	return t.span
}

// OTelContext returns the OpenTelemetry context containing the active span.
// Use this when you need to pass context to native OTel SDK operations.
func (t *Transaction) OTelContext() context.Context {
	if t == nil { return context.Background() }
	return t.ctx
}

// OTelTracer returns the OpenTelemetry tracer used by this transaction.
// This allows creating child spans or other OTel operations within the same trace.
func (t *Transaction) OTelTracer() trace.Tracer {
	if t == nil { return nil }
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
	if s == nil || s.span == nil { return }
	s.span.SetAttributes(anyToAttr(key, val))
}

func (s *Segment) End() {
	if s == nil || s.txn == nil { return }
	_, span := s.txn.tracer.Start(s.txn.ctx, s.Name, trace.WithTimestamp(s.StartTime.t))
	s.span = span
	span.End(trace.WithTimestamp(time.Now()))
}

// OTelSpan returns the underlying OpenTelemetry span for this segment (available after End() is called).
func (s *Segment) OTelSpan() trace.Span {
	if s == nil { return nil }
	return s.span
}

// Convenience alias to match deprecated free function.
func StartSegment(txn *Transaction, name string) *Segment { 
	if txn == nil { return nil }
	return txn.StartSegment(name) 
}

// ==============================
// DatastoreSegment
// ==============================

type DatastoreProduct string

const (
	DatastoreMySQL      DatastoreProduct = "MySQL"
	DatastorePostgres   DatastoreProduct = "Postgres"
	DatastoreMongoDB    DatastoreProduct = "MongoDB"
	DatastoreDynamoDB   DatastoreProduct = "DynamoDB"
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
	if ds == nil || ds.txn == nil { return }
	attrs := []attribute.KeyValue{
		semconv.DBSystemKey.String(strings.ToLower(string(ds.Product))),
	}
	if ds.Collection != "" { attrs = append(attrs, semconv.DBSQLTableKey.String(ds.Collection)) }
	if ds.Operation != "" { attrs = append(attrs, semconv.DBSystemKey.String(strings.ToLower(ds.Operation))) }
	if ds.ParameterizedQuery != "" { attrs = append(attrs, semconv.DBStatementKey.String(ds.ParameterizedQuery)) }
	if ds.Host != "" { attrs = append(attrs, semconv.ServerAddress(ds.Host)) }
	if ds.PortPathOrID != "" { attrs = append(attrs, semconv.ServerPortKey.String(ds.PortPathOrID)) }
	if ds.DatabaseName != "" { attrs = append(attrs, semconv.DBNameKey.String(ds.DatabaseName)) }
	for k, v := range ds.QueryParameters { attrs = append(attrs, anyToAttr("db.param."+k, v)) }

	_, span := ds.txn.tracer.Start(ds.txn.ctx, fmt.Sprintf("DB %s %s", ds.Product, ds.Operation), trace.WithTimestamp(ds.StartTime.t))
	ds.span = span
	span.SetAttributes(attrs...)
	span.End(trace.WithTimestamp(time.Now()))
}

// OTelSpan returns the underlying OpenTelemetry span for this datastore segment (available after End() is called).
func (ds *DatastoreSegment) OTelSpan() trace.Span {
	if ds == nil { return nil }
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
	if txn == nil { return nil }
	seg := &ExternalSegment{StartTime: txn.StartSegmentNow(), Request: req, txn: txn}
	if req != nil { seg.URL = req.URL.String() }
	return seg
}

func (es *ExternalSegment) SetStatusCode(code int) {
	if es == nil || es.span == nil { return }
	es.span.SetAttributes(semconv.HTTPResponseStatusCode(code))
}

func (es *ExternalSegment) End() {
	if es == nil || es.txn == nil { return }
	name := "external"
	if es.Request != nil { name = es.Request.Method + " " + es.Request.URL.Host }
	_, span := es.txn.tracer.Start(es.txn.ctx, name, trace.WithTimestamp(es.StartTime.t))
	es.span = span
	if es.URL != "" { span.SetAttributes(semconv.URLFull(es.URL)) }
	if es.Response != nil { span.SetAttributes(semconv.HTTPResponseStatusCode(es.Response.StatusCode)) }
	span.End(trace.WithTimestamp(time.Now()))
}

// OTelSpan returns the underlying OpenTelemetry span for this external segment (available after End() is called).
func (es *ExternalSegment) OTelSpan() trace.Span {
	if es == nil { return nil }
	return es.span
}

// NewRoundTripper matches v3 helper for auto external segments.
func NewRoundTripper(original http.RoundTripper) http.RoundTripper {
	if original == nil { original = http.DefaultTransport }
	return otelhttp.NewTransport(original)
}

// ==============================
// HTTP server helpers
// ==============================

func WrapHandle(h http.Handler) http.Handler { return otelhttp.NewHandler(h, "http.server") }

func WrapHandleFunc(hf http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		otelhttp.NewHandler(hf, "http.server").ServeHTTP(w, r)
	}
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
type WebResponse struct { ResponseWriter http.ResponseWriter }

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
	Library         string // e.g. "kafka", "rabbitmq", "sqs"
	DestinationType string // "queue" or "topic"
	DestinationName string // queue/topic name
	Headers         map[string]string // optional, mapped as attributes

	// internals
	txn  *Transaction
	span trace.Span
}

func (m *MessageProducerSegment) End() {
	if m == nil || m.txn == nil { return }
	name := "message publish"
	if m.DestinationName != "" { name = "publish " + m.DestinationName }
	_, span := m.txn.tracer.Start(m.txn.ctx, name, trace.WithTimestamp(m.StartTime.t))
	m.span = span
	if m.Library != "" { span.SetAttributes(attribute.String("messaging.system", strings.ToLower(m.Library))) }
	if m.DestinationName != "" { span.SetAttributes(attribute.String("messaging.destination.name", m.DestinationName)) }
	if m.DestinationType != "" { span.SetAttributes(attribute.String("messaging.destination.kind", strings.ToLower(m.DestinationType))) }
	span.SetAttributes(attribute.String("messaging.operation", "publish"))
	for k, v := range m.Headers { span.SetAttributes(attribute.String("messaging.header."+k, v)) }
	span.End(trace.WithTimestamp(time.Now()))
}

// OTelSpan returns the underlying OpenTelemetry span for this message producer segment (available after End() is called).
func (m *MessageProducerSegment) OTelSpan() trace.Span {
	if m == nil { return nil }
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
	if m == nil || m.txn == nil { return }
	name := "message consume"
	if m.DestinationName != "" { name = "consume " + m.DestinationName }
	_, span := m.txn.tracer.Start(m.txn.ctx, name, trace.WithTimestamp(m.StartTime.t))
	m.span = span
	if m.Library != "" { span.SetAttributes(attribute.String("messaging.system", strings.ToLower(m.Library))) }
	if m.DestinationName != "" { span.SetAttributes(attribute.String("messaging.destination.name", m.DestinationName)) }
	if m.DestinationType != "" { span.SetAttributes(attribute.String("messaging.destination.kind", strings.ToLower(m.DestinationType))) }
	span.SetAttributes(attribute.String("messaging.operation", "receive"))
	span.End(trace.WithTimestamp(time.Now()))
}

// OTelSpan returns the underlying OpenTelemetry span for this message consumer segment (available after End() is called).
func (m *MessageConsumerSegment) OTelSpan() trace.Span {
	if m == nil { return nil }
	return m.span
}

// Helpers to construct segments with the transaction like NR usage.
func (t *Transaction) MessageProducerSegment() *MessageProducerSegment {
	if t == nil { return nil }
	return &MessageProducerSegment{StartTime: t.StartSegmentNow(), txn: t}
}
func (t *Transaction) MessageConsumerSegment() *MessageConsumerSegment {
	if t == nil { return nil }
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

func (h headerCreds) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) { return h, nil }
func (h headerCreds) RequireTransportSecurity() bool { return !true /* allow with insecure too */ }
