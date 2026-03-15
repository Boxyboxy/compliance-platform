package scoring

import (
	"context"
	"encoding/json"

	"encore.dev/pubsub"
	"encore.dev/rlog"

	"compliance-platform/compliance"
	"compliance-platform/contact"
)

//encore:service
type Service struct{}

// Subscribe to interaction-created events for async QA scoring.
//
// Async scoring exists alongside in-workflow scoring because:
// - It enables re-scoring if the rubric is updated later.
// - It decouples QA scoring from the contact flow so scoring failures don't block delivery.
var _ = pubsub.NewSubscription(
	contact.InteractionCreated,
	"scoring-interaction-created",
	pubsub.SubscriptionConfig[*contact.InteractionCreatedEvent]{
		Handler: handleInteractionCreated,
	},
)

// defaultRubric returns the standard 3-item QA scorecard rubric.
func defaultRubric() compliance.ScorecardRubric {
	return compliance.ScorecardRubric{
		Name: "default",
		Items: []compliance.ScorecardItem{
			{
				ID:          "agent-id",
				Description: "Agent identifies themselves",
				Required:    true,
				Keywords:    []string{"this is", "my name is", "speaking with"},
				Weight:      3,
			},
			{
				ID:          "mini-miranda",
				Description: "Mini-Miranda disclosure provided",
				Required:    true,
				Keywords:    []string{"this is an attempt to collect a debt", "debt collector"},
				Weight:      4,
			},
			{
				ID:          "payment-option",
				Description: "Payment options discussed",
				Required:    false,
				Keywords:    []string{"payment plan", "pay in full", "settlement"},
				Weight:      3,
			},
		},
	}
}

func handleInteractionCreated(ctx context.Context, event *contact.InteractionCreatedEvent) error {
	// Skip if no content to score (blocked contacts have no message).
	if event.SanitizedContent == "" {
		rlog.Debug("skipping scoring for empty content",
			"service", "scoring",
			"contact_attempt_id", event.ContactAttemptID)
		return nil
	}

	rubric := defaultRubric()
	result, err := compliance.ScoreInteraction(ctx, &compliance.ScoreRequest{
		Transcript: event.SanitizedContent,
		Rubric:     rubric,
	})
	if err != nil {
		rlog.Error("failed to score interaction",
			"service", "scoring",
			"contact_attempt_id", event.ContactAttemptID,
			"err", err)
		return err
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		rlog.Error("failed to marshal score result",
			"service", "scoring",
			"contact_attempt_id", event.ContactAttemptID,
			"err", err)
		return err
	}

	// Update the contact attempt with the scorecard result.
	// The PATCH endpoint overwrites scorecard_result. With the same rubric
	// the result is identical — the overwrite is a safe no-op.
	err = contact.UpdateScorecardResult(ctx, event.ContactAttemptID, &contact.UpdateScorecardReq{
		ScorecardResult: json.RawMessage(resultJSON),
	})
	if err != nil {
		rlog.Error("failed to update scorecard result",
			"service", "scoring",
			"contact_attempt_id", event.ContactAttemptID,
			"err", err)
		return err
	}

	rlog.Info("interaction scored",
		"service", "scoring",
		"contact_attempt_id", event.ContactAttemptID,
		"total_score", result.TotalScore,
		"max_score", result.MaxScore,
		"required_passed", result.RequiredPassed,
		"correlation_id", event.CorrelationID)
	return nil
}
