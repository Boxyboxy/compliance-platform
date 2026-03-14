package scoring

import (
	"context"

	"encore.dev/pubsub"
	"encore.dev/rlog"

	"compliance-platform/contact"
)

//encore:service
type Service struct{}

// Subscribe to interaction-created events for async QA scoring.
// Phase 4 will implement full re-scoring logic.
var _ = pubsub.NewSubscription(
	contact.InteractionCreated,
	"scoring-interaction-created",
	pubsub.SubscriptionConfig[*contact.InteractionCreatedEvent]{
		Handler: handleInteractionCreated,
	},
)

func handleInteractionCreated(ctx context.Context, event *contact.InteractionCreatedEvent) error {
	rlog.Info("interaction received for scoring",
		"service", "scoring",
		"contact_attempt_id", event.ContactAttemptID,
		"consumer_id", event.ConsumerID,
		"channel", event.Channel,
		"correlation_id", event.CorrelationID)
	return nil
}
