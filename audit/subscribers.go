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

// --- Handlers ---

func handleContactAttempted(ctx context.Context, event *contact.ContactAttemptedEvent) error {
	dedupKey := fmt.Sprintf("contact-attempted:%d", event.ContactAttemptID)
	if isDuplicate(ctx, dedupKey) {
		rlog.Debug("duplicate contact-attempted event, skipping",
			"service", "audit",
			"dedup_key", dedupKey)
		return nil
	}

	newValue, _ := json.Marshal(event)
	_, err := recordAuditEntry(ctx, &RecordAuditReq{
		EntityType: "contact",
		EntityID:   event.ContactAttemptID,
		Action:     "contact_attempted",
		Actor:      "system:pubsub",
		NewValue:   json.RawMessage(newValue),
		Metadata:   buildMetadata(event.CorrelationID, dedupKey),
	})
	if err != nil {
		rlog.Error("failed to record contact-attempted audit entry",
			"service", "audit",
			"contact_attempt_id", event.ContactAttemptID,
			"err", err)
		return err
	}

	rlog.Info("contact-attempted audit recorded",
		"service", "audit",
		"contact_attempt_id", event.ContactAttemptID)
	return nil
}

func handleInteractionCreated(ctx context.Context, event *contact.InteractionCreatedEvent) error {
	dedupKey := fmt.Sprintf("interaction-created:%d", event.ContactAttemptID)
	if isDuplicate(ctx, dedupKey) {
		rlog.Debug("duplicate interaction-created event, skipping",
			"service", "audit",
			"dedup_key", dedupKey)
		return nil
	}

	newValue, _ := json.Marshal(event)
	_, err := recordAuditEntry(ctx, &RecordAuditReq{
		EntityType: "contact",
		EntityID:   event.ContactAttemptID,
		Action:     "interaction_created",
		Actor:      "system:pubsub",
		NewValue:   json.RawMessage(newValue),
		Metadata:   buildMetadata(event.CorrelationID, dedupKey),
	})
	if err != nil {
		rlog.Error("failed to record interaction-created audit entry",
			"service", "audit",
			"contact_attempt_id", event.ContactAttemptID,
			"err", err)
		return err
	}

	rlog.Info("interaction-created audit recorded",
		"service", "audit",
		"contact_attempt_id", event.ContactAttemptID)
	return nil
}

func handleConsentChanged(ctx context.Context, event *consumer.ConsentChangedEvent) error {
	dedupKey := fmt.Sprintf("consent-changed:%d:%s:%s", event.ConsumerID, event.ConsentStatus, event.ChangedAt)
	if isDuplicate(ctx, dedupKey) {
		rlog.Debug("duplicate consent-changed event, skipping",
			"service", "audit",
			"dedup_key", dedupKey)
		return nil
	}

	action := "consent_granted"
	if event.ConsentStatus == domain.ConsentRevoked {
		action = "consent_revoked"
	}

	newValue, _ := json.Marshal(event)
	_, err := recordAuditEntry(ctx, &RecordAuditReq{
		EntityType: "consumer",
		EntityID:   event.ConsumerID,
		Action:     action,
		Actor:      "system:pubsub",
		NewValue:   json.RawMessage(newValue),
		Metadata:   buildMetadata("", dedupKey),
	})
	if err != nil {
		rlog.Error("failed to record consent-changed audit entry",
			"service", "audit",
			"consumer_id", event.ConsumerID,
			"err", err)
		return err
	}

	rlog.Info("consent-changed audit recorded",
		"service", "audit",
		"consumer_id", event.ConsumerID,
		"action", action)
	return nil
}

func handleConsumerLifecycle(ctx context.Context, event *consumer.ConsumerLifecycleEvent) error {
	dedupKey := fmt.Sprintf("consumer-lifecycle:%d:%s", event.ConsumerID, event.Action)
	if isDuplicate(ctx, dedupKey) {
		rlog.Debug("duplicate consumer-lifecycle event, skipping",
			"service", "audit",
			"dedup_key", dedupKey)
		return nil
	}

	_, err := recordAuditEntry(ctx, &RecordAuditReq{
		EntityType: "consumer",
		EntityID:   event.ConsumerID,
		Action:     event.Action,
		Actor:      "system:pubsub",
		NewValue:   event.NewValue,
		Metadata:   buildMetadata("", dedupKey),
	})
	if err != nil {
		rlog.Error("failed to record consumer-lifecycle audit entry",
			"service", "audit",
			"consumer_id", event.ConsumerID,
			"err", err)
		return err
	}

	rlog.Info("consumer-lifecycle audit recorded",
		"service", "audit",
		"consumer_id", event.ConsumerID,
		"action", event.Action)
	return nil
}

func handleAccountLifecycle(ctx context.Context, event *account.AccountLifecycleEvent) error {
	dedupKey := fmt.Sprintf("account-lifecycle:%d:%s", event.AccountID, event.Action)
	if isDuplicate(ctx, dedupKey) {
		rlog.Debug("duplicate account-lifecycle event, skipping",
			"service", "audit",
			"dedup_key", dedupKey)
		return nil
	}

	_, err := recordAuditEntry(ctx, &RecordAuditReq{
		EntityType: "account",
		EntityID:   event.AccountID,
		Action:     event.Action,
		Actor:      "system:pubsub",
		OldValue:   event.OldValue,
		NewValue:   event.NewValue,
		Metadata:   buildMetadata("", dedupKey),
	})
	if err != nil {
		rlog.Error("failed to record account-lifecycle audit entry",
			"service", "audit",
			"account_id", event.AccountID,
			"err", err)
		return err
	}

	rlog.Info("account-lifecycle audit recorded",
		"service", "audit",
		"account_id", event.AccountID,
		"action", event.Action)
	return nil
}

func handlePaymentUpdated(ctx context.Context, event *payment.PaymentUpdatedEvent) error {
	dedupKey := fmt.Sprintf("payment-updated:%d:%s", event.PlanID, event.EventType)
	if isDuplicate(ctx, dedupKey) {
		rlog.Debug("duplicate payment-updated event, skipping",
			"service", "audit",
			"dedup_key", dedupKey)
		return nil
	}

	newValue, _ := json.Marshal(event)
	_, err := recordAuditEntry(ctx, &RecordAuditReq{
		EntityType: "payment_plan",
		EntityID:   event.PlanID,
		Action:     event.EventType,
		Actor:      "system:pubsub",
		NewValue:   json.RawMessage(newValue),
		Metadata:   buildMetadata(event.CorrelationID, dedupKey),
	})
	if err != nil {
		rlog.Error("failed to record payment-updated audit entry",
			"service", "audit",
			"plan_id", event.PlanID,
			"err", err)
		return err
	}

	rlog.Info("payment-updated audit recorded",
		"service", "audit",
		"plan_id", event.PlanID,
		"event_type", event.EventType)
	return nil
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
