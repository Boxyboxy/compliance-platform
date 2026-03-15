# Test Coverage

## Running Coverage

```bash
# Generate coverage profile (requires Encore for DB-backed services)
encore test ./... -coverprofile=coverage.out

# View HTML report
go tool cover -html=coverage.out

# Print per-function coverage
go tool cover -func=coverage.out
```

## Coverage Targets

| Service | Target | Notes |
|---------|--------|-------|
| `compliance/` | >90% | Core business logic — rules engine, PII sanitizer, scorecard evaluator |
| `consumer/` | >80% | CRUD + consent management |
| `account/` | >80% | CRUD + status transitions |
| `payment/` | >80% | Payment plan lifecycle + state machine |
| `audit/` | >70% | Append-only log + subscriber handlers |
| `scoring/` | >70% | Async QA scoring subscriber |
| `workflows/` | >70% | Temporal workflow + activity definitions |

## Notes

- **`contact/`** has lower unit test coverage because its primary logic (Temporal workflow orchestration) is integration-tested via the full `encore run` + Temporal worker stack.
- **`workflows/worker/`** is excluded from coverage targets — it is a `main` package that wires dependencies and starts the Temporal worker.
- Services with Encore-managed databases (`consumer`, `account`, `contact`, `payment`, `audit`) require `encore test` instead of `go test` so that Encore provisions the test database.
- The `compliance/` service has 30+ parametrized tests covering all 5 TCPA/FDCPA rules, PII sanitization patterns, and scorecard evaluation.
