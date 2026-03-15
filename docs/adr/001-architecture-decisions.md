# ADR-001: Architecture Decisions

## 1. Encore over Custom HTTP Framework

### Context
The platform needs a Go API framework that provides type-safe REST APIs, database provisioning, Pub/Sub, and local development tooling. Options include building on top of net/http with chi/gin, or adopting Encore.go which bundles infrastructure management.

### Decision
Use Encore.go as the API framework and local infrastructure manager.

### Consequences
- **Positive:** Type-safe API definitions with compile-time checks, zero-config local PostgreSQL and Pub/Sub, built-in distributed tracing and service catalog, automatic API documentation.
- **Negative:** Vendor lock-in — Encore annotations (`//encore:api`, `sqldb.NewDatabase`) are non-standard. Migrating away requires rewriting all service entry points. The `encore test` requirement means standard `go test` cannot provision databases.

---

## 2. Temporal over Cron / Custom Retry

### Context
Contact workflows involve 7 sequential steps (compliance check → message generation → PII sanitization → delivery simulation → result recording → event publishing). Payment plan lifecycles require long-running timers (72h acceptance window, recurring installment tracking with grace periods). These need crash recovery and durable state.

### Decision
Use Temporal for all multi-step workflow orchestration (ContactWorkflow, PaymentPlanWorkflow).

### Consequences
- **Positive:** Durable execution survives process crashes without manual checkpointing. Built-in retry policies, timeouts, and signal handling. Workflow visibility UI for debugging. The PaymentPlanWorkflow uses signals for acceptance and timers for installment tracking — patterns that would require complex state machines with cron.
- **Negative:** Operational complexity — requires running a separate Temporal Server and worker process. Adds a network hop for every activity. Team must learn Temporal's programming model (deterministic workflow code, activity registration).

---

## 3. HTTP-Only Worker Pattern

### Context
Temporal workers run as standalone Go processes outside the Encore runtime. Encore packages (`encore.dev/storage/sqldb`, `encore.dev/pubsub`) cannot be imported into non-Encore binaries. The worker needs to call Encore service APIs (compliance checks, contact result updates, event publishing).

### Decision
The `workflows/` package has zero Encore imports. All activities interact with Encore services via plain HTTP calls to the Encore API gateway. The worker receives `ENCORE_BASE_URL` as an environment variable.

### Consequences
- **Positive:** Clean separation — the worker is a standard Go binary that can be built and deployed independently. Activities are testable with HTTP mocks. No Encore version coupling in the worker.
- **Negative:** HTTP overhead on every activity call (vs. in-process function calls). Model struct duplication — `workflows/models.go` redefines request/response types that mirror Encore service types. Changes to Encore API signatures require manual updates to the worker's HTTP calls.

---

## 4. Append-Only Audit with DB-Level Trigger

### Context
Regulatory compliance (TCPA/FDCPA) requires an immutable audit trail of all contact attempts, consent changes, and account status transitions. Application-level immutability can be bypassed by direct DB access or bugs.

### Decision
The `audit_log` table is protected by a PostgreSQL trigger (`prevent_audit_mutation`) that raises an exception on any UPDATE or DELETE operation. Immutability is enforced at the database level, not the application level.

### Consequences
- **Positive:** Guarantees immutability regardless of application bugs, admin access, or ORM behavior. Any attempt to modify or delete an audit record fails with a clear error. Satisfies auditor requirements for tamper-evident logging.
- **Negative:** Incorrect entries cannot be corrected — they must be superseded by new corrective entries. Schema migrations on `audit_log` require temporarily dropping and recreating the trigger. Bulk data cleanup (e.g., GDPR deletion requests) needs a carefully managed process.
