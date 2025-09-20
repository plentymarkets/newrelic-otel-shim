package newrelic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

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

func TestConfig(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{
			name: "basic config",
			config: Config{
				AppName:      "test-app",
				License:      "test-license",
				Enabled:      true,
				OTLPEndpoint: "localhost:4317",
				Insecure:     true,
			},
		},
		{
			name: "with headers",
			config: Config{
				AppName:      "test-app-headers",
				Enabled:      true,
				OTLPEndpoint: "localhost:4317",
				Insecure:     true,
				Headers:      map[string]string{"Authorization": "Bearer token"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.config.AppName == "" {
				t.Error("AppName should not be empty")
			}
			if !tt.config.Enabled {
				t.Error("Enabled should be true for test")
			}
		})
	}
}

func TestNewApplication_Disabled(t *testing.T) {
	config := Config{
		AppName: "test-app",
		Enabled: false,
	}

	app, err := NewApplication(config)
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
	err = app.Shutdown(context.Background())
	if err != nil {
		t.Errorf("Shutdown failed: %v", err)
	}
}

func TestNewApplication_InvalidEndpoint(t *testing.T) {
	config := Config{
		AppName:      "test-app",
		Enabled:      true,
		OTLPEndpoint: "invalid-endpoint",
		Insecure:     true,
	}

	// This might fail due to invalid endpoint, but we test the error handling
	_, err := NewApplication(config)
	// We expect this to potentially error due to invalid endpoint
	// The exact behavior depends on the OTLP exporter implementation
	t.Logf("NewApplication with invalid endpoint returned error: %v", err)
}

func TestApplication_StartTransaction(t *testing.T) {
	exporter := setupTestTracer()
	defer exporter.Reset()

	config := Config{
		AppName: "test-app",
		Enabled: false, // Use noop to avoid OTLP connection
	}

	app, err := NewApplication(config)
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

func TestApplication_RecordCustomEvent(t *testing.T) {
	exporter := setupTestTracer()
	defer exporter.Reset()

	config := Config{
		AppName: "test-app",
		Enabled: false,
	}

	app, err := NewApplication(config)
	if err != nil {
		t.Fatalf("NewApplication failed: %v", err)
	}

	attrs := map[string]interface{}{
		"string_attr": "value",
		"int_attr":    42,
		"float_attr":  3.14,
		"bool_attr":   true,
	}

	err = app.RecordCustomEvent("test-event", attrs)
	if err != nil {
		t.Errorf("RecordCustomEvent failed: %v", err)
	}
}

func TestTransaction_BasicMethods(t *testing.T) {
	exporter := setupTestTracer()
	defer exporter.Reset()

	config := Config{AppName: "test-app", Enabled: false}
	app, _ := NewApplication(config)
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
	if !txn.ignored.Load() {
		t.Error("Transaction should be marked as ignored")
	}

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
	config := Config{AppName: "test-app", Enabled: false}
	app, _ := NewApplication(config)
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

func TestTransaction_NewGoroutine(t *testing.T) {
	config := Config{AppName: "test-app", Enabled: false}
	app, _ := NewApplication(config)
	txn := app.StartTransaction("goroutine-test")

	goroutineTxn := txn.NewGoroutine()
	if goroutineTxn == nil {
		t.Fatal("NewGoroutine should not return nil")
	}

	if goroutineTxn.tracer != txn.tracer {
		t.Error("Goroutine transaction should share the same tracer")
	}

	if goroutineTxn.ctx != txn.ctx {
		t.Error("Goroutine transaction should share the same context")
	}

	txn.End()
	goroutineTxn.End()
}

func TestSegment(t *testing.T) {
	config := Config{AppName: "test-app", Enabled: false}
	app, _ := NewApplication(config)
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

func TestSegment_StartSegmentNow(t *testing.T) {
	config := Config{AppName: "test-app", Enabled: false}
	app, _ := NewApplication(config)
	txn := app.StartTransaction("segment-now-test")

	// Test StartSegmentNow
	startTime := txn.StartSegmentNow()
	if startTime.t.IsZero() {
		t.Error("StartSegmentNow should return non-zero time")
	}

	// Test creating segment with start time
	seg := &Segment{
		Name:      "manual-segment",
		StartTime: startTime,
		txn:       txn,
	}
	seg.End()

	// Test free function
	freeSeg := StartSegment(txn, "free-function-segment")
	freeSeg.End()

	txn.End()
}

func TestDatastoreSegment(t *testing.T) {
	config := Config{AppName: "test-app", Enabled: false}
	app, _ := NewApplication(config)
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

	// Test different datastore products
	products := []DatastoreProduct{
		DatastorePostgres,
		DatastoreMongoDB,
		DatastoreDynamoDB,
	}

	for _, product := range products {
		ds2 := &DatastoreSegment{
			StartTime: txn.StartSegmentNow(),
			Product:   product,
			Operation: "INSERT",
			txn:       txn,
		}
		ds2.End()
	}

	txn.End()
}

func TestExternalSegment(t *testing.T) {
	config := Config{AppName: "test-app", Enabled: false}
	app, _ := NewApplication(config)
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

	// Test with request and response
	req := httptest.NewRequest("GET", "https://api.example.com/users", nil)
	resp := &http.Response{StatusCode: 200}

	es2 := &ExternalSegment{
		StartTime: txn.StartSegmentNow(),
		Request:   req,
		Response:  resp,
		txn:       txn,
	}
	es2.End()

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
	config := Config{AppName: "test-app", Enabled: false}
	app, _ := NewApplication(config)
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

	// Test MessageConsumerSegment
	consumer := &MessageConsumerSegment{
		StartTime:       txn.StartSegmentNow(),
		Library:         "rabbitmq",
		DestinationType: DestinationTypeQueue,
		DestinationName: "user-queue",
		QueueName:       "user-queue", // alias test
		txn:             txn,
	}
	consumer.End()

	// Test OTelSpan
	consumerSpan := consumer.OTelSpan()
	if consumerSpan == nil {
		t.Error("MessageConsumerSegment OTelSpan should not be nil after End")
	}

	// Test transaction helper methods
	producer2 := txn.MessageProducerSegment()
	producer2.Library = "sqs"
	producer2.DestinationType = DestinationTypeQueue
	producer2.DestinationName = "orders"
	producer2.End()

	consumer2 := txn.MessageConsumerSegment()
	consumer2.Library = "kafka"
	consumer2.DestinationType = DestinationTypeTopic
	consumer2.DestinationName = "notifications"
	consumer2.End()

	txn.End()
}

func TestContextHelpers(t *testing.T) {
	config := Config{AppName: "test-app", Enabled: false}
	app, _ := NewApplication(config)
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

func TestHTTPWrappers(t *testing.T) {
	config := Config{AppName: "test-app", Enabled: false}
	app, _ := NewApplication(config)

	// Test WrapHandle
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test response"))
	})

	wrappedHandler := WrapHandle(handler)
	if wrappedHandler == nil {
		t.Fatal("WrapHandle should not return nil")
	}

	// Test the wrapped handler
	req := httptest.NewRequest("GET", "/test", nil)
	recorder := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", recorder.Code)
	}

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

	// Test NewRoundTripper
	originalTransport := &http.Transport{}
	wrappedTransport := NewRoundTripper(originalTransport)
	if wrappedTransport == nil {
		t.Fatal("NewRoundTripper should not return nil")
	}

	// Test with nil transport (should use default)
	defaultWrapped := NewRoundTripper(nil)
	if defaultWrapped == nil {
		t.Fatal("NewRoundTripper should not return nil even with nil input")
	}

	app.Shutdown(context.Background())
}

func TestBreakoutMethods(t *testing.T) {
	exporter := setupTestTracer()
	defer exporter.Reset()

	config := Config{AppName: "test-app", Enabled: false}
	app, _ := NewApplication(config)
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

func TestBreakoutMethods_WithNilTransaction(t *testing.T) {
	var txn *Transaction

	// Test nil safety
	otelSpan := txn.OTelSpan()
	if otelSpan != nil {
		t.Error("OTelSpan should return nil for nil transaction")
	}

	otelContext := txn.OTelContext()
	if otelContext == nil {
		t.Error("OTelContext should return background context for nil transaction")
	}

	otelTracer := txn.OTelTracer()
	if otelTracer != nil {
		t.Error("OTelTracer should return nil for nil transaction")
	}

	// StartOTelSpan with nil transaction should use fallback
	ctx, span := txn.StartOTelSpan("fallback-span")
	if ctx == nil {
		t.Error("StartOTelSpan should return non-nil context even with nil transaction")
	}
	if span == nil {
		t.Error("StartOTelSpan should return non-nil span even with nil transaction")
	}
	span.End()
}

func TestContextPropagationHelpers(t *testing.T) {
	exporter := setupTestTracer()
	defer exporter.Reset()

	config := Config{AppName: "test-app", Enabled: false}
	app, _ := NewApplication(config)
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
	// Note: Can't directly compare spans as noop.Span is not comparable
	// Just verify we get a valid span back

	// Test with context without span
	emptySpan := SpanFromOTelContext(context.Background())
	if emptySpan == nil {
		t.Error("SpanFromOTelContext should return a non-nil span (likely noop)")
	}

	txn.End()
}

func TestAnyToAttr(t *testing.T) {
	tests := []struct {
		key      string
		value    interface{}
		expected string // type of expected attribute
	}{
		{"string", "test", "string"},
		{"int", 42, "int"},
		{"int64", int64(123), "int64"},
		{"float64", 3.14, "float64"},
		{"bool", true, "bool"},
		{"other", struct{}{}, "string"}, // should convert to string
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_%s", tt.key, tt.expected), func(t *testing.T) {
			attr := anyToAttr(tt.key, tt.value)
			if string(attr.Key) != tt.key {
				t.Errorf("Expected key %s, got %s", tt.key, attr.Key)
			}

			// The actual attribute validation would require more complex type checking
			// but we verify the function doesn't panic and returns a valid attribute
			if attr.Key == "" {
				t.Error("Attribute key should not be empty")
			}
		})
	}
}

func TestHeaderCreds(t *testing.T) {
	headers := map[string]string{
		"Authorization": "Bearer token",
		"Custom-Header": "value",
	}

	creds := headerCreds(headers)

	// Test GetRequestMetadata
	metadata, err := creds.GetRequestMetadata(context.Background())
	if err != nil {
		t.Errorf("GetRequestMetadata failed: %v", err)
	}

	if len(metadata) != len(headers) {
		t.Errorf("Expected %d metadata entries, got %d", len(headers), len(metadata))
	}

	for k, v := range headers {
		if metadata[k] != v {
			t.Errorf("Expected metadata[%s] = %s, got %s", k, v, metadata[k])
		}
	}

	// Test RequireTransportSecurity (should return false for insecure)
	if creds.RequireTransportSecurity() {
		t.Error("RequireTransportSecurity should return false")
	}
}

func TestNilSafety(t *testing.T) {
	// Test all methods with nil objects to ensure no panics

	// Nil Transaction
	var txn *Transaction
	txn.End()                           // should not panic
	txn.NoticeError(fmt.Errorf("test")) // should not panic
	txn.AddAttribute("key", "value")    // should not panic
	txn.SetName("test")                 // should not panic
	txn.Ignore()                        // should not panic
	txn.SetWebRequest(WebRequest{})     // should not panic
	txn.SetWebRequestHTTP(nil)          // should not panic
	txn.SetWebResponse(WebResponse{})   // should not panic
	txn.SetWebResponseHTTP(nil)         // should not panic

	// Test methods that return values
	if txn.StartSegment("test") != nil {
		t.Error("StartSegment should return nil for nil transaction")
	}
	if txn.NewGoroutine() != nil {
		t.Error("NewGoroutine should return nil for nil transaction")
	}
	if txn.MessageProducerSegment() != nil {
		t.Error("MessageProducerSegment should return nil for nil transaction")
	}
	if txn.MessageConsumerSegment() != nil {
		t.Error("MessageConsumerSegment should return nil for nil transaction")
	}

	// Context should return background context, not nil
	ctx := txn.Context()
	if ctx == nil {
		t.Error("Context should return background context for nil transaction")
	}

	// Nil Segment
	var seg *Segment
	seg.AddAttribute("key", "value") // should not panic
	seg.End()                        // should not panic
	span := seg.OTelSpan()           // should return nil
	if span != nil {
		t.Error("OTelSpan should return nil for nil segment")
	}

	// Nil DatastoreSegment
	var ds *DatastoreSegment
	ds.End() // should not panic
	dsSpan := ds.OTelSpan()
	if dsSpan != nil {
		t.Error("OTelSpan should return nil for nil datastore segment")
	}

	// Nil ExternalSegment
	var es *ExternalSegment
	es.SetStatusCode(200) // should not panic
	es.End()              // should not panic
	esSpan := es.OTelSpan()
	if esSpan != nil {
		t.Error("OTelSpan should return nil for nil external segment")
	}

	// Nil MessageProducerSegment
	var mps *MessageProducerSegment
	mps.End() // should not panic
	mpsSpan := mps.OTelSpan()
	if mpsSpan != nil {
		t.Error("OTelSpan should return nil for nil message producer segment")
	}

	// Nil MessageConsumerSegment
	var mcs *MessageConsumerSegment
	mcs.End() // should not panic
	mcsSpan := mcs.OTelSpan()
	if mcsSpan != nil {
		t.Error("OTelSpan should return nil for nil message consumer segment")
	}

	// Test helper functions with nil transaction
	if StartSegment(nil, "test") != nil {
		t.Error("StartSegment should return nil for nil transaction")
	}

	if StartExternalSegment(nil, nil) != nil {
		t.Error("StartExternalSegment should return nil for nil transaction")
	}
}

// Integration test that combines multiple features
func TestIntegrationExample(t *testing.T) {
	exporter := setupTestTracer()
	defer exporter.Reset()

	config := Config{
		AppName:      "integration-test",
		Enabled:      false, // Use noop to avoid external dependencies
		OTLPEndpoint: "localhost:4317",
		Insecure:     true,
	}

	app, err := NewApplication(config)
	if err != nil {
		t.Fatalf("NewApplication failed: %v", err)
	}
	defer app.Shutdown(context.Background())

	// Start main transaction
	txn := app.StartTransaction("integration-operation")
	txn.AddAttribute("operation.type", "integration-test")

	// Create various segments
	seg := txn.StartSegment("business-logic")
	seg.AddAttribute("complexity", "high")
	seg.End()

	// Database operation
	ds := &DatastoreSegment{
		StartTime:    txn.StartSegmentNow(),
		Product:      DatastorePostgres,
		Collection:   "users",
		Operation:    "SELECT",
		DatabaseName: "production",
		Host:         "db.example.com",
		PortPathOrID: "5432",
		txn:          txn,
	}
	ds.End()

	// External API call
	req := httptest.NewRequest("GET", "https://api.example.com/data", nil)
	es := StartExternalSegment(txn, req)
	es.SetStatusCode(200)
	es.End()

	// Message production
	producer := txn.MessageProducerSegment()
	producer.Library = "kafka"
	producer.DestinationType = DestinationTypeTopic
	producer.DestinationName = "events"
	producer.Headers = map[string]string{"trace-id": "abc123"}
	producer.End()

	// Use breakout methods for custom OTel operations
	childCtx, childSpan := txn.StartOTelSpan("custom-operation")
	childSpan.SetAttributes(
		attribute.String("custom.framework", "native-otel"),
		attribute.Bool("is.important", true),
	)
	childSpan.AddEvent("Custom processing started")
	childSpan.End()

	// Verify context propagation
	if SpanFromOTelContext(childCtx) == nil {
		t.Error("Child context should contain span")
	}

	// Custom event
	app.RecordCustomEvent("integration.completed", map[string]interface{}{
		"duration_ms": 150,
		"success":     true,
	})

	txn.End()
}
