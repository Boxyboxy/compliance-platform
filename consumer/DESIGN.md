# Consumer Service — Design Notes

## Responsibility

This service is the single source of truth for consumer identity and consent. Every other service that needs to know whether a consumer can be contacted, who their attorney is, or what timezone they live in must read from this service rather than duplicate that data.

## Schema Decisions

### `external_id` (TEXT UNIQUE NOT NULL)
Client systems have their own consumer identifiers. `external_id` is the bridge. Using a `TEXT` column (not an integer FK) keeps the schema decoupled from whatever numbering scheme the client uses. The `UNIQUE` constraint enforces that a single consumer record exists per client ID.

### `consent_status` (TEXT CHECK + DEFAULT 'granted')
A CHECK constraint (`'granted'` | `'revoked'`) was chosen over a boolean `do_not_contact` field or a separate `consent_revoked_at` timestamp for two reasons:
1. The status is human-readable in raw SQL queries and audit logs.
2. It is extensible — future consent granularity (per-channel, per-creditor) would expand the CHECK values rather than add new boolean columns.

The `do_not_contact` boolean exists as a separate flag because it represents a _regulatory_ do-not-contact flag (e.g. a cease-and-desist letter) that is distinct from a consumer voluntarily revoking electronic consent. Both independently block outbound contact; neither unblocks the other.

### `attorney_on_file` (BOOLEAN NOT NULL DEFAULT false)
FDCPA § 805(a)(2) prohibits communicating with a consumer who is represented by an attorney if the collector knows of that representation. This flag encodes that knowledge. It is a separate field (not a consent status) because the legal basis for blocking is different and it may need to be set independently of consent.

### `timezone` (TEXT NOT NULL DEFAULT 'America/New_York')
Stored as an IANA timezone string (e.g. `America/Los_Angeles`) rather than a UTC offset. UTC offsets are not stable — they change with DST transitions. The compliance engine uses `time.LoadLocation(timezone)` at check time to get the current offset. The default of `America/New_York` is conservative: if timezone is unknown, Eastern time is the most common U.S. debt-servicing zone.

### `phone` / `email` (TEXT, nullable)
Optional. Stored as NULL when not provided so that downstream code can distinguish "no phone on file" from "phone is an empty string." In the API layer, both scan to an empty string in the response (via `sql.NullString`) and are omitted from JSON via `omitempty`.

## Consent Event Design

When consent is revoked, `UpdateConsent` publishes a `consent-changed` event to the `consent-changed` Pub/Sub topic **after** the DB write succeeds. This is intentionally asynchronous — the API returns the updated consumer record immediately; the contact service cancels pending workflows as a subscriber.

Why not a synchronous call from consumer → contact service?

- Avoids coupling the consumer service to the contact service's availability.
- The contact service can scale independently and may be unavailable during a deployment.
- Encore Pub/Sub provides at-least-once delivery, so a transient failure in the contact service will be retried without the consumer service needing to retry.

The tradeoff: there is a small window (the Pub/Sub delivery latency, typically < 1 second) during which a pending workflow could still attempt delivery after consent is revoked. This is mitigated by the compliance pre-check inside the Temporal workflow, which re-reads consent status from the DB at execution time.

## PII Considerations

### Data stored in `consumers`
- `phone` and `email` are contact channels, not inherently sensitive PII in isolation, but they become sensitive in combination with financial data.
- Neither field is encrypted at rest in v1. The schema includes them as plaintext TEXT columns.
- **Future work**: Apply column-level encryption (e.g., `pgcrypto` AES) to `phone` and `email` fields, with key management via AWS KMS or HashiCorp Vault. An ADR should be written before implementing.

### Data not stored here
Consumer financial data (account numbers, balances, SSNs) lives in the `account` service or is sanitized by the compliance PII sanitizer before storage. The consumer service intentionally holds only contact metadata, not financial account details.

### Audit trail
All consent changes must produce an audit log entry (implemented in Phase 3 via the `audit` service subscribing to `consent-changed`). Compliance officers require an immutable record of every consent revocation with a timestamp for TCPA audit purposes.

## Error Codes

| Condition | HTTP status | `errs.Code` |
|---|---|---|
| Missing required field | 400 | `InvalidArgument` |
| Invalid consent_status value | 400 | `InvalidArgument` |
| Consumer not found | 404 | `NotFound` |
| Duplicate external_id | 500 (DB constraint) | — (surface as-is) |

Duplicate `external_id` is left as a 500 for now. In a future iteration, wrap the `pq` unique violation error and return a 409 Conflict.
