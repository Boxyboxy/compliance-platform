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

// TestAuditPipelineIntegration exercises the full event-driven audit pipeline
// for consumer and account lifecycle events. It calls subscriber handlers
// directly (simulating Pub/Sub delivery) and verifies audit entries via queries.
//
// Contact workflow tests require Temporal server and are covered separately.
func TestAuditPipelineIntegration(t *testing.T) {
	ctx := context.Background()
	svc := newSvc()

	consumerSvc := &consumer.Service{}
	accountSvc := &account.Service{}

	// --- Step 1: Create consumer and simulate lifecycle event ---
	c, err := consumerSvc.CreateConsumer(ctx, &consumer.CreateConsumerReq{
		ExternalID: "integration-test-" + time.Now().Format("20060102150405.000"),
		FirstName:  "Integration",
		LastName:   "Test",
		Timezone:   "America/New_York",
	})
	if err != nil {
		t.Fatalf("CreateConsumer: %v", err)
	}

	consumerJSON, _ := json.Marshal(c)
	err = handleConsumerLifecycle(ctx, &consumer.ConsumerLifecycleEvent{
		ConsumerID: c.ID,
		Action:     "created",
		NewValue:   json.RawMessage(consumerJSON),
	})
	if err != nil {
		t.Fatalf("handleConsumerLifecycle: %v", err)
	}

	got, err := queryAuditLog(ctx, &GetAuditLogParams{
		EntityType: "consumer",
		EntityId:   c.ID,
		Action:     "created",
	})
	if err != nil {
		t.Fatalf("queryAuditLog(consumer/created): %v", err)
	}
	if len(got.Entries) < 1 {
		t.Error("expected audit entry for consumer created")
	}

	// --- Step 2: Create account and simulate lifecycle event ---
	a, err := accountSvc.CreateAccount(ctx, &account.CreateAccountReq{
		ConsumerID:       c.ID,
		OriginalCreditor: "Test Corp",
		AccountNumber:    "INTEG-001",
		BalanceDue:       1000.00,
	})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	accountJSON, _ := json.Marshal(a)
	err = handleAccountLifecycle(ctx, &account.AccountLifecycleEvent{
		AccountID:  a.ID,
		ConsumerID: a.ConsumerID,
		Action:     "created",
		NewValue:   json.RawMessage(accountJSON),
	})
	if err != nil {
		t.Fatalf("handleAccountLifecycle(created): %v", err)
	}

	got, err = queryAuditLog(ctx, &GetAuditLogParams{
		EntityType: "account",
		EntityId:   a.ID,
		Action:     "created",
	})
	if err != nil {
		t.Fatalf("queryAuditLog(account/created): %v", err)
	}
	if len(got.Entries) < 1 {
		t.Error("expected audit entry for account created")
	}

	// --- Step 3: Update account status and simulate lifecycle event ---
	_, err = accountSvc.UpdateAccountStatus(ctx, a.ID, &account.UpdateStatusReq{
		Status: domain.AccountStatusDelinquent,
	})
	if err != nil {
		t.Fatalf("UpdateAccountStatus: %v", err)
	}

	oldVal, _ := json.Marshal(map[string]string{"status": "current"})
	newVal, _ := json.Marshal(map[string]string{"status": "delinquent"})
	err = handleAccountLifecycle(ctx, &account.AccountLifecycleEvent{
		AccountID:  a.ID,
		ConsumerID: a.ConsumerID,
		Action:     "status_updated",
		OldValue:   json.RawMessage(oldVal),
		NewValue:   json.RawMessage(newVal),
	})
	if err != nil {
		t.Fatalf("handleAccountLifecycle(status_updated): %v", err)
	}

	got, err = queryAuditLog(ctx, &GetAuditLogParams{
		EntityType: "account",
		EntityId:   a.ID,
		Action:     "status_updated",
	})
	if err != nil {
		t.Fatalf("queryAuditLog(account/status_updated): %v", err)
	}
	if len(got.Entries) < 1 {
		t.Error("expected audit entry for account status_updated")
	}
	if len(got.Entries) > 0 {
		var oldStatus map[string]string
		if err := json.Unmarshal(got.Entries[0].OldValue, &oldStatus); err == nil {
			if oldStatus["status"] != "current" {
				t.Errorf("OldValue status = %q, want %q", oldStatus["status"], "current")
			}
		}
		var newStatus map[string]string
		if err := json.Unmarshal(got.Entries[0].NewValue, &newStatus); err == nil {
			if newStatus["status"] != "delinquent" {
				t.Errorf("NewValue status = %q, want %q", newStatus["status"], "delinquent")
			}
		}
	}

	// --- Step 4: Revoke consent and simulate event ---
	_, err = consumerSvc.UpdateConsent(ctx, c.ID, &consumer.UpdateConsentReq{
		ConsentStatus: domain.ConsentRevoked,
	})
	if err != nil {
		t.Fatalf("UpdateConsent(revoke): %v", err)
	}

	err = handleConsentChanged(ctx, &consumer.ConsentChangedEvent{
		ConsumerID:    c.ID,
		ConsentStatus: domain.ConsentRevoked,
		ChangedAt:     time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("handleConsentChanged(revoke): %v", err)
	}

	got, err = queryAuditLog(ctx, &GetAuditLogParams{
		EntityType: "consumer",
		EntityId:   c.ID,
		Action:     "consent_revoked",
	})
	if err != nil {
		t.Fatalf("queryAuditLog(consent_revoked): %v", err)
	}
	if len(got.Entries) < 1 {
		t.Error("expected audit entry for consent_revoked")
	}

	// --- Step 5: Grant consent back and simulate event ---
	_, err = consumerSvc.UpdateConsent(ctx, c.ID, &consumer.UpdateConsentReq{
		ConsentStatus: domain.ConsentGranted,
	})
	if err != nil {
		t.Fatalf("UpdateConsent(grant): %v", err)
	}

	err = handleConsentChanged(ctx, &consumer.ConsentChangedEvent{
		ConsumerID:    c.ID,
		ConsentStatus: domain.ConsentGranted,
		ChangedAt:     time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("handleConsentChanged(grant): %v", err)
	}

	got, err = queryAuditLog(ctx, &GetAuditLogParams{
		EntityType: "consumer",
		EntityId:   c.ID,
		Action:     "consent_granted",
	})
	if err != nil {
		t.Fatalf("queryAuditLog(consent_granted): %v", err)
	}
	if len(got.Entries) < 1 {
		t.Error("expected audit entry for consent_granted")
	}

	// --- Step 6: Query with action filter ---
	allEntries, err := svc.GetAuditLog(ctx, "consumer", c.ID)
	if err != nil {
		t.Fatalf("GetAuditLog(all): %v", err)
	}
	// Should have at least: created, consent_revoked, consent_granted
	if len(allEntries.Entries) < 3 {
		t.Errorf("got %d total consumer entries, want at least 3", len(allEntries.Entries))
	}

	// Filter by action=created should return exactly 1.
	createdOnly, err := queryAuditLog(ctx, &GetAuditLogParams{
		EntityType: "consumer",
		EntityId:   c.ID,
		Action:     "created",
	})
	if err != nil {
		t.Fatalf("queryAuditLog(action=created): %v", err)
	}
	if len(createdOnly.Entries) != 1 {
		t.Errorf("got %d created entries, want 1", len(createdOnly.Entries))
	}

	// --- Step 7: Query with time range ---
	since := time.Now().Add(-1 * time.Minute).Format(time.RFC3339)
	until := time.Now().Add(1 * time.Minute).Format(time.RFC3339)

	rangeEntries, err := queryAuditLog(ctx, &GetAuditLogParams{
		EntityType: "consumer",
		EntityId:   c.ID,
		Since:      since,
		Until:      until,
	})
	if err != nil {
		t.Fatalf("queryAuditLog(time range): %v", err)
	}
	if len(rangeEntries.Entries) < 3 {
		t.Errorf("got %d entries in time range, want at least 3", len(rangeEntries.Entries))
	}

	// Future range should return 0.
	futureRange, err := queryAuditLog(ctx, &GetAuditLogParams{
		EntityType: "consumer",
		EntityId:   c.ID,
		Since:      time.Now().Add(1 * time.Hour).Format(time.RFC3339),
		Until:      time.Now().Add(2 * time.Hour).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("queryAuditLog(future range): %v", err)
	}
	if len(futureRange.Entries) != 0 {
		t.Errorf("got %d entries for future range, want 0", len(futureRange.Entries))
	}
}
