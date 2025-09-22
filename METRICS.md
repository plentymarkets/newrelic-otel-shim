# New Relic Metrics API Shim

This provides a drop-in replacement for New Relic's metrics API that uses Prometheus metrics backend with a `/metrics` endpoint.

## Features

- **Custom Metrics**: `app.RecordCustomMetric(name, value)`
- **Counter Metrics**: Increment-only metrics for counting events
- **Gauge Metrics**: Set arbitrary values (up/down)
- **Histogram Metrics**: Observe values with configurable buckets
- **Labels Support**: All metric types support labels for dimensionality
- **Prometheus Backend**: All metrics are exposed via Prometheus format
- **HTTP Handler**: Built-in `/metrics` endpoint handler

## Usage Examples

### Basic Setup

```go
package main

import (
    "log"
    "net/http"
    "time"

    "github.com/newrelic/go-agent/v3/newrelic"
)

func main() {
    app, err := newrelic.NewApplication(
        newrelic.ConfigAppName("my-metrics-app"),
        newrelic.ConfigFromEnvironment(),
    )
    if err != nil {
        log.Fatal(err)
    }
    defer app.Shutdown(5 * time.Second)

    // Expose metrics endpoint
    http.Handle("/metrics", app.GetMetricsHandler())

    // Start metrics server
    go http.ListenAndServe(":9090", nil)

    // Your application code here...
}
```

### Custom Metrics

```go
// Simple custom metric (becomes a gauge)
app.RecordCustomMetric("user_sessions", 42.0)
app.RecordCustomMetric("cache_hit_ratio", 0.85)
```

### Counter Metrics

```go
// Simple counter
requestsTotal := app.NewCounterMetric("requests_total", "Total HTTP requests")
requestsTotal.Increment()
requestsTotal.Add(5.0)

// Counter with labels
httpRequests := app.NewCounterMetric(
    "http_requests_total",
    "Total HTTP requests by method and status",
    "method", "status",
)
httpRequests.IncrementWithLabels("GET", "200")
httpRequests.IncrementWithLabels("POST", "201")
httpRequests.AddWithLabels(3.0, "PUT", "200")
```

### Gauge Metrics

```go
// Simple gauge
activeConnections := app.NewGaugeMetric("active_connections", "Number of active connections")
activeConnections.Set(15.0)

// Gauge with labels
memoryUsage := app.NewGaugeMetric(
    "memory_usage_bytes",
    "Memory usage by service component",
    "component",
)
memoryUsage.SetWithLabels(1024*1024*100, "cache")
memoryUsage.SetWithLabels(1024*1024*200, "database")
```

### Histogram Metrics

```go
// Request duration histogram
requestDuration := app.NewHistogramMetric(
    "request_duration_seconds",
    "HTTP request duration in seconds",
    []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
)
requestDuration.Observe(0.123)

// Histogram with labels
apiLatency := app.NewHistogramMetric(
    "api_request_duration_seconds",
    "API request duration by endpoint",
    []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0},
    "endpoint", "method",
)
apiLatency.ObserveWithLabels(0.045, "/users", "GET")
apiLatency.ObserveWithLabels(0.125, "/orders", "POST")
```

### HTTP Handler Integration

```go
func setupMetricsEndpoint(app *newrelic.Application) {
    // Create some business metrics
    orders := app.NewCounterMetric("orders_total", "Total orders processed", "status")
    processingTime := app.NewHistogramMetric(
        "order_processing_duration_seconds",
        "Time spent processing orders",
        []float64{0.1, 0.5, 1.0, 2.0, 5.0},
    )

    http.HandleFunc("/order", func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()

        // Process order logic here...
        success := processOrder()

        // Record metrics
        if success {
            orders.IncrementWithLabels("success")
        } else {
            orders.IncrementWithLabels("failed")
        }
        processingTime.Observe(time.Since(start).Seconds())

        w.WriteHeader(http.StatusOK)
    })

    // Expose Prometheus metrics
    http.Handle("/metrics", app.GetMetricsHandler())
}
```

## Prometheus Integration

All metrics are automatically registered with the default Prometheus registry and exposed via the `/metrics` endpoint in standard Prometheus format:

```
# HELP custom_user_sessions Custom metric: user_sessions
# TYPE custom_user_sessions gauge
custom_user_sessions 42

# HELP http_requests_total Total HTTP requests by method and status
# TYPE http_requests_total counter
http_requests_total{method="GET",status="200"} 1
http_requests_total{method="POST",status="201"} 1

# HELP request_duration_seconds HTTP request duration in seconds
# TYPE request_duration_seconds histogram
request_duration_seconds_bucket{le="0.005"} 0
request_duration_seconds_bucket{le="0.01"} 0
request_duration_seconds_bucket{le="0.025"} 0
request_duration_seconds_bucket{le="0.05"} 0
request_duration_seconds_bucket{le="0.1"} 0
request_duration_seconds_bucket{le="0.25"} 1
request_duration_seconds_bucket{le="+Inf"} 1
request_duration_seconds_sum 0.123
request_duration_seconds_count 1
```

## Migration from New Relic

This shim provides a drop-in replacement for New Relic's metrics API. Simply ensure your `go.mod` has:

```go
replace github.com/newrelic/go-agent/v3 => github.com/plentymarkets/newrelic-otel-shim v0.0.0
```

Your existing New Relic metrics code will continue to work, but metrics will be exposed via Prometheus instead of sent to New Relic.

## Metric Name Sanitization

Metric names are automatically sanitized to be Prometheus-compatible:

- Dots (`.`) become underscores (`_`)
- Dashes (`-`) become underscores (`_`)
- Spaces (` `) become underscores (`_`)
- Names starting with numbers get a leading underscore

## Monitoring Integration

You can integrate with standard Prometheus monitoring stacks:

- **Prometheus**: Scrape the `/metrics` endpoint
- **Grafana**: Create dashboards using the metrics
- **AlertManager**: Set up alerts based on metric thresholds
- **Kubernetes**: Use ServiceMonitor for automatic discovery
