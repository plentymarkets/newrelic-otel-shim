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
replace github.com/newrelic/go-agent/v3/newrelic => github.com/plentymarkets/newrelic-otel-shim/newrelic v0.0.0
```

Then add this repo as a dependency:

```bash
go get github.com/plentymarkets/newrelic-otel-shim/newrelic
```

---

## Quickstart

```go
import "github.com/newrelic/go-agent/v3/newrelic"

func main() {
    app, _ := newrelic.NewApplication(newrelic.Config{
        AppName:      "my-service",
        Enabled:      true,
        OTLPEndpoint: "otel-collector:4317",
        Insecure:     true, // in-cluster collector without TLS
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

## Migration Strategy

1. Swap the import with this shim using `replace` in `go.mod`.
2. Keep your existing New Relic API usage.
3. Deploy with OTel Collector exporting traces/metrics to your backend.
4. Gradually replace old NR APIs with native OTel patterns.

---
