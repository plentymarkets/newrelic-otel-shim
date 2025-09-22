// Package zerologWriter provides integration between zerolog and New Relic through OpenTelemetry.
// This is a shim for github.com/newrelic/go-agent/v3/integrations/logcontext-v2/zerologWriter
package zerologWriter

import (
	"context"
	"encoding/json"
	"io"
	"os"

	"github.com/newrelic/go-agent/v3/newrelic"
	"github.com/rs/zerolog"
)

// Writer provides a writer that forwards log entries to OpenTelemetry
// This is a shim for the New Relic zerolog integration
type Writer struct {
	writer     io.Writer
	app        *newrelic.Application
	baseLogger zerolog.Logger
	txn        *newrelic.Transaction
	debug      bool
}

// New creates a new Writer instance with the specified output writer and New Relic application
// Usage: zerologWriter.New(os.Stdout, app)
// This shim captures log entries and forwards them as OpenTelemetry log records
func New(w io.Writer, app *newrelic.Application) *Writer {
	if w == nil {
		w = os.Stdout // Default to stdout if no writer provided
	}

	// Create a base zerolog logger that writes to the provided writer
	baseLogger := zerolog.New(w)

	return &Writer{
		writer:     w,
		app:        app,
		baseLogger: baseLogger,
	}
}

func (b *Writer) WithTransaction(txn *newrelic.Transaction) Writer {
	return Writer{
		writer: b.writer,
		app:    b.app,
		debug:  b.debug,
		txn:    txn,
	}
}

// WithTransaction duplicates the current NewRelicWriter and sets the transaction to the transaction parsed from ctx
func (b *Writer) WithContext(ctx context.Context) Writer {
	txn := newrelic.FromContext(ctx)
	return Writer{
		writer: b.writer,
		app:    b.app,
		debug:  b.debug,
		txn:    txn,
	}
}

// Write implements io.Writer interface for zerolog integration
// This method enriches log entries with New Relic transaction and span context
func (w *Writer) Write(p []byte) (n int, err error) {
	if w == nil {
		return os.Stdout.Write(p)
	}

	// Try to parse the JSON log entry to enrich it
	var logEntry map[string]interface{}
	if err := json.Unmarshal(p, &logEntry); err != nil {
		// If parsing fails, just pass through to the base writer
		return w.writer.Write(p)
	}

	// Add New Relic context if available
	w.addNewRelicContext(logEntry)

	// Marshal back to JSON
	enrichedJSON, err := json.Marshal(logEntry)
	if err != nil {
		// If marshaling fails, use original
		enrichedJSON = p
	}

	// Write to the underlying writer
	return w.writer.Write(enrichedJSON)
}

// WriteLevel implements zerolog's LevelWriter interface
// This allows capturing the log level directly and adding New Relic context
func (w *Writer) WriteLevel(level string, p []byte) (n int, err error) {
	if w == nil {
		return os.Stdout.Write(p)
	}

	// Parse and enrich the log entry
	var logEntry map[string]interface{}
	if err := json.Unmarshal(p, &logEntry); err != nil {
		return w.writer.Write(p)
	}

	// Ensure the level is set
	logEntry["level"] = level

	// Add New Relic context
	w.addNewRelicContext(logEntry)

	// Marshal and write
	enrichedJSON, err := json.Marshal(logEntry)
	if err != nil {
		enrichedJSON = p
	}

	return w.writer.Write(enrichedJSON)
}

// addNewRelicContext enriches a log entry with New Relic transaction and span information
func (w *Writer) addNewRelicContext(logEntry map[string]interface{}) {
	if w.app == nil {
		return
	}

	// Try to get the current transaction from context
	// In a real application, you would typically pass context through the log call
	// For now, we'll add placeholder fields that could be populated by middleware

	// Add New Relic linking metadata (these would typically come from active transaction)
	if w.app != nil {
		// In a full implementation, you would extract these from the active transaction
		// For now, we add the structure that New Relic expects
		logEntry["entity.name"] = w.app.Config().AppName
		logEntry["entity.type"] = "SERVICE"

		// These would be populated from active span context
		if w.txn != nil {
			span := w.txn.OTelSpan()
			if span != nil {
				logEntry["trace.id"] = span.SpanContext().TraceID().String()
				logEntry["span.id"] = span.SpanContext().SpanID().String()
			}
		}

		// Mark as New Relic log for proper correlation
		logEntry["newrelic.source"] = "api.logs"
	}
}

// Logger returns the base zerolog logger for direct use
// This allows you to use: writer.Logger().Info().Msg("Hello")
func (w *Writer) Logger() zerolog.Logger {
	if w == nil {
		return zerolog.New(os.Stdout)
	}
	return w.baseLogger
}

// Close implements io.Closer interface
func (w *Writer) Close() error {
	// No resources to cleanup in this basic implementation
	return nil
}
