package newrelic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// setupTestTracer creates a test tracer for capturing spans
func setupTestTracer() *tracetest.InMemoryExporter {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	otel.SetTracerProvider(tp)
	return exporter
}

func TestConfigOptions(t *testing.T) {
	app, err := NewApplication(
		ConfigAppName("test-app"),
		ConfigLicense("test-license"),
		ConfigEnabled(true),
	)
	if err != nil {
		t.Fatalf("NewApplication failed: %v", err)
	}
	if app == nil {
		t.Fatal("Application should not be nil")
	}

	err = app.Shutdown(5 * time.Second)
	if err != nil {
		t.Errorf("Shutdown failed: %v", err)
	}
}

func TestNewApplication_Disabled(t *testing.T) {
	app, err := NewApplication(
		ConfigAppName("test-app"),
		ConfigEnabled(false),
	)
	if err != nil {
		t.Fatalf("NewApplication failed: %v", err)
	}

	if app == nil {
		t.Fatal("Application should not be nil")
	}

	// When disabled, should use noop tracer
	if app.tracer == nil {
		t.Error("Tracer should not be nil even when disabled")
	}

	// Test shutdown
	err = app.Shutdown(5 * time.Second)
	if err != nil {
		t.Errorf("Shutdown failed: %v", err)
	}
}

func TestApplication_StartTransaction(t *testing.T) {
	exporter := setupTestTracer()
	defer exporter.Reset()

	app, err := NewApplication(
		ConfigAppName("test-app"),
		ConfigEnabled(false), // Use noop to avoid OTLP connection
	)
	if err != nil {
		t.Fatalf("NewApplication failed: %v", err)
	}

	txn := app.StartTransaction("test-transaction")
	if txn == nil {
		t.Fatal("StartTransaction returned nil")
	}

	if txn.tracer == nil {
		t.Error("Transaction tracer should not be nil")
	}

	if txn.span == nil {
		t.Error("Transaction span should not be nil")
	}

	if txn.ctx == nil {
		t.Error("Transaction context should not be nil")
	}

	txn.End()
}

func TestTransaction_BasicMethods(t *testing.T) {
	exporter := setupTestTracer()
	defer exporter.Reset()

	app, _ := NewApplication(ConfigAppName("test-app"), ConfigEnabled(false))
	txn := app.StartTransaction("test-transaction")

	// Test AddAttribute
	txn.AddAttribute("test-key", "test-value")
	txn.AddAttribute("int-key", 123)
	txn.AddAttribute("bool-key", true)

	// Test NoticeError
	testErr := fmt.Errorf("test error")
	txn.NoticeError(testErr)

	// Test SetName
	txn.SetName("updated-transaction")

	// Test Ignore
	txn.Ignore()

	// Test Context
	ctx := txn.Context()
	if ctx == nil {
		t.Error("Transaction context should not be nil")
	}

	// Verify we can get the transaction back from context
	retrievedTxn := FromContext(ctx)
	if retrievedTxn != txn {
		t.Error("Retrieved transaction should match original")
	}

	txn.End()
}

func TestTransaction_WebRequest(t *testing.T) {
	app, _ := NewApplication(ConfigAppName("test-app"), ConfigEnabled(false))
	txn := app.StartTransaction("web-test")

	// Test SetWebRequest
	testURL, _ := url.Parse("https://example.com/test")
	webReq := WebRequest{
		Method: "GET",
		URL:    testURL,
		Header: http.Header{"User-Agent": []string{"test-agent"}},
	}
	txn.SetWebRequest(webReq)

	// Test SetWebRequestHTTP
	req := httptest.NewRequest("POST", "https://example.com/api", nil)
	txn.SetWebRequestHTTP(req)

	// Test SetWebResponse
	recorder := httptest.NewRecorder()
	webResp := WebResponse{ResponseWriter: recorder}
	resultWriter := txn.SetWebResponse(webResp)
	if resultWriter != recorder {
		t.Error("SetWebResponse should return the original ResponseWriter")
	}

	// Test SetWebResponseHTTP
	resultWriter2 := txn.SetWebResponseHTTP(recorder)
	if resultWriter2 != recorder {
		t.Error("SetWebResponseHTTP should return the ResponseWriter")
	}

	txn.End()
}

func TestSegment(t *testing.T) {
	app, _ := NewApplication(ConfigAppName("test-app"), ConfigEnabled(false))
	txn := app.StartTransaction("segment-test")

	// Test StartSegment
	seg := txn.StartSegment("test-segment")
	if seg == nil {
		t.Fatal("StartSegment should not return nil")
	}

	if seg.Name != "test-segment" {
		t.Errorf("Expected segment name 'test-segment', got %s", seg.Name)
	}

	if seg.txn != txn {
		t.Error("Segment should reference the transaction")
	}

	// Test AddAttribute before End
	seg.AddAttribute("test-attr", "test-value")

	// Test End
	seg.End()

	// Test OTelSpan after End
	otelSpan := seg.OTelSpan()
	if otelSpan == nil {
		t.Error("OTelSpan should not be nil after End")
	}

	txn.End()
}

func TestDatastoreSegment(t *testing.T) {
	app, _ := NewApplication(ConfigAppName("test-app"), ConfigEnabled(false))
	txn := app.StartTransaction("datastore-test")

	ds := &DatastoreSegment{
		StartTime:          txn.StartSegmentNow(),
		Product:            DatastoreMySQL,
		Collection:         "users",
		Operation:          "SELECT",
		ParameterizedQuery: "SELECT * FROM users WHERE id = ?",
		QueryParameters:    map[string]interface{}{"id": 123},
		Host:               "localhost",
		PortPathOrID:       "3306",
		DatabaseName:       "testdb",
		txn:                txn,
	}

	ds.End()

	// Test OTelSpan
	otelSpan := ds.OTelSpan()
	if otelSpan == nil {
		t.Error("DatastoreSegment OTelSpan should not be nil after End")
	}

	txn.End()
}

func TestExternalSegment(t *testing.T) {
	app, _ := NewApplication(ConfigAppName("test-app"), ConfigEnabled(false))
	txn := app.StartTransaction("external-test")

	// Test basic external segment
	es := &ExternalSegment{
		StartTime: txn.StartSegmentNow(),
		URL:       "https://api.example.com/users",
		txn:       txn,
	}

	// Test SetStatusCode
	es.SetStatusCode(200)

	es.End()

	// Test OTelSpan
	otelSpan := es.OTelSpan()
	if otelSpan == nil {
		t.Error("ExternalSegment OTelSpan should not be nil after End")
	}

	// Test StartExternalSegment helper
	req2 := httptest.NewRequest("POST", "https://api.example.com/users", nil)
	es3 := StartExternalSegment(txn, req2)
	if es3.URL != req2.URL.String() {
		t.Errorf("Expected URL %s, got %s", req2.URL.String(), es3.URL)
	}
	es3.End()

	txn.End()
}

func TestMessageSegments(t *testing.T) {
	app, _ := NewApplication(ConfigAppName("test-app"), ConfigEnabled(false))
	txn := app.StartTransaction("message-test")
    
	// Test MessageProducerSegment
	producer := &MessageProducerSegment{
		StartTime:       txn.StartSegmentNow(),
		Library:         "kafka",
		DestinationType: DestinationTypeTopic,
		DestinationName: "user-events",
		Headers:         map[string]string{"correlation-id": "12345"},
		txn:             txn,
	}
	producer.End()

	// Test OTelSpan
	producerSpan := producer.OTelSpan()
	if producerSpan == nil {
		t.Error("MessageProducerSegment OTelSpan should not be nil after End")
	}

	txn.End()
}

func TestHTTPWrappers(t *testing.T) {
	app, _ := NewApplication(ConfigAppName("test-app"), ConfigEnabled(false))

	// Test WrapHandleFunc
	handlerFunc := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("created"))
	}

	wrappedHandlerFunc := WrapHandleFunc(handlerFunc)
	if wrappedHandlerFunc == nil {
		t.Fatal("WrapHandleFunc should not return nil")
	}

	req2 := httptest.NewRequest("POST", "/create", nil)
	recorder2 := httptest.NewRecorder()
	wrappedHandlerFunc(recorder2, req2)

	if recorder2.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d", recorder2.Code)
	}

	app.Shutdown(5 * time.Second)
}

func TestBreakoutMethods(t *testing.T) {
	exporter := setupTestTracer()
	defer exporter.Reset()

	app, _ := NewApplication(ConfigAppName("test-app"), ConfigEnabled(false))
	txn := app.StartTransaction("breakout-test")

	// Test Transaction breakout methods
	otelSpan := txn.OTelSpan()
	if otelSpan == nil {
		t.Error("Transaction OTelSpan should not be nil")
	}

	otelContext := txn.OTelContext()
	if otelContext == nil {
		t.Error("Transaction OTelContext should not be nil")
	}

	otelTracer := txn.OTelTracer()
	if otelTracer == nil {
		t.Error("Transaction OTelTracer should not be nil")
	}

	// Test StartOTelSpan
	childCtx, childSpan := txn.StartOTelSpan("child-span")
	if childCtx == nil {
		t.Error("StartOTelSpan should return non-nil context")
	}
	if childSpan == nil {
		t.Error("StartOTelSpan should return non-nil span")
	}

	// Add attributes to child span using native OTel
	childSpan.SetAttributes(
		attribute.String("custom.attribute", "value"),
		attribute.Int("custom.number", 42),
	)
	childSpan.AddEvent("Custom event")
	childSpan.End()

	txn.End()
}

func TestContextHelpers(t *testing.T) {
	app, _ := NewApplication(ConfigAppName("test-app"), ConfigEnabled(false))
	txn := app.StartTransaction("context-test")

	// Test NewContext and FromContext
	ctx := NewContext(context.Background(), txn)
	retrievedTxn := FromContext(ctx)

	if retrievedTxn != txn {
		t.Error("FromContext should return the original transaction")
	}

	// Test with nil transaction
	nilCtx := NewContext(context.Background(), nil)
	nilTxn := FromContext(nilCtx)
	if nilTxn != nil {
		t.Error("FromContext should return nil for context with nil transaction")
	}

	// Test with context without transaction
	emptyTxn := FromContext(context.Background())
	if emptyTxn != nil {
		t.Error("FromContext should return nil for context without transaction")
	}

	txn.End()
}

func TestContextPropagationHelpers(t *testing.T) {
	exporter := setupTestTracer()
	defer exporter.Reset()

	app, _ := NewApplication(ConfigAppName("test-app"), ConfigEnabled(false))
	txn := app.StartTransaction("propagation-test")

	// Test ContextWithOTelSpan
	_, span := otel.Tracer("test").Start(context.Background(), "test-span")
	defer span.End()

	combinedCtx := ContextWithOTelSpan(txn.OTelContext(), span)
	if combinedCtx == nil {
		t.Error("ContextWithOTelSpan should not return nil")
	}

	// Test SpanFromOTelContext
	retrievedSpan := SpanFromOTelContext(combinedCtx)
	if retrievedSpan == nil {
		t.Error("SpanFromOTelContext should return a non-nil span")
	}

	txn.End()
}
   