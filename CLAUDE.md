# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Status

**Phase 5 complete.** All core services are implemented:
- **Phase 1:** `consumer/` and `account/` — CRUD APIs with migrations and table-driven tests.
- **Phase 2:** `compliance/` — Rules engine (5 TCPA/FDCPA rules), PII sanitizer, scorecard evaluator with 30+ parametrized tests.
- **Phase 3:** `contact/`, `workflows/`, `audit/`, `scoring/` — Temporal ContactWorkflow (7 activities), Pub/Sub event flow, append-only audit log, consent revocation propagation, scoring subscriber skeleton.
- **Phase 4:** Full audit pipeline — lifecycle events on consumer (created, consent grant+revoke) and account (created, status_updated); audit subscribes to all 6 topics with idempotency; append-only DB trigger; filtered queries (action, since, until via `POST /audit/search`); scoring fully implemented; payment topic stub.
- **Phase 5:** `payment/` — Payment plan CRUD (propose, accept, record payment, get plan) + lifecycle state machine (proposed→accepted→active→completed/defaulted). Private endpoints for Temporal callbacks (MarkDefaulted, MarkCompleted). PaymentPlanWorkflow with signal-driven acceptance timeout (72h), installment tracking with configurable frequency, 3-day grace periods, and 3-miss default threshold. All transitions publish `payment-updated` events; audit integration works via existing Phase 4 subscriber.

Next: Phase 6 (ADR, coverage reports, diagrams, OpenTelemetry integration). See `docs/TD.md` for technical details and `docs/PRD.md` for business context.

## Tech Stack

- **Language:** Go
- **API Framework:** [Encore.go](https://encore.dev) — type-safe REST APIs, auto-provisioned local PostgreSQL and Pub/Sub
- **Workflow Engine:** Temporal — durable multi-step orchestration with crash recovery
- **Database:** PostgreSQL (provisioned by Encore per service)
- **Async Events:** Encore Pub/Sub

## Environment Variables

The company Go proxy is not set up for public modules. Prefix any `go get` or
`go mod tidy` invocation with these variables (or export them in your shell):

```bash
export GOPROXY="https://proxy.golang.org,direct"
export GOSUMDB="sum.golang.org"
```

## Commands

```bash
# Local development
encore run                        # Start API + PostgreSQL + Pub/Sub locally

# Dependency management (requires public proxy — see Environment Variables above)
GOPROXY="https://proxy.golang.org,direct" GOSUMDB="sum.golang.org" go get <pkg>
GOPROXY="https://proxy.golang.org,direct" GOSUMDB="sum.golang.org" go mod tidy

# Testing — must use encore test so the DB is provisioned
encore test ./...                          # All services
encore test ./compliance/...               # Compliance module (>90% coverage target)
encore test -run TestFoo ./consumer/...    # Single test by name

# Temporal worker (separate process)
go run ./workflows/worker/main.go

# Linting
golangci-lint run
```

## Architecture

The platform is a single Encore app organized as service-per-directory. Each service owns its own PostgreSQL schema (via `migrations/`) and communicates with other services through Pub/Sub topics or direct API calls.

### Services

| Service | Responsibility |
|---------|---------------|
| `consumer/` | Consumer CRUD, consent management, attorney flag |
| `account/` | Account CRUD, status transitions (current → delinquent → charged_off → settled → closed) |
| `compliance/` | Rules engine, PII sanitizer, QA scorecard — the core product |
| `contact/` | Contact workflow orchestration (delegates to Temporal) |
| `payment/` | Payment plan lifecycle (proposed → accepted → active → completed/defaulted) |
| `audit/` | Append-only audit log; subscribes to all Pub/Sub topics |
| `scoring/` | Async QA scoring; subscribes to `interaction-created` |
| `workflows/` | Temporal workflow + activity definitions; `workflows/worker/main.go` is the worker entry point |

### Pub/Sub Event Flow

```
contact-attempted   → audit, scoring
interaction-created → audit, scoring
consent-changed     → audit, contact (cancel pending workflows)
payment-updated     → audit
```

### Compliance Engine (F3) — Core Business Logic

Pre-contact checks run all rules (non-short-circuit) and aggregate results:

1. **Time Window** — Block before 8am or after 9pm in consumer's local timezone (TCPA)
2. **Frequency Cap** — Max 7 contact attempts in a 7-day rolling window (FDCPA Reg F)
3. **Attorney Block** — Block if attorney on file
4. **Consent Check** — Block if consent revoked or do-not-contact flagged
5. **Opt-out Validation** — SMS/email must include opt-out instructions

PII redaction patterns: SSNs (`\d{3}-\d{2}-\d{4}`), credit cards (16-digit), phone numbers.

Scorecard evaluation is keyword-based (no AI/NLP) using configurable JSON rubrics per client.

### Contact Temporal Workflow

Steps in order: pre-contact compliance check → generate message → sanitize PII → simulate delivery → record contact attempt → score interaction → publish event.

### Performance Targets

- Compliance pre-check: < 50ms p99
- Full contact workflow: < 2s p99
- Workflow completion rate: 99.9%
- Zero PII patterns in stored records

## Observability

### Metrics Stack

Use **Prometheus + Grafana** (not Encore's built-in metrics):
- Temporal Server and Go SDK expose `/metrics` natively to Prometheus — Encore metrics would create two separate stores with no unified dashboard.
- Local development: Encore's built-in dashboard is fine for traces/API calls.
- Staging/prod: Prometheus scrape + Grafana dashboards.

| Concern | Tool |
|---|---|
| Metrics | Prometheus (native Temporal support) |
| Dashboards + alerting | Grafana |
| Distributed tracing | OpenTelemetry → Jaeger (dev) / Datadog (prod) |
| Structured logging | `encore.dev/rlog` → stdout → Loki or CloudWatch |

### Logging Standards

Log levels: `rlog.Debug` for read-path lookups, `rlog.Info` for successful state changes, `rlog.Warn` for recoverable conditions, `rlog.Error` for failures returned to callers.

Required fields on every log call: `"service"`, entity `"id"`, and domain key (e.g. `"external_id"`). Encore injects `trace_id` automatically.

**Never log:** `account_number`, raw SSN/CC/phone, unsanitized `message_content`, JWT tokens, API keys, or DB connection strings.

Temporal activity logs must include `"workflow_id"` and `"run_id"` as standard fields.

### Key Metrics

- `compliance_check_duration_ms` — histogram (p99 target < 50ms)
- `compliance_violation_total` — counter, labelled by `rule`
- `contact_attempt_total` — counter, labelled by `channel` and `outcome`
- `contact_workflow_duration_ms` — histogram (p99 target < 2000ms)
- `consent_revocation_total` — counter (spikes = leading compliance indicator)
- `consent_event_publish_error_total` — counter (any value > 0 = critical)
- `account_status_transition_total` — counter, labelled by `from`/`to`
- Temporal SDK reports `temporal_workflow_completed`, `temporal_workflow_execution_latency`, `temporal_activity_execution_latency`, `temporal_schedule_to_start_latency` automatically when a Prometheus reporter is registered on the worker.

### Trace Propagation

- **Encore → Encore**: automatic (OTel context injected into all inter-service HTTP calls).
- **Encore → Pub/Sub → Subscriber**: automatic (trace context in message metadata).
- **Encore API → Temporal Worker**: manual — requires Temporal's OTel interceptor (`go.temporal.io/sdk/contrib/opentelemetry`) registered on both the worker and the Temporal client.

Add `CorrelationID string` to `ContactWorkflowInput` (pass `encore.CurrentRequest().TraceID`). Every activity logs it so logs can be correlated even without a trace collector.

### SLOs and Alerting

| SLI | SLO | Alert condition |
|---|---|---|
| `compliance_check_duration_ms` p99 | < 50ms | p99 > 50ms for 5 min |
| `contact_workflow_duration_ms` p99 | < 2000ms | p99 > 2000ms for 5 min |
| Temporal workflow completion rate | 99.9% | failure rate > 0.1% over 1 hour |
| `consent_event_publish_error_total` | 0 | alert immediately on any increment |
| `temporal_schedule_to_start_latency` p99 | < 500ms | p99 > 500ms (worker starvation) |
| Contact block rate | monitor only | alert if > 80% over 1 hour |

### Health Check

Add `GET /health` to the `contact` service. It must verify DB connectivity and Temporal namespace reachability. Used by load balancers and uptime monitors — distinct from Prometheus scraping.

## Implementation Order

Follow the phases in `TD.md`:
1. **Phase 1:** Consumer + Account CRUD ✅
2. **Phase 2:** Compliance engine (rules, sanitizer, scorecard) with heavy tests ✅
3. **Phase 3:** Contact orchestration (Temporal + Pub/Sub + audit) ✅
4. **Phase 4:** Scoring subscriber + audit pipeline ✅
5. **Phase 5:** Payment plan CRUD + lifecycle + PaymentPlanWorkflow ✅
6. **Phase 6:** ADR, coverage reports, diagrams