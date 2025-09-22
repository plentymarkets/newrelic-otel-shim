# newrelic-otel-shim

A lightweight compatibility shim that mimics the **New Relic Go Agent v3 API** but routes all telemetry through **OpenTelemetry** and exports it to an **OTel Collector** (via OTLP/gRPC).

This allows you to:

- Keep your existing `github.com/newrelic/go-agent/v3/newrelic` imports
- Avoid rewriting all New Relic API usage immediately
- Send traces, segments, and custom events into OTel + your chosen backend

---

## Installation

Add a **replace directive** to your `go.mod`:

```go
replace github.com/newrelic/go-agent/v3 => github.com/plentymarkets/newrelic-otel-shim/v3 v3.0.2
```

Then add this repo as a dependency:

```bash
go get github.com/newrelic/go-agent/v3
```

You also have to modify the creation of the application like this, at best using environment variables for configuration:

```go
app, _ := newrelic.NewApplication(newrelic.Config{
        ConfigAppName("test-app"),
        ConfigFromEnvironment(),
    })
```

---

## Quickstart

```go
import "github.com/newrelic/go-agent/v3/newrelic"

func main() {
    app, _ := newrelic.NewApplication(newrelic.Config{
        ConfigAppName("test-app"),
        ConfigFromEnvironment(),
    })
    defer app.Shutdown(context.Background())

    txn := app.StartTransaction("GET /users/:id")
    defer txn.End()

    seg := txn.StartSegment("load-user")
    defer seg.End()

    // ... business logic ...
}
```

---

## Supported APIs

The shim implements a subset of the **New Relic Go Agent v3 API**, including:

- `Application` / `Config` / `NewApplication`
- `Transaction` with methods:
  - `End()`, `NoticeError(err)`, `AddAttribute(key,val)`
  - `StartSegment(name)`, `StartSegmentNow()`, `NewGoroutine()`
  - `SetName()`, `Ignore()`
  - `SetWebRequest`, `SetWebRequestHTTP`, `SetWebResponse`, `SetWebResponseHTTP`
- `Segment` for generic timed code blocks
- `DatastoreSegment` (DB operations)
- `ExternalSegment` (HTTP client calls)
- `MessageProducerSegment` / `MessageConsumerSegment` (messaging systems)
- Context helpers: `NewContext`, `FromContext`
- HTTP helpers: `WrapHandle`, `WrapHandleFunc`, `NewRoundTripper`
- `Application.RecordCustomEvent`

---

## Notes

- `License` and distributed tracing flags are no-ops; OTel configuration drives behavior.
- `Ignore()` sets an `nr.ignored=true` attribute so you can filter these spans downstream.
- Only common APIs are included — extend as needed for your application.

---

## OpenTelemetry Breakout Methods

For gradual migration or advanced use cases, the shim provides "breakout" methods that expose the underlying OpenTelemetry objects, allowing you to mix New Relic shim API with native OTel SDK calls:

### Accessing Underlying OTel Objects

```go
import (
    "github.com/newrelic/go-agent/v3/newrelic"
    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/trace"
)

func mixedExample() {
    app, _ := newrelic.NewApplication(newrelic.Config{
        ConfigAppName("test-app"),
        ConfigFromEnvironment(),
    })

    // Start with New Relic API
    txn := app.StartTransaction("mixed-operation")
    defer txn.End()

    // Break out to native OTel for advanced operations
    otelCtx, otelSpan := txn.StartOTelSpan("custom-child-span")
    defer otelSpan.End()

    // Use native OTel APIs
    otelSpan.SetAttributes(
        attribute.String("custom.attribute", "value"),
        attribute.Int("custom.count", 42),
    )
    otelSpan.AddEvent("Custom event with native OTel")

    // Continue with New Relic API in the same trace
    seg := txn.StartSegment("nr-segment")
    defer seg.End()

    // Access the underlying span from a segment after it ends
    if span := seg.OTelSpan(); span != nil {
        // Span is available after seg.End() is called
        span.SetAttributes(attribute.String("post.processing", "done"))
    }
}
```

### Context Propagation Between APIs

```go
func contextPropagationExample() {
    txn := app.StartTransaction("parent-operation")
    defer txn.End()

    // Get OTel context for passing to functions expecting OTel context
    otelCtx := txn.OTelContext()

    // Call a function that uses native OTel SDK
    performNativeOTelWork(otelCtx)

    // The native OTel spans will be properly correlated with the NR transaction
}

func performNativeOTelWork(ctx context.Context) {
    tracer := otel.Tracer("native-otel")
    _, span := tracer.Start(ctx, "native-operation")
    defer span.End()

    // This span will be a child of the NR transaction span
    span.SetAttributes(attribute.String("framework", "native-otel"))
}
```

### Available Breakout Methods

- **Transaction.OTelSpan()** - Returns the underlying `trace.Span`
- **Transaction.OTelContext()** - Returns the OTel context containing the active span
- **Transaction.OTelTracer()** - Returns the `trace.Tracer` instance
- **Transaction.StartOTelSpan(name, opts...)** - Creates a child span using native OTel API
- **Segment.OTelSpan()** - Returns the segment's underlying span (after `End()`)
- **DatastoreSegment.OTelSpan()** - Returns the DB segment's underlying span (after `End()`)
- **ExternalSegment.OTelSpan()** - Returns the HTTP segment's underlying span (after `End()`)
- **MessageProducerSegment.OTelSpan()** - Returns the messaging segment's underlying span (after `End()`)
- **MessageConsumerSegment.OTelSpan()** - Returns the messaging segment's underlying span (after `End()`)

---

## Migration Strategy

1. Swap the import with this shim using `replace` in `go.mod`.
2. Keep your existing New Relic API usage.
3. Deploy with OTel Collector exporting traces/metrics to your backend.
4. Gradually replace old NR APIs with native OTel patterns.

---

## Testing

The shim includes comprehensive unit tests covering all functionality:

```bash
go test -v
```

Test coverage includes:

- **Application configuration and lifecycle**
- **Transaction creation, attributes, error handling**
- **All segment types (generic, datastore, external, messaging)**
- **Context propagation and HTTP wrappers**
- **OpenTelemetry breakout methods**
- **Nil safety and error conditions**
- **Integration scenarios mixing NR and OTel APIs**

Run tests with coverage:

```bash
go test -v -cover
```

---

## Development

### Local Development

```bash
# Install development tools
make tools

# Run tests
make test

# Run linting
make lint
```
