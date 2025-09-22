package newrelic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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
		ConfigDistributedTracerEnabled(true),
		ConfigAppLogForwardingEnabled(true), // Test the new noop option
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

func TestConfigFromEnv(t *testing.T) {
	// Save original environment
	originalVars := map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT":           os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		"OTEL_EXPORTER_OTLP_INSECURE":           os.Getenv("OTEL_EXPORTER_OTLP_INSECURE"),
		"NEW_RELIC_APP_NAME":                    os.Getenv("NEW_RELIC_APP_NAME"),
		"NEW_RELIC_LICENSE_KEY":                 os.Getenv("NEW_RELIC_LICENSE_KEY"),
		"NEW_RELIC_ENABLED":                     os.Getenv("NEW_RELIC_ENABLED"),
		"NEW_RELIC_DISTRIBUTED_TRACING_ENABLED": os.Getenv("NEW_RELIC_DISTRIBUTED_TRACING_ENABLED"),
		"OTEL_EXPORTER_OTLP_HEADERS":            os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"),
	}

	// Restore environment after test
	defer func() {
		for key, value := range originalVars {
			if value == "" {
				os.Unsetenv(key)
			} else {
				os.Setenv(key, value)
			}
		}
	}()

	// Set test environment variables
	os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://custom-collector:4318")
	os.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "false")
	os.Setenv("NEW_RELIC_APP_NAME", "env-test-app")
	os.Setenv("NEW_RELIC_LICENSE_KEY", "env-license-123")
	os.Setenv("NEW_RELIC_ENABLED", "true")
	os.Setenv("NEW_RELIC_DISTRIBUTED_TRACING_ENABLED", "false")
	os.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "api-key=secret123,team=platform")

	app, err := NewApplication(
		ConfigFromEnvironment(), // This should read from environment
		ConfigEnabled(false),    // Override to disable for test
	)
	if err != nil {
		t.Fatalf("NewApplication with ConfigFromEnvironment failed: %v", err)
	}

	// Verify configuration was read from environment
	if app.cfg.OTLPEndpoint != "https://custom-collector:4318" {
		t.Errorf("Expected OTLP endpoint 'https://custom-collector:4318', got '%s'", app.cfg.OTLPEndpoint)
	}

	if app.cfg.Insecure != false {
		t.Errorf("Expected Insecure to be false, got %v", app.cfg.Insecure)
	}

	if app.cfg.AppName != "env-test-app" {
		t.Errorf("Expected app name 'env-test-app', got '%s'", app.cfg.AppName)
	}

	expectedHeaders := map[string]string{
		"api-key": "secret123",
		"team":    "platform",
	}

	for key, expectedValue := range expectedHeaders {
		if actualValue, exists := app.cfg.Headers[key]; !exists {
			t.Errorf("Expected header '%s' to exist", key)
		} else if actualValue != expectedValue {
			t.Errorf("Expected header '%s' to be '%s', got '%s'", key, expectedValue, actualValue)
		}
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

	// Debug: Test transaction creation directly
	txn := app.StartTransaction("debug-test")
	if txn == nil {
		t.Fatal("App should create valid transactions even when disabled")
	}
	ctx := NewContext(context.Background(), txn)
	retrievedTxn := FromContext(ctx)
	if retrievedTxn == nil {
		t.Fatal("NewContext/FromContext should work correctly")
	}
	txn.End()

	// Test new WrapHandleFunc with app and pattern
	handlerFunc := func(w http.ResponseWriter, r *http.Request) {
		// Debug: Check what's in the context
		t.Logf("Request context: %+v", r.Context())

		// Verify transaction is available in context
		txn := FromContext(r.Context())
		if txn == nil {
			t.Error("Transaction should be available in request context")
		} else {
			// Transaction exists, add some attributes to test it works
			txn.AddAttribute("test.handler", "executed")
			t.Log("Transaction found and attribute added successfully")
		}

		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("created"))
	}

	// Test the new signature that returns (pattern, handlerFunc)
	pattern, wrappedHandlerFunc := WrapHandleFunc(app, "/logout", handlerFunc)

	if pattern != "/logout" {
		t.Errorf("Expected pattern '/logout', got '%s'", pattern)
	}

	if wrappedHandlerFunc == nil {
		t.Fatal("WrapHandleFunc should not return nil handler")
	}

	// Test the wrapped handler
	req := httptest.NewRequest("GET", "/logout", nil)
	recorder := httptest.NewRecorder()
	wrappedHandlerFunc(recorder, req)

	if recorder.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d", recorder.Code)
	}

	if recorder.Body.String() != "created" {
		t.Errorf("Expected body 'created', got '%s'", recorder.Body.String())
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

	app, err := NewApplication(
		ConfigAppName("integration-test"),
		ConfigEnabled(false),
		ConfigDistributedTracerEnabled(true),
	)
	if err != nil {
		t.Fatalf("NewApplication failed: %v", err)
	}
	defer app.Shutdown(5 * time.Second)

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

func TestMetricsAPI(t *testing.T) {
	app, err := NewApplication(ConfigAppName("metrics-test"), ConfigEnabled(false))
	if err != nil {
		t.Fatalf("NewApplication failed: %v", err)
	}
	defer app.Shutdown(5 * time.Second)

	// Test RecordCustomMetric
	err = app.RecordCustomMetric("test_metric", 123.45)
	if err != nil {
		t.Errorf("RecordCustomMetric should not error: %v", err)
	}

	// Test Counter Metrics
	counter := app.NewCounterMetric("test_counter", "Test counter metric")
	if counter == nil {
		t.Fatal("NewCounterMetric should not return nil")
	}

	counter.Increment()
	counter.Add(5.0)

	// Test Counter with Labels
	labeledCounter := app.NewCounterMetric("test_counter_labeled", "Test labeled counter", "method", "status")
	if labeledCounter == nil {
		t.Fatal("NewCounterMetric with labels should not return nil")
	}

	labeledCounter.IncrementWithLabels("GET", "200")
	labeledCounter.AddWithLabels(3.0, "POST", "201")

	// Test Gauge Metrics
	gauge := app.NewGaugeMetric("test_gauge", "Test gauge metric")
	if gauge == nil {
		t.Fatal("NewGaugeMetric should not return nil")
	}

	gauge.Set(42.0)

	// Test Gauge with Labels
	labeledGauge := app.NewGaugeMetric("test_gauge_labeled", "Test labeled gauge", "service")
	if labeledGauge == nil {
		t.Fatal("NewGaugeMetric with labels should not return nil")
	}

	labeledGauge.SetWithLabels(100.0, "api")

	// Test Histogram Metrics
	histogram := app.NewHistogramMetric("test_histogram", "Test histogram metric", []float64{0.1, 1, 5, 10})
	if histogram == nil {
		t.Fatal("NewHistogramMetric should not return nil")
	}

	histogram.Observe(2.5)

	// Test Histogram with Labels
	labeledHistogram := app.NewHistogramMetric("test_histogram_labeled", "Test labeled histogram", []float64{0.1, 1, 5}, "endpoint")
	if labeledHistogram == nil {
		t.Fatal("NewHistogramMetric with labels should not return nil")
	}

	labeledHistogram.ObserveWithLabels(1.2, "/api/users")

}

func TestMetricsNilSafety(t *testing.T) {
	// Test all metrics methods with nil application
	var app *Application

	// Should not panic
	err := app.RecordCustomMetric("test", 1.0)
	if err != nil {
		t.Error("RecordCustomMetric with nil app should not error")
	}

	counter := app.NewCounterMetric("test", "help")
	if counter == nil {
		t.Fatal("NewCounterMetric with nil app should return empty metric")
	}

	gauge := app.NewGaugeMetric("test", "help")
	if gauge == nil {
		t.Fatal("NewGaugeMetric with nil app should return empty metric")
	}

	histogram := app.NewHistogramMetric("test", "help", nil)
	if histogram == nil {
		t.Fatal("NewHistogramMetric with nil app should return empty metric")
	}

	// Test nil metric methods (should not panic)
	var nilMetric *Metric
	nilMetric.Increment()
	nilMetric.IncrementWithLabels("test")
	nilMetric.Add(1.0)
	nilMetric.AddWithLabels(1.0, "test")
	nilMetric.Set(1.0)
	nilMetric.SetWithLabels(1.0, "test")
	nilMetric.Observe(1.0)
	nilMetric.ObserveWithLabels(1.0, "test")
}

func TestSanitizeMetricName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple_name", "simple_name"},
		{"name.with.dots", "name_with_dots"},
		{"name-with-dashes", "name_with_dashes"},
		{"name with spaces", "name_with_spaces"},
		{"123starts_with_number", "_123starts_with_number"},
		{"", ""},
		{"ValidName123", "ValidName123"},
	}

	for _, test := range tests {
		result := sanitizeMetricName(test.input)
		if result != test.expected {
			t.Errorf("sanitizeMetricName(%q) = %q, expected %q", test.input, result, test.expected)
		}
	}
}

func TestMetricsDuplicateRegistration(t *testing.T) {
	app, err := NewApplication(ConfigAppName("duplicate-test"), ConfigEnabled(false))
	if err != nil {
		t.Fatalf("NewApplication failed: %v", err)
	}
	defer app.Shutdown(5 * time.Second)

	// Test duplicate RecordCustomMetric - should not panic
	err1 := app.RecordCustomMetric("duplicate_metric", 123.0)
	err2 := app.RecordCustomMetric("duplicate_metric", 456.0)

	if err1 != nil {
		t.Errorf("First RecordCustomMetric should not error: %v", err1)
	}
	if err2 != nil {
		t.Errorf("Second RecordCustomMetric should not error: %v", err2)
	}

	// Test duplicate counter creation - should return same metric
	counter1 := app.NewCounterMetric("duplicate_counter", "Test counter")
	counter2 := app.NewCounterMetric("duplicate_counter", "Test counter")

	if counter1 == nil {
		t.Fatal("First counter should not be nil")
	}
	if counter2 == nil {
		t.Fatal("Second counter should not be nil")
	}
	// They should be the same object
	if counter1 != counter2 {
		t.Error("Duplicate counter metrics should return same instance")
	}

	// Test duplicate gauge creation
	gauge1 := app.NewGaugeMetric("duplicate_gauge", "Test gauge")
	gauge2 := app.NewGaugeMetric("duplicate_gauge", "Test gauge")

	if gauge1 == nil {
		t.Fatal("First gauge should not be nil")
	}
	if gauge2 == nil {
		t.Fatal("Second gauge should not be nil")
	}
	if gauge1 != gauge2 {
		t.Error("Duplicate gauge metrics should return same instance")
	}

	// Test duplicate histogram creation
	buckets := []float64{0.1, 1.0, 10.0}
	histogram1 := app.NewHistogramMetric("duplicate_histogram", "Test histogram", buckets)
	histogram2 := app.NewHistogramMetric("duplicate_histogram", "Test histogram", buckets)

	if histogram1 == nil {
		t.Fatal("First histogram should not be nil")
	}
	if histogram2 == nil {
		t.Fatal("Second histogram should not be nil")
	}
	if histogram1 != histogram2 {
		t.Error("Duplicate histogram metrics should return same instance")
	}

	// Test operations on duplicate metrics work correctly
	counter1.Increment()
	counter2.Add(5.0) // Should be same underlying metric

	gauge1.Set(100.0)
	gauge2.Set(200.0) // Should override previous value

	histogram1.Observe(0.5)
	histogram2.Observe(1.5) // Should be same underlying metric
}
