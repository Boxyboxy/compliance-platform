package audit

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"compliance-platform/account"
	"compliance-platform/consumer"
	"compliance-platform/internal/domain"
)

func newSvc() *Service { return &Service{} }

func TestRecordAudit(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	tests := []struct {
		name    string
		req     *RecordAuditReq
		wantErr bool
		check   func(t *testing.T, got *AuditEntry)
	}{
		{
			name: "valid entry with all fields",
			req: &RecordAuditReq{
				EntityType: "consumer",
				EntityID:   100,
				Action:     "created",
				Actor:      "api",
				NewValue:   json.RawMessage(`{"first_name":"Jane"}`),
				Metadata:   json.RawMessage(`{"correlation_id":"abc-123"}`),
			},
			check: func(t *testing.T, got *AuditEntry) {
				if got.ID == 0 {
					t.Error("expected non-zero ID")
				}
				if got.EntityType != "consumer" {
					t.Errorf("EntityType = %q, want %q", got.EntityType, "consumer")
				}
				if got.Action != "created" {
					t.Errorf("Action = %q, want %q", got.Action, "created")
				}
			},
		},
		{
			name: "valid entry minimal fields",
			req: &RecordAuditReq{
				EntityType: "account",
				EntityID:   200,
				Action:     "status_updated",
				Actor:      "system",
			},
			check: func(t *testing.T, got *AuditEntry) {
				if got.EntityType != "account" {
					t.Errorf("EntityType = %q, want %q", got.EntityType, "account")
				}
			},
		},
		{
			name: "missing entity_type",
			req: &RecordAuditReq{
				EntityID: 100,
				Action:   "created",
				Actor:    "api",
			},
			wantErr: true,
		},
		{
			name: "missing entity_id",
			req: &RecordAuditReq{
				EntityType: "consumer",
				Action:     "created",
				Actor:      "api",
			},
			wantErr: true,
		},
		{
			name: "missing action",
			req: &RecordAuditReq{
				EntityType: "consumer",
				EntityID:   100,
				Actor:      "api",
			},
			wantErr: true,
		},
		{
			name: "missing actor",
			req: &RecordAuditReq{
				EntityType: "consumer",
				EntityID:   100,
				Action:     "created",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.RecordAudit(ctx, tt.req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("RecordAudit() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

func TestGetAuditLog(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	// Seed entries.
	_, err := svc.RecordAudit(ctx, &RecordAuditReq{
		EntityType: "contact",
		EntityID:   300,
		Action:     "contact_attempted",
		Actor:      "system:pubsub",
		NewValue:   json.RawMessage(`{"status":"blocked"}`),
	})
	if err != nil {
		t.Fatalf("seed 1: %v", err)
	}
	_, err = svc.RecordAudit(ctx, &RecordAuditReq{
		EntityType: "contact",
		EntityID:   300,
		Action:     "interaction_created",
		Actor:      "system:pubsub",
	})
	if err != nil {
		t.Fatalf("seed 2: %v", err)
	}

	tests := []struct {
		name       string
		entityType string
		entityID   int64
		wantMin    int
	}{
		{
			name:       "entity with entries",
			entityType: "contact",
			entityID:   300,
			wantMin:    2,
		},
		{
			name:       "entity with no entries",
			entityType: "contact",
			entityID:   999999,
			wantMin:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.GetAuditLog(ctx, tt.entityType, tt.entityID)
			if err != nil {
				t.Fatalf("GetAuditLog() error = %v", err)
			}
			if len(got.Entries) < tt.wantMin {
				t.Errorf("got %d entries, want at least %d", len(got.Entries), tt.wantMin)
			}
			// Verify DESC ordering.
			if len(got.Entries) >= 2 {
				if got.Entries[0].CreatedAt.Before(got.Entries[1].CreatedAt) {
					t.Error("entries not in DESC order by created_at")
				}
			}
		})
	}
}

func TestGetAuditLog_ActionFilter(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	// Seed entries with different actions.
	entityID := time.Now().UnixNano() % 1000000
	for _, action := range []string{"created", "updated", "deleted"} {
		_, err := svc.RecordAudit(ctx, &RecordAuditReq{
			EntityType: "test_filter",
			EntityID:   entityID,
			Action:     action,
			Actor:      "test",
		})
		if err != nil {
			t.Fatalf("seed %s: %v", action, err)
		}
	}

	// Use queryAuditLog (internal) to test action filter.
	got, err := queryAuditLog(ctx, &GetAuditLogParams{
		EntityType: "test_filter",
		EntityId:   entityID,
		Action:     "created",
	})
	if err != nil {
		t.Fatalf("queryAuditLog() error = %v", err)
	}
	if len(got.Entries) != 1 {
		t.Errorf("got %d entries, want 1", len(got.Entries))
	}
	if len(got.Entries) > 0 && got.Entries[0].Action != "created" {
		t.Errorf("Action = %q, want %q", got.Entries[0].Action, "created")
	}

	// Also test via SearchAuditLog API.
	got, err = svc.SearchAuditLog(ctx, &GetAuditLogParams{
		EntityType: "test_filter",
		EntityId:   entityID,
		Action:     "updated",
	})
	if err != nil {
		t.Fatalf("SearchAuditLog() error = %v", err)
	}
	if len(got.Entries) != 1 {
		t.Errorf("SearchAuditLog got %d entries, want 1", len(got.Entries))
	}
}

func TestGetAuditLog_TimeRange(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	entityID := time.Now().UnixNano()%1000000 + 1000000

	// Seed an entry.
	entry, err := svc.RecordAudit(ctx, &RecordAuditReq{
		EntityType: "test_time",
		EntityID:   entityID,
		Action:     "created",
		Actor:      "test",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Query with since = 1 minute ago, until = 1 minute from now.
	since := entry.CreatedAt.Add(-1 * time.Minute).Format(time.RFC3339)
	until := entry.CreatedAt.Add(1 * time.Minute).Format(time.RFC3339)

	got, err := queryAuditLog(ctx, &GetAuditLogParams{
		EntityType: "test_time",
		EntityId:   entityID,
		Since:      since,
		Until:      until,
	})
	if err != nil {
		t.Fatalf("queryAuditLog() error = %v", err)
	}
	if len(got.Entries) < 1 {
		t.Error("expected at least 1 entry within time range")
	}

	// Query with future time range — should return 0.
	futureSince := entry.CreatedAt.Add(1 * time.Hour).Format(time.RFC3339)
	futureUntil := entry.CreatedAt.Add(2 * time.Hour).Format(time.RFC3339)

	got, err = queryAuditLog(ctx, &GetAuditLogParams{
		EntityType: "test_time",
		EntityId:   entityID,
		Since:      futureSince,
		Until:      futureUntil,
	})
	if err != nil {
		t.Fatalf("queryAuditLog() future error = %v", err)
	}
	if len(got.Entries) != 0 {
		t.Errorf("got %d entries for future range, want 0", len(got.Entries))
	}
}

func TestIdempotency(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	entityID := time.Now().UnixNano()%1000000 + 2000000
	dedupKey := "test-dedup:unique-key-123"

	metadata, _ := json.Marshal(map[string]string{"dedup_key": dedupKey})

	// First insert.
	_, err := svc.RecordAudit(ctx, &RecordAuditReq{
		EntityType: "test_dedup",
		EntityID:   entityID,
		Action:     "created",
		Actor:      "test",
		Metadata:   json.RawMessage(metadata),
	})
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}

	// isDuplicate should now return true.
	if !isDuplicate(ctx, dedupKey) {
		t.Error("isDuplicate() = false after insert, want true")
	}

	// isDuplicate for a different key should return false.
	if isDuplicate(ctx, "test-dedup:nonexistent") {
		t.Error("isDuplicate() = true for nonexistent key, want false")
	}
}

func TestAppendOnlyEnforcement(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	entityID := time.Now().UnixNano()%1000000 + 3000000

	entry, err := svc.RecordAudit(ctx, &RecordAuditReq{
		EntityType: "test_immutable",
		EntityID:   entityID,
		Action:     "created",
		Actor:      "test",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Attempt UPDATE — should fail due to trigger.
	_, err = db.Exec(ctx, `UPDATE audit_log SET action = 'modified' WHERE id = $1`, entry.ID)
	if err == nil {
		t.Error("UPDATE on audit_log succeeded, expected trigger to block it")
	}

	// Attempt DELETE — should fail due to trigger.
	_, err = db.Exec(ctx, `DELETE FROM audit_log WHERE id = $1`, entry.ID)
	if err == nil {
		t.Error("DELETE on audit_log succeeded, expected trigger to block it")
	}

	// Verify the entry is unchanged.
	got, err := svc.GetAuditLog(ctx, "test_immutable", entityID)
	if err != nil {
		t.Fatalf("GetAuditLog() error = %v", err)
	}
	found := false
	for _, e := range got.Entries {
		if e.ID == entry.ID {
			found = true
			if e.Action != "created" {
				t.Errorf("Action = %q after attempted mutation, want %q", e.Action, "created")
			}
		}
	}
	if !found {
		t.Error("entry not found after attempted delete")
	}
}

func TestConsentChangedSubscriber(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		event      *consumer.ConsentChangedEvent
		wantAction string
	}{
		{
			name: "consent revoked",
			event: &consumer.ConsentChangedEvent{
				ConsumerID:    time.Now().UnixNano()%1000000 + 4000000,
				ConsentStatus: domain.ConsentRevoked,
				ChangedAt:     time.Now().UTC().Format(time.RFC3339),
			},
			wantAction: "consent_revoked",
		},
		{
			name: "consent granted",
			event: &consumer.ConsentChangedEvent{
				ConsumerID:    time.Now().UnixNano()%1000000 + 4100000,
				ConsentStatus: domain.ConsentGranted,
				ChangedAt:     time.Now().UTC().Format(time.RFC3339),
			},
			wantAction: "consent_granted",
		},
	}

	svc := newSvc()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handleConsentChanged(ctx, tt.event)
			if err != nil {
				t.Fatalf("handleConsentChanged() error = %v", err)
			}

			got, err := svc.GetAuditLog(ctx, "consumer", tt.event.ConsumerID)
			if err != nil {
				t.Fatalf("GetAuditLog() error = %v", err)
			}
			if len(got.Entries) == 0 {
				t.Fatal("expected at least 1 audit entry")
			}
			if got.Entries[0].Action != tt.wantAction {
				t.Errorf("Action = %q, want %q", got.Entries[0].Action, tt.wantAction)
			}
			if got.Entries[0].EntityType != "consumer" {
				t.Errorf("EntityType = %q, want %q", got.Entries[0].EntityType, "consumer")
			}
		})
	}
}

func TestConsumerLifecycleSubscriber(t *testing.T) {
	ctx := context.Background()
	svc := newSvc()

	consumerID := time.Now().UnixNano()%1000000 + 5000000
	err := handleConsumerLifecycle(ctx, &consumer.ConsumerLifecycleEvent{
		ConsumerID: consumerID,
		Action:     "created",
		NewValue:   json.RawMessage(`{"first_name":"Test"}`),
	})
	if err != nil {
		t.Fatalf("handleConsumerLifecycle() error = %v", err)
	}

	got, err := svc.GetAuditLog(ctx, "consumer", consumerID)
	if err != nil {
		t.Fatalf("GetAuditLog() error = %v", err)
	}
	if len(got.Entries) == 0 {
		t.Fatal("expected at least 1 audit entry")
	}
	if got.Entries[0].Action != "created" {
		t.Errorf("Action = %q, want %q", got.Entries[0].Action, "created")
	}
	if got.Entries[0].EntityType != "consumer" {
		t.Errorf("EntityType = %q, want %q", got.Entries[0].EntityType, "consumer")
	}
}

func TestAccountLifecycleSubscriber(t *testing.T) {
	ctx := context.Background()
	svc := newSvc()

	accountID := time.Now().UnixNano()%1000000 + 6000000

	// Test "created" action.
	err := handleAccountLifecycle(ctx, &account.AccountLifecycleEvent{
		AccountID:  accountID,
		ConsumerID: 1,
		Action:     "created",
		NewValue:   json.RawMessage(`{"status":"current"}`),
	})
	if err != nil {
		t.Fatalf("handleAccountLifecycle(created) error = %v", err)
	}

	// Test "status_updated" action.
	err = handleAccountLifecycle(ctx, &account.AccountLifecycleEvent{
		AccountID:  accountID,
		ConsumerID: 1,
		Action:     "status_updated",
		OldValue:   json.RawMessage(`{"status":"current"}`),
		NewValue:   json.RawMessage(`{"status":"delinquent"}`),
	})
	if err != nil {
		t.Fatalf("handleAccountLifecycle(status_updated) error = %v", err)
	}

	got, err := svc.GetAuditLog(ctx, "account", accountID)
	if err != nil {
		t.Fatalf("GetAuditLog() error = %v", err)
	}
	if len(got.Entries) < 2 {
		t.Fatalf("got %d entries, want at least 2", len(got.Entries))
	}

	// DESC order: status_updated first, created second.
	if got.Entries[0].Action != "status_updated" {
		t.Errorf("first entry Action = %q, want %q", got.Entries[0].Action, "status_updated")
	}
	if got.Entries[1].Action != "created" {
		t.Errorf("second entry Action = %q, want %q", got.Entries[1].Action, "created")
	}

	// Verify old/new values on status_updated (use JSON unmarshal to handle formatting).
	var oldVal map[string]string
	if err := json.Unmarshal(got.Entries[0].OldValue, &oldVal); err != nil {
		t.Fatalf("failed to unmarshal OldValue: %v", err)
	}
	if oldVal["status"] != "current" {
		t.Errorf("OldValue status = %q, want %q", oldVal["status"], "current")
	}
}
