package zerologWriter

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/newrelic/go-agent/v3/newrelic"
	"github.com/rs/zerolog"
)

func TestNew(t *testing.T) {
	// Create a mock application (using our shim)
	app, err := newrelic.NewApplication(
		newrelic.ConfigAppName("zerolog-test"),
		newrelic.ConfigEnabled(false), // Disable for testing
	)
	if err != nil {
		t.Fatalf("Failed to create application: %v", err)
	}
	defer app.Shutdown(0)

	// Test New function with os.Stdout and app
	writer := New(os.Stdout, app)
	if writer == nil {
		t.Fatal("New should not return nil")
	}
	if writer.app != app {
		t.Error("Writer should reference the application")
	}
	if writer.writer != os.Stdout {
		t.Error("Writer should reference the provided writer")
	}

	// Test with nil writer (should default to stdout)
	writerWithNilWriter := New(nil, app)
	if writerWithNilWriter == nil {
		t.Fatal("New with nil writer should return a writer")
	}
	if writerWithNilWriter.writer != os.Stdout {
		t.Error("Writer with nil writer should default to os.Stdout")
	}

	// Test with nil app
	nilAppWriter := New(os.Stdout, nil)
	if nilAppWriter == nil {
		t.Fatal("New with nil app should return a writer")
	}
	if nilAppWriter.app != nil {
		t.Error("Writer with nil app should have nil app reference")
	}
}

func TestWriter_Write(t *testing.T) {
	app, _ := newrelic.NewApplication(
		newrelic.ConfigAppName("zerolog-test"),
		newrelic.ConfigEnabled(false),
	)
	defer app.Shutdown(0)

	// Use a buffer to capture output
	var buf bytes.Buffer
	writer := New(&buf, app)

	// Test Write method
	testLog := "{\"entity.name\":\"zerolog-test\",\"entity.type\":\"SERVICE\",\"level\":\"info\",\"message\":\"test log entry\",\"newrelic.source\":\"api.logs\",\"timestamp\":\"2023-01-01T12:00:00Z\"}"
	n, err := writer.Write([]byte(testLog))
	if err != nil {
		t.Errorf("Write should not return error: %v", err)
	}
	if n != len(testLog) {
		t.Errorf("Write should return correct byte count, got %d, expected %d", n, len(testLog))
	}

	// Verify the output was written to the buffer
	if buf.String() != testLog {
		t.Errorf("Expected output %q, got %q", testLog, buf.String())
	}
}

func TestWriter_WriteLevel(t *testing.T) {
	app, _ := newrelic.NewApplication(
		newrelic.ConfigAppName("zerolog-test"),
		newrelic.ConfigEnabled(false),
	)
	defer app.Shutdown(0)

	// Use a buffer to capture output
	var buf bytes.Buffer
	writer := New(&buf, app)

	// Test WriteLevel method
	testLog := "{\"entity.name\":\"zerolog-test\",\"entity.type\":\"SERVICE\",\"level\":\"error\",\"message\":\"error log\",\"newrelic.source\":\"api.logs\"}"
	n, err := writer.WriteLevel("error", []byte(testLog))
	if err != nil {
		t.Errorf("WriteLevel should not return error: %v", err)
	}
	if n <= 0 {
		t.Error("WriteLevel should write bytes")
	}

	// Verify the output was written to the buffer
	if buf.String() != testLog {
		t.Errorf("Expected output %q, got %q", testLog, buf.String())
	}
}

func TestWriter_Close(t *testing.T) {
	app, _ := newrelic.NewApplication(
		newrelic.ConfigAppName("zerolog-test"),
		newrelic.ConfigEnabled(false),
	)
	defer app.Shutdown(0)

	writer := New(os.Stdout, app)

	// Test Close method
	err := writer.Close()
	if err != nil {
		t.Errorf("Close should not return error: %v", err)
	}
}

func TestWriter_Logger(t *testing.T) {
	var buf bytes.Buffer
	writer := New(&buf, nil)

	// Get the base logger
	logger := writer.Logger()

	// Log a message
	logger.Info().Msg("Test message")

	// Verify the message was written
	output := buf.String()
	if !strings.Contains(output, "Test message") {
		t.Error("Output should contain the log message")
	}
	if !strings.Contains(output, "info") {
		t.Error("Output should contain log level")
	}
}

func TestWriter_EnrichLogEntry(t *testing.T) {
	app, _ := newrelic.NewApplication(
		newrelic.ConfigAppName("zerolog-test"),
		newrelic.ConfigEnabled(false),
	)
	defer app.Shutdown(0)

	var buf bytes.Buffer
	writer := New(&buf, app)

	// Write a JSON log entry
	logJSON := `{"level":"info","message":"test message","timestamp":"2023-01-01T12:00:00Z"}`
	n, err := writer.Write([]byte(logJSON))
	if err != nil {
		t.Errorf("Write should not return error: %v", err)
	}
	if n <= 0 {
		t.Error("Write should return positive byte count")
	}

	// Parse the enriched output
	output := buf.String()
	var enriched map[string]interface{}
	if err := json.Unmarshal([]byte(output), &enriched); err != nil {
		t.Fatalf("Output should be valid JSON: %v", err)
	}

	// Verify New Relic fields were added
	if enriched["entity.name"] == nil {
		t.Error("Enriched log should contain entity.name")
	}
	if enriched["entity.type"] == nil {
		t.Error("Enriched log should contain entity.type")
	}
	if enriched["newrelic.source"] == nil {
		t.Error("Enriched log should contain newrelic.source")
	}

	// Verify original fields are preserved
	if enriched["level"] != "info" {
		t.Error("Original level should be preserved")
	}
	if enriched["message"] != "test message" {
		t.Error("Original message should be preserved")
	}
}

func TestUsageExample(t *testing.T) {
	// This test demonstrates the intended usage
	app, _ := newrelic.NewApplication(
		newrelic.ConfigAppName("example-app"),
		newrelic.ConfigEnabled(false),
	)
	defer app.Shutdown(0)

	var buf bytes.Buffer

	// Create the writer as intended
	writer := New(&buf, app)

	// Create a zerolog logger as intended
	logger := zerolog.New(writer).With().Timestamp().Logger()

	// Use the logger
	logger.Info().Str("component", "test").Msg("Application started")

	// Verify it works
	output := buf.String()
	if !strings.Contains(output, "Application started") {
		t.Error("Should log the message")
	}
	if !strings.Contains(output, "component") {
		t.Error("Should preserve custom fields")
	}
	// Should contain timestamp due to .With().Timestamp()
	if !strings.Contains(output, "time") {
		t.Error("Should contain timestamp")
	}
}

func TestWriter_NilSafety(t *testing.T) {
	// Test nil writer doesn't panic
	var nilWriter *Writer

	_, err := nilWriter.Write([]byte("test"))
	if err != nil {
		t.Error("Nil Writer Write should not error")
	}

	_, err = nilWriter.WriteLevel("info", []byte("test"))
	if err != nil {
		t.Error("Nil Writer WriteLevel should not error")
	}

	err = nilWriter.Close()
	if err != nil {
		t.Error("Nil Writer Close should not error")
	}
}
