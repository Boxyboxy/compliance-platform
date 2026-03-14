package audit

import (
	"context"
	"encoding/json"

	"encore.dev/pubsub"
	"encore.dev/rlog"

	"compliance-platform/contact"
)

// Subscribe to contact-attempted events.
var _ = pubsub.NewSubscription(
	contact.ContactAttempted,
	"audit-contact-attempted",
	pubsub.SubscriptionConfig[*contact.ContactAttemptedEvent]{
		Handler: handleContactAttempted,
	},
)

// Subscribe to interaction-created events.
var _ = pubsub.NewSubscription(
	contact.InteractionCreated,
	"audit-interaction-created",
	pubsub.SubscriptionConfig[*contact.InteractionCreatedEvent]{
		Handler: handleInteractionCreated,
	},
)

func handleContactAttempted(ctx context.Context, event *contact.ContactAttemptedEvent) error {
	newValue, _ := json.Marshal(event)

	_, err := recordAuditEntry(ctx, &RecordAuditReq{
		EntityType: "contact",
		EntityID:   event.ContactAttemptID,
		Action:     "contact_attempted",
		Actor:      "system:pubsub",
		NewValue:   json.RawMessage(newValue),
		Metadata:   json.RawMessage(metadataJSON(event.CorrelationID)),
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
	newValue, _ := json.Marshal(event)

	_, err := recordAuditEntry(ctx, &RecordAuditReq{
		EntityType: "contact",
		EntityID:   event.ContactAttemptID,
		Action:     "interaction_created",
		Actor:      "system:pubsub",
		NewValue:   json.RawMessage(newValue),
		Metadata:   json.RawMessage(metadataJSON(event.CorrelationID)),
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

func metadataJSON(correlationID string) []byte {
	if correlationID == "" {
		return nil
	}
	data, _ := json.Marshal(map[string]string{"correlation_id": correlationID})
	return data
}
