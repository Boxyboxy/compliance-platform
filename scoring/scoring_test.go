package scoring

import (
	"context"
	"testing"

	"compliance-platform/contact"
	"compliance-platform/internal/domain"
)

func TestScoreInteraction_FullScore(t *testing.T) {
	ctx := context.Background()

	// Content matching all 3 rubric keywords.
	event := &contact.InteractionCreatedEvent{
		ContactAttemptID: 1,
		ConsumerID:       1,
		AccountID:        1,
		Channel:          domain.ChannelSMS,
		SanitizedContent: "Hello, this is agent Smith speaking with you. This is an attempt to collect a debt. We can offer a payment plan.",
		CorrelationID:    "test-full",
	}

	err := handleInteractionCreated(ctx, event)
	if err != nil {
		t.Fatalf("handleInteractionCreated() error = %v", err)
	}
}

func TestScoreInteraction_PartialScore(t *testing.T) {
	ctx := context.Background()

	// Content missing required keywords (no mini-miranda).
	event := &contact.InteractionCreatedEvent{
		ContactAttemptID: 2,
		ConsumerID:       1,
		AccountID:        1,
		Channel:          domain.ChannelSMS,
		SanitizedContent: "Hello, this is agent Smith speaking with you. We can offer a payment plan.",
		CorrelationID:    "test-partial",
	}

	err := handleInteractionCreated(ctx, event)
	if err != nil {
		t.Fatalf("handleInteractionCreated() error = %v", err)
	}
}

func TestScoreInteraction_EmptyContent(t *testing.T) {
	ctx := context.Background()

	// Empty content — should be skipped.
	event := &contact.InteractionCreatedEvent{
		ContactAttemptID: 3,
		ConsumerID:       1,
		AccountID:        1,
		Channel:          domain.ChannelSMS,
		SanitizedContent: "",
		CorrelationID:    "test-empty",
	}

	err := handleInteractionCreated(ctx, event)
	if err != nil {
		t.Fatalf("handleInteractionCreated() error = %v (expected nil for empty content)", err)
	}
}

func TestScoreInteraction_Idempotency(t *testing.T) {
	ctx := context.Background()

	event := &contact.InteractionCreatedEvent{
		ContactAttemptID: 4,
		ConsumerID:       1,
		AccountID:        1,
		Channel:          domain.ChannelSMS,
		SanitizedContent: "Hello, this is agent Smith speaking with you. This is an attempt to collect a debt.",
		CorrelationID:    "test-idempotent",
	}

	// Call twice — second call should also succeed (PATCH overwrites with same result).
	err := handleInteractionCreated(ctx, event)
	if err != nil {
		t.Fatalf("first call error = %v", err)
	}

	err = handleInteractionCreated(ctx, event)
	if err != nil {
		t.Fatalf("second call error = %v", err)
	}
}
