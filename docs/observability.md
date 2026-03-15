# Observability

## End-to-End Tracing

### Trace Shape

A single contact initiation produces the following trace:

```
HTTP POST /contact/initiate (Encore auto-instrumented)
  └─ Temporal ContactWorkflow
       ├─ CheckCompliance
       ├─ GenerateMessage
       ├─ SanitizePII
       ├─ SimulateDelivery
       ├─ RecordContactResult
       ├─ ScoreInteraction
       ├─ PublishContactAttempted
       └─ PublishInteractionCreated
```

The OTel interceptor on the Temporal worker (`workflows/worker/main.go`) reconstructs the trace context, creating continuous spans from the HTTP request through all workflow activities.

### Trace Propagation Boundaries

| Boundary | Mechanism |
|----------|-----------|
| Encore → Encore (inter-service) | Automatic — OTel context injected into all HTTP calls |
| Encore → Pub/Sub → Subscriber | Automatic — trace context in message metadata |
| Encore API → Temporal Worker | Manual — OTel interceptor registered on the worker |

## Local Development with Jaeger

Start Jaeger all-in-one for local trace collection:

```bash
docker run -d --name jaeger \
  -p 16686:16686 \
  -p 4317:4317 \
  jaegertracing/all-in-one:latest
```

- **Jaeger UI:** http://localhost:16686
- **OTLP gRPC endpoint:** localhost:4317

### Environment Variables

Set these before starting the Temporal worker:

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
export OTEL_SERVICE_NAME=temporal-worker
```

Encore's built-in tracing works independently and is visible in the Encore development dashboard.

## Correlation ID

Every activity logs `correlation_id` (set to `encore.CurrentRequest().TraceID` at initiation). This allows log correlation even without a trace collector:

```bash
# Find all logs for a specific contact workflow
grep "correlation_id=abc-123" logs.txt
```

The `correlation_id` is also stored in audit entry metadata, enabling cross-reference between the audit log and Temporal workflow history.

## Metrics

See `CLAUDE.md` for the full metrics table. Key instruments:

- `compliance_check_duration_ms` — histogram, p99 target < 50ms
- `contact_workflow_duration_ms` — histogram, p99 target < 2000ms
- `compliance_violation_total` — counter by rule
- `contact_attempt_total` — counter by channel and outcome
- `consent_revocation_total` — counter (spikes = leading compliance indicator)

The Temporal SDK automatically reports `temporal_workflow_completed`, `temporal_workflow_execution_latency`, `temporal_activity_execution_latency`, and `temporal_schedule_to_start_latency` when a Prometheus reporter is registered on the worker.

## Production Stack

| Concern | Tool |
|---------|------|
| Metrics | Prometheus (native Temporal support) |
| Dashboards + alerting | Grafana |
| Distributed tracing | OpenTelemetry → Jaeger (dev) / Datadog (prod) |
| Structured logging | `encore.dev/rlog` → stdout → Loki or CloudWatch |
