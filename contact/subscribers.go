package contact

import (
	"context"

	"encore.dev/pubsub"
	"encore.dev/rlog"

	"compliance-platform/consumer"
	"compliance-platform/internal/domain"
)

// Subscribe to consent-changed: when consent is revoked, block all pending contacts.
var _ = pubsub.NewSubscription(
	consumer.ConsentChanged,
	"contact-consent-changed",
	pubsub.SubscriptionConfig[*consumer.ConsentChangedEvent]{
		Handler: handleConsentChanged,
	},
)

func handleConsentChanged(ctx context.Context, event *consumer.ConsentChangedEvent) error {
	if event.ConsentStatus != domain.ConsentRevoked {
		return nil
	}

	result, err := db.Exec(ctx, `
		UPDATE contact_attempts
		SET status = 'blocked', block_reason = 'consent_revoked', completed_at = now()
		WHERE consumer_id = $1 AND status = 'pending'
	`, event.ConsumerID)
	if err != nil {
		rlog.Error("failed to block pending contacts on consent revocation",
			"service", "contact",
			"consumer_id", event.ConsumerID,
			"err", err)
		return err
	}

	affected := result.RowsAffected()
	rlog.Info("pending contacts blocked due to consent revocation",
		"service", "contact",
		"consumer_id", event.ConsumerID,
		"blocked_count", affected)
	return nil
}
