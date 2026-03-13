# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Status

**Phase 1 complete.** `consumer/` and `account/` services are implemented with migrations, API handlers, and table-driven tests. Start with `TD.md` for technical implementation details and `PRD.md` for business context.

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

## Implementation Order

Follow the phases in `TD.md`:
1. **Phase 1:** Consumer + Account CRUD
2. **Phase 2:** Compliance engine (rules, sanitizer, scorecard) with heavy tests
3. **Phase 3:** Contact orchestration (Temporal + Pub/Sub + audit)
4. **Phase 4:** Scoring subscriber + payment plan lifecycle
5. **Phase 5:** ADR, coverage reports, diagrams