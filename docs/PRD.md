# Product Requirements Document: Krew Backend Platform

**Product**: Krew Backend Platform (Codename: "The Rails")
**Last Updated**: March 2026
**Status**: Draft
**Tech Spec**: See `TD.md`

---

## 1. Problem Statement

Credit servicers and debt collectors in the U.S. operate under a dense regulatory framework — FDCPA, TCPA, TILA, Regulation F, GLBA — that governs when, how, and how often they can contact consumers. Violations carry penalties of $500–$1,500 per incident, and class action exposure can reach $500,000 or 1% of a company's net worth. At the same time, these organizations manage tens of thousands of delinquent accounts and need to contact consumers across SMS, email, and voice at scale.

Krew's AI agents (Sarah, Daniel, Mark) handle the consumer-facing interaction — generating empathetic, compliant messages and negotiating payment plans. But those agents cannot operate in isolation. They need a backend platform that:

- Decides **whether** a consumer can be contacted right now (compliance pre-check)
- Orchestrates the **multi-step contact flow** reliably (check → generate → sanitize → deliver → record → score)
- Ensures **every interaction is auditable** with immutable logs for regulatory review
- **Sanitizes PII** from all logs and stored transcripts
- Scores **100% of interactions** against configurable quality rubrics
- Manages **payment plan lifecycle** from proposal through completion or default
- Propagates **consent changes in real time** across all pending and future outbound contacts

This platform is the foundation that every AI agent runs on top of. Without it, the agents are legally exposed and operationally blind.

---

## 2. Users & Personas

### Primary: Krew's AI Agents (Sarah, Daniel, Mark)

These are the internal consumers of the platform APIs. Daniel, for example, needs to:

- Receive consumer context (account status, balance, contact history) before generating a message
- Know whether a contact attempt is legally permissible before sending anything
- Have the message logged, PII-scrubbed, scored, and audited after delivery

The agents don't make compliance decisions — the platform does. The agents call `POST /contact/initiate` and the platform handles everything else.

### Secondary: Operations Team

The ops team at Krew's client institutions (banks, servicers, collection agencies) needs to:

- View contact history for any consumer, including blocked attempts and the reason for blocking
- Query the audit trail for regulatory review and examiner requests
- Monitor contact rates, block rates, and cure rates (percentage of delinquent accounts that return to current status)
- Review QA scorecards for individual interactions

### Tertiary: Compliance Officers

Compliance officers at client institutions need:

- Proof that every outbound contact respected TCPA time windows and FDCPA frequency caps
- Evidence that PII was sanitized from all stored interaction records
- An immutable audit log that cannot be modified after the fact
- Scorecard results showing that agents delivered required disclosures (Mini-Miranda, identification, opt-out instructions)

---

## 3. Goals & Success Metrics

### P0 — Must Have

| Goal | Success Metric | Rationale |
|------|---------------|-----------|
| Zero compliance violations in outbound contacts | 100% of contact attempts run through pre-check before delivery | A single missed pre-check could result in a TCPA lawsuit |
| Full audit trail for all state changes | Every create, update, consent change, and contact attempt produces an append-only audit entry within 1 second | Regulatory examiners expect real-time audit completeness |
| PII never appears in stored logs or interaction records | 100% of interaction content passes through PII sanitizer before storage; zero PII patterns found in audit log or contact_attempts tables | GLBA and internal security policy require PII scrubbing |
| Contact workflow completes reliably even under failures | Temporal workflow completes or retries to completion for 99.9% of initiated contacts; no silently dropped contacts | A dropped contact means a consumer who should have been reached wasn't, affecting cure rates |

### P1 — Should Have

| Goal | Success Metric | Rationale |
|------|---------------|-----------|
| 100% interaction scoring | Every completed interaction has a scorecard result within 30 seconds of delivery | QA scoring is a key differentiator for Krew's "Mark" agent |
| Real-time consent propagation | When consent is revoked, all pending outbound contacts for that consumer are cancelled within 5 seconds | Delayed consent propagation is a compliance risk |
| Payment plan lifecycle management | Payment plans transition correctly through proposed → accepted → active → completed/defaulted states | Payment resolution is the core business outcome |

### P2 — Nice to Have

| Goal | Success Metric | Rationale |
|------|---------------|-----------|
| Compliance check latency < 50ms p99 | Measured at the API layer | Keeps the overall contact initiation flow fast |
| Configurable scorecard rubrics per client | Ops team can upload JSON rubrics without code changes | Different clients have different QA requirements |
| Contact attempt analytics (block rate, channel distribution, cure rate) | Queryable via API | Operational intelligence for account managers |

---

## 4. Scope

### In Scope

- Consumer and account data management (CRUD)
- Compliance engine: pre-contact checks (TCPA time windows, FDCPA frequency caps, consent, attorney block), PII sanitization, scorecard evaluation
- Contact orchestration via Temporal workflows (pre-check → stub message generation → sanitize → simulate delivery → record → score → publish event)
- Payment plan lifecycle (propose, accept, record payments, detect default/completion)
- Event-driven architecture via Pub/Sub (contact events, consent changes, payment updates)
- Append-only audit logging for all state changes
- QA scoring as an async subscriber to interaction events

### Out of Scope

- The AI agent itself (Daniel's LLM, prompt engineering, voice synthesis). The platform exposes a stub where the agent plugs in.
- Actual SMS/email/voice delivery infrastructure (Twilio, SendGrid, LiveKit). Delivery is simulated via a stub activity.
- Client-facing dashboard UI. The platform exposes APIs; a frontend is a separate workstream.
- Multi-tenancy. This version is single-tenant. Multi-tenant isolation (per-client data partitioning, rubric configuration) is a follow-up.
- Encryption at rest for account numbers. Noted as a requirement but not implemented in v1 — the schema includes the column, and the ADR documents the approach.

---

## 5. Feature Requirements

### F1: Consumer Management

**What**: CRUD operations for consumer records, including consent management.

**User story**: As Daniel (AI agent), I need to retrieve a consumer's profile — including their timezone, consent status, and attorney flag — before initiating contact, so the platform can make a compliance decision.

**Acceptance criteria**:
- Create a consumer with required fields (external_id, name, timezone) and optional fields (phone, email)
- Retrieve a consumer by internal ID
- Update consent status (grant/revoke). On revocation:
  - A `consent-changed` event is published
  - An audit log entry is created
  - All pending outbound contacts for this consumer are cancelled by the contact service (subscriber)

### F2: Account Management

**What**: CRUD operations for delinquent accounts linked to consumers.

**User story**: As Daniel, I need to know a consumer's outstanding balance, days past due, and account status to tailor my negotiation approach.

**Acceptance criteria**:
- Create an account linked to a consumer
- List all accounts for a consumer
- Update account status (current → delinquent → charged_off → settled → closed)
- Status changes produce audit log entries

### F3: Compliance Engine (KOMPLY-lite)

**What**: A rules engine that determines whether a contact attempt is legally permissible, sanitizes PII from interaction records, and scores interactions against quality rubrics.

**User stories**:

- As the platform, I need to block a contact attempt to a consumer in Hawaii at 11pm local time, even though it's 4am ET on the server, because TCPA requires timezone-aware enforcement.
- As a compliance officer, I need proof that interaction #4521 was checked against all five compliance rules before delivery, and that the message stored in the database contains no PII.
- As the ops team, I need to see that Daniel delivered the Mini-Miranda disclosure in interaction #4521, per the client's QA rubric.

**Acceptance criteria**:

Pre-contact check:
- Time window rule: blocks contact before 8am or after 9pm in the consumer's local timezone
- Frequency cap rule: blocks contact if 7+ attempts were made to this consumer in the trailing 7 days
- Attorney block rule: blocks contact if the consumer has an attorney on file
- Consent check rule: blocks contact if consent is revoked or do-not-contact is flagged
- Opt-out validation rule: SMS/email payloads must contain opt-out instructions
- Returns a structured result with `allowed: bool` and an array of violations with rule names and details
- All five rules are evaluated on every check (not short-circuit) so the full violation set is visible

PII sanitizer:
- Redacts SSN patterns (XXX-XX-XXXX) to `[SSN_REDACTED]`
- Redacts credit card patterns (16-digit with optional separators) to `[CC_REDACTED]`
- Redacts phone number patterns (various US formats) to `[PHONE_REDACTED]`
- Returns the sanitized string; original is never stored

Scorecard evaluator:
- Accepts interaction text and a rubric (JSON-configured list of items with keywords, weights, required flags)
- Returns a score breakdown: each item's pass/fail status, the weighted total score, and whether all required items passed
- No AI/NLP — pure keyword matching against the rubric

### F4: Contact Orchestration

**What**: The main workflow that coordinates a contact attempt from initiation through delivery, recording, and scoring.

**User story**: As Daniel, I call `POST /contact/initiate` with a consumer ID, account ID, and channel. The platform handles the rest — compliance check, message stub, PII sanitization, simulated delivery, database recording, audit logging, QA scoring — and returns the result.

**Acceptance criteria**:
- Initiating a contact starts a Temporal workflow (ContactWorkflow)
- Step 1: Pre-contact compliance check. If blocked, the workflow records a blocked attempt (with violations) and exits early.
- Step 2: Generate message (stub — returns a hardcoded template with consumer name and balance)
- Step 3: Sanitize PII from the message for storage
- Step 4: Simulate delivery (stub — returns success/failure)
- Step 5: Record the contact attempt in the database with compliance result, sanitized message, and delivery status
- Step 6: Score the interaction against the configured rubric
- Step 7: Publish `interaction-created` event
- If any step fails, Temporal retries with backoff. If the worker crashes, Temporal resumes from the last completed step.
- Contact history is queryable per consumer via `GET /consumers/:id/contacts`

### F5: Payment Plan Lifecycle

**What**: Create, accept, and track payment plans for delinquent accounts.

**User story**: As Daniel, when a consumer agrees to a payment arrangement, I call `POST /payment-plans` to propose a plan. When the consumer confirms, I call `PATCH /payment-plans/:id/accept`. The platform tracks installments and detects defaults.

**Acceptance criteria**:
- Propose a plan with total amount, number of installments, and frequency
- Accept a plan (transitions from proposed → accepted → active)
- Record individual payments against a plan
- Detect completion (all installments paid) and default (3+ missed payments) — this is the stretch Temporal workflow
- All state transitions produce audit entries and publish `payment-updated` events

### F6: Audit Trail

**What**: An immutable, append-only record of every state change across all services.

**User story**: As a compliance officer, I need to pull the complete history of account #7892 — every contact attempt, every consent change, every payment event — and present it to an examiner as evidence of compliant servicing.

**Acceptance criteria**:
- Every state change across all services (consumer, account, contact, payment) produces an audit entry
- Audit entries include: entity type, entity ID, action, actor (system/api/workflow ID), old value, new value, timestamp
- The audit table supports no UPDATE or DELETE operations at the application layer
- Audit entries include metadata (correlation_id, workflow_id) for tracing
- Queryable by entity type + entity ID via API

### F7: Interaction Scoring

**What**: Asynchronous quality scoring of every completed interaction.

**User story**: As Mark (QA agent), I subscribe to completed interactions and score them against the configured rubric, surfacing whether required disclosures were delivered and computing an overall quality score.

**Acceptance criteria**:
- Subscribes to `interaction-created` Pub/Sub events
- Runs the scorecard evaluator from the compliance service
- Writes the scorecard result back to the contact attempt record
- Scoring does not block the contact workflow — it runs asynchronously after delivery

---

## 6. Non-Functional Requirements

| Category | Requirement | Target |
|----------|-------------|--------|
| Latency | Compliance pre-check | < 50ms p99 |
| Latency | Full contact workflow (initiate to recorded) | < 2 seconds p99 |
| Reliability | Contact workflow completion rate | 99.9% (Temporal handles retries) |
| Durability | Audit log entries | Append-only, no deletes, retained indefinitely |
| Privacy | PII in stored records | Zero PII patterns in contact_attempts.message_content or audit_log |
| Observability | Request tracing | Distributed traces across all service calls (provided by Encore) |
| Observability | Workflow visibility | All Temporal workflows visible in Temporal UI with step-level detail |
| Testability | Compliance engine test coverage | > 90% line coverage |
| Recoverability | Worker crash during contact workflow | Temporal resumes from last completed activity; no manual intervention |
| Consistency | Consent revocation propagation | < 5 seconds from revocation to all pending contacts cancelled |

---

## 7. Dependencies & Assumptions

**Dependencies**:
- Temporal Server (self-hosted or Temporal Cloud) for workflow execution
- PostgreSQL for persistent storage (provisioned by Encore locally)
- Encore CLI for local development and infrastructure management

**Assumptions**:
- Single-tenant deployment. Multi-tenant isolation is a future workstream.
- Consumer timezone data is provided by the client at onboarding and assumed correct.
- The AI agent (Daniel) is a black box that receives context and returns a message string. The platform does not evaluate message quality beyond keyword-based scorecard matching.
- Actual message delivery (SMS, email, voice) is simulated. Integration with delivery providers (Twilio, SendGrid, LiveKit) is a separate integration effort.
- The FDCPA 7-in-7 frequency cap applies per consumer, not per account. A consumer with 3 delinquent accounts still has a single 7-contact limit across all accounts.

---

## 8. Risks & Mitigations

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| Compliance rule logic has a bug (e.g., timezone edge case) | A contact is sent in violation of TCPA; potential lawsuit | Medium | Heavy parametrized test coverage on every rule, including DST transitions and edge cases (exactly 8am, exactly 9pm) |
| PII sanitizer misses a pattern | PII stored in logs; GLBA violation | Medium | Regex patterns validated against a curated test corpus; periodic audit of stored records for PII leakage |
| Temporal worker crashes mid-workflow | Contact stuck in pending state | Low | Temporal automatically retries from the last completed activity; set appropriate timeouts so stuck workflows are visible |
| Consent revocation event is delayed or dropped | Consumer contacted after revoking consent; TCPA violation | Low | Encore Pub/Sub provides at-least-once delivery; idempotent handler ensures duplicate events are safe |
| Scorecard rubric keywords produce false positives/negatives | QA scores are inaccurate; ops team loses trust in scoring | Medium | Rubrics are configurable per client; scoring is advisory (not blocking); ops team can review and adjust rubrics |

---

## 9. Release Phases

### Phase 1: Foundation (MVP)
Consumer + Account CRUD, compliance engine with all five rules, PII sanitizer, basic tests. **This alone is demonstrable in an interview.**

### Phase 2: Contact Orchestration
Temporal ContactWorkflow, contact attempt recording, Pub/Sub events, audit logging. **This shows event-driven architecture and durable execution.**

### Phase 3: Scoring & Payment
Async interaction scoring, payment plan lifecycle, PaymentPlanWorkflow (stretch). **This shows async patterns and long-running workflow design.**

### Phase 4: Polish
ADR, test coverage reports, README, architecture diagrams. **This shows engineering maturity.**

---

## 10. Open Questions

1. **Frequency cap scope**: The current spec applies the 7-in-7 cap per consumer. Should it be per consumer per channel (7 calls + 7 texts + 7 emails allowed) or 7 total across all channels? Regulation F says 7 calls per 7 days — other channels may have different limits. This needs confirmation with compliance counsel.

2. **Scorecard rubric versioning**: When a rubric is updated, should existing scored interactions be re-scored against the new rubric, or is the score locked at the time of evaluation? The current spec locks the score at evaluation time, but ops teams may want historical re-scoring.

3. **Consent granularity**: Is consent all-or-nothing, or can a consumer revoke consent for a specific channel (e.g., no voice calls, but SMS is OK)? The current schema has a single `consent_status` field. Per-channel consent would require a separate table.

4. **DST handling**: When clocks change, a consumer in Eastern time has a 1-hour ambiguity window. Should the platform use the stricter interpretation (8am–9pm in *both* the old and new offsets) during the transition period?

---

## Appendix: Regulatory Context

This platform enforces rules derived from the following U.S. federal regulations:

- **TCPA (Telephone Consumer Protection Act)**: Restricts automated calls, texts, and faxes. Requires prior express consent for autodialed calls to cell phones. Contact hours limited to 8am–9pm in the consumer's local timezone.
- **FDCPA (Fair Debt Collection Practices Act)**: Governs third-party debt collector behavior. Prohibits harassment, false statements, and unfair practices. Regulation F (2021) added a 7-calls-per-7-days frequency cap and electronic communication rules.
- **GLBA (Gramm-Leach-Bliley Act)**: Requires financial institutions to protect consumer financial data. Mandates PII safeguards.
- **TILA (Truth in Lending Act)**: Requires disclosure of credit terms. Relevant for payment plan discussions.

The platform's KOMPLY-lite compliance engine encodes the contact-timing and frequency rules from TCPA and FDCPA. PII sanitization addresses GLBA requirements. The scorecard evaluator validates required disclosures per FDCPA (Mini-Miranda) and client-specific policies.