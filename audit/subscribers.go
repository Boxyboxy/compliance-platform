package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"encore.dev/pubsub"
	"encore.dev/rlog"

	"compliance-platform/account"
	"compliance-platform/consumer"
	"compliance-platform/contact"
	"compliance-platform/internal/domain"
	"compliance-platform/payment"
)

// --- Subscriptions ---

var _ = pubsub.NewSubscription(
	contact.ContactAttempted,
	"audit-contact-attempted",
	pubsub.SubscriptionConfig[*contact.ContactAttemptedEvent]{
		Handler: handleContactAttempted,
	},
)

var _ = pubsub.NewSubscription(
	contact.InteractionCreated,
	"audit-interaction-created",
	pubsub.SubscriptionConfig[*contact.InteractionCreatedEvent]{
		Handler: handleInteractionCreated,
	},
)

var _ = pubsub.NewSubscription(
	consumer.ConsentChanged,
	"audit-consent-changed",
	pubsub.SubscriptionConfig[*consumer.ConsentChangedEvent]{
		Handler: handleConsentChanged,
	},
)

var _ = pubsub.NewSubscription(
	consumer.ConsumerLifecycle,
	"audit-consumer-lifecycle",
	pubsub.SubscriptionConfig[*consumer.ConsumerLifecycleEvent]{
		Handler: handleConsumerLifecycle,
	},
)

var _ = pubsub.NewSubscription(
	account.AccountLifecycle,
	"audit-account-lifecycle",
	pubsub.SubscriptionConfig[*account.AccountLifecycleEvent]{
		Handler: handleAccountLifecycle,
	},
)

var _ = pubsub.NewSubscription(
	payment.PaymentUpdated,
	"audit-payment-updated",
	pubsub.SubscriptionConfig[*payment.PaymentUpdatedEvent]{
		Handler: handlePaymentUpdated,
	},
)

// --- Generic audit handler ---

// auditDescriptor describes how to extract audit fields from a Pub/Sub event.
type auditDescriptor struct {
	EntityType    string
	EntityID      int64
	Action        string
	DedupKey      string
	CorrelationID string
	OldValue      json.RawMessage
	NewValue      json.RawMessage
}

// handleAuditEvent is the single entry point for all audit Pub/Sub handlers.
// It performs dedup, records the audit entry, and logs the outcome.
func handleAuditEvent(ctx context.Context, desc auditDescriptor) error {
	if isDuplicate(ctx, desc.DedupKey) {
		rlog.Debug("duplicate event, skipping",
			"service", "audit",
			"dedup_key", desc.DedupKey)
		return nil
	}

	_, err := recordAuditEntry(ctx, &RecordAuditReq{
		EntityType: desc.EntityType,
		EntityID:   desc.EntityID,
		Action:     desc.Action,
		Actor:      "system:pubsub",
		OldValue:   desc.OldValue,
		NewValue:   desc.NewValue,
		Metadata:   buildMetadata(desc.CorrelationID, desc.DedupKey),
	})
	if err != nil {
		rlog.Error("failed to record audit entry",
			"service", "audit",
			"entity_type", desc.EntityType,
			"entity_id", desc.EntityID,
			"action", desc.Action,
			"err", err)
		return err
	}

	rlog.Info("audit entry recorded",
		"service", "audit",
		"entity_type", desc.EntityType,
		"entity_id", desc.EntityID,
		"action", desc.Action)
	return nil
}

// marshalValue is a convenience wrapper that marshals v to JSON for audit storage.
func marshalValue(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return json.RawMessage(data)
}

// --- Handlers (thin extractors that delegate to handleAuditEvent) ---

func handleContactAttempted(ctx context.Context, event *contact.ContactAttemptedEvent) error {
	return handleAuditEvent(ctx, auditDescriptor{
		EntityType:    "contact",
		EntityID:      event.ContactAttemptID,
		Action:        "contact_attempted",
		DedupKey:      fmt.Sprintf("contact-attempted:%d", event.ContactAttemptID),
		CorrelationID: event.CorrelationID,
		NewValue:      marshalValue(event),
	})
}

func handleInteractionCreated(ctx context.Context, event *contact.InteractionCreatedEvent) error {
	return handleAuditEvent(ctx, auditDescriptor{
		EntityType:    "contact",
		EntityID:      event.ContactAttemptID,
		Action:        "interaction_created",
		DedupKey:      fmt.Sprintf("interaction-created:%d", event.ContactAttemptID),
		CorrelationID: event.CorrelationID,
		NewValue:      marshalValue(event),
	})
}

func handleConsentChanged(ctx context.Context, event *consumer.ConsentChangedEvent) error {
	action := "consent_granted"
	if event.ConsentStatus == domain.ConsentRevoked {
		action = "consent_revoked"
	}
	return handleAuditEvent(ctx, auditDescriptor{
		EntityType: "consumer",
		EntityID:   event.ConsumerID,
		Action:     action,
		DedupKey:   fmt.Sprintf("consent-changed:%d:%s:%s", event.ConsumerID, event.ConsentStatus, event.ChangedAt),
		NewValue:   marshalValue(event),
	})
}

func handleConsumerLifecycle(ctx context.Context, event *consumer.ConsumerLifecycleEvent) error {
	return handleAuditEvent(ctx, auditDescriptor{
		EntityType: "consumer",
		EntityID:   event.ConsumerID,
		Action:     event.Action,
		DedupKey:   fmt.Sprintf("consumer-lifecycle:%d:%s", event.ConsumerID, event.Action),
		NewValue:   event.NewValue,
	})
}

func handleAccountLifecycle(ctx context.Context, event *account.AccountLifecycleEvent) error {
	return handleAuditEvent(ctx, auditDescriptor{
		EntityType: "account",
		EntityID:   event.AccountID,
		Action:     event.Action,
		DedupKey:   fmt.Sprintf("account-lifecycle:%d:%s", event.AccountID, event.Action),
		OldValue:   event.OldValue,
		NewValue:   event.NewValue,
	})
}

func handlePaymentUpdated(ctx context.Context, event *payment.PaymentUpdatedEvent) error {
	return handleAuditEvent(ctx, auditDescriptor{
		EntityType:    "payment_plan",
		EntityID:      event.PlanID,
		Action:        event.EventType,
		DedupKey:      fmt.Sprintf("payment-updated:%d:%s", event.PlanID, event.EventType),
		CorrelationID: event.CorrelationID,
		NewValue:      marshalValue(event),
	})
}

// --- Helpers ---

// buildMetadata constructs the metadata JSON with correlation_id and dedup_key.
func buildMetadata(correlationID, dedupKey string) json.RawMessage {
	m := map[string]string{}
	if correlationID != "" {
		m["correlation_id"] = correlationID
	}
	if dedupKey != "" {
		m["dedup_key"] = dedupKey
	}
	if len(m) == 0 {
		return nil
	}
	data, _ := json.Marshal(m)
	return json.RawMessage(data)
}

// isDuplicate checks if an audit entry with the given dedup_key already exists.
func isDuplicate(ctx context.Context, dedupKey string) bool {
	var exists bool
	err := db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM audit_log WHERE metadata->>'dedup_key' = $1)
	`, dedupKey).Scan(&exists)
	if err != nil {
		// On error, assume not duplicate to avoid dropping events.
		rlog.Error("dedup check failed, proceeding with insert",
			"service", "audit",
			"dedup_key", dedupKey,
			"err", err)
		return false
	}
	return exists
}
