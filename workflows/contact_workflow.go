package workflows

import (
	"encoding/json"
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// ContactWorkflow orchestrates a contact attempt through compliance check,
// PII sanitization, delivery simulation, result recording, scoring, and event publishing.
func ContactWorkflow(ctx workflow.Context, input ContactWorkflowInput) (ContactWorkflowResult, error) {
	activityOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
			InitialInterval: 1 * time.Second,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, activityOpts)

	var activities *Activities

	// Step 1: Pre-contact compliance check.
	checkInput := ComplianceCheckInput{
		ConsumerID:              input.ConsumerID,
		Channel:                 input.Channel,
		Timezone:                input.ConsumerTimezone,
		ConsentStatus:           input.ConsumerConsent,
		DoNotContact:            input.DoNotContact,
		AttorneyOnFile:          input.AttorneyOnFile,
		RecentContactTimestamps: input.RecentContactTimestamps,
		MessageContent:          input.MessageContent,
	}

	var checkResult ComplianceCheckOutput
	err := workflow.ExecuteActivity(ctx, activities.CheckCompliance, checkInput).Get(ctx, &checkResult)
	if err != nil {
		return ContactWorkflowResult{
			ContactAttemptID: input.ContactAttemptID,
			Status:           "failed",
		}, err
	}

	complianceJSON, err := json.Marshal(checkResult)
	if err != nil {
		return ContactWorkflowResult{ContactAttemptID: input.ContactAttemptID, Status: "failed"}, fmt.Errorf("marshalling compliance result: %w", err)
	}

	// If blocked, record result and publish event, then return early.
	if !checkResult.Allowed {
		blockReason := ""
		if len(checkResult.Violations) > 0 {
			blockReason = checkResult.Violations[0].Rule
		}

		// Record blocked result.
		err = workflow.ExecuteActivity(ctx, activities.RecordContactResult, RecordResultInput{
			ContactAttemptID: input.ContactAttemptID,
			Status:           "blocked",
			ComplianceResult: json.RawMessage(complianceJSON),
			BlockReason:      blockReason,
		}).Get(ctx, nil)
		if err != nil {
			return ContactWorkflowResult{ContactAttemptID: input.ContactAttemptID, Status: "failed"}, err
		}

		// Publish contact-attempted event.
		err = workflow.ExecuteActivity(ctx, activities.PublishContactAttempted, PublishAttemptedInput{
			ContactAttemptID: input.ContactAttemptID,
			ConsumerID:       input.ConsumerID,
			AccountID:        input.AccountID,
			Channel:          input.Channel,
			Status:           "blocked",
			BlockReason:      blockReason,
			CorrelationID:    input.CorrelationID,
		}).Get(ctx, nil)
		if err != nil {
			return ContactWorkflowResult{ContactAttemptID: input.ContactAttemptID, Status: "failed"}, err
		}

		return ContactWorkflowResult{
			ContactAttemptID: input.ContactAttemptID,
			Status:           "blocked",
			Allowed:          false,
			BlockReason:      blockReason,
		}, nil
	}

	// Step 2: Sanitize PII from message content.
	var sanitizeResult SanitizeOutput
	err = workflow.ExecuteActivity(ctx, activities.SanitizePII, SanitizeInput{
		Text: input.MessageContent,
	}).Get(ctx, &sanitizeResult)
	if err != nil {
		return ContactWorkflowResult{ContactAttemptID: input.ContactAttemptID, Status: "failed"}, err
	}

	// Step 3: Simulate delivery.
	var deliveryResult DeliveryResult
	err = workflow.ExecuteActivity(ctx, activities.SimulateDelivery, SimulateDeliveryInput{
		AttemptID: input.ContactAttemptID,
	}).Get(ctx, &deliveryResult)
	if err != nil {
		return ContactWorkflowResult{ContactAttemptID: input.ContactAttemptID, Status: "failed"}, err
	}

	// Step 4: Score interaction using a default rubric.
	var scoreResult ScoreOutput
	err = workflow.ExecuteActivity(ctx, activities.ScoreInteraction, ScoreInput{
		Transcript: sanitizeResult.Sanitized,
		Rubric: ScoreRubric{
			Name: "default-contact-rubric",
			Items: []ScoreItem{
				{
					ID:          "agent-id",
					Description: "Agent identified themselves",
					Required:    true,
					Keywords:    []string{"my name is", "this is", "calling from"},
					Weight:      3,
				},
				{
					ID:          "mini-miranda",
					Description: "Mini-Miranda disclosure",
					Required:    true,
					Keywords:    []string{"attempt to collect a debt", "information will be used"},
					Weight:      4,
				},
				{
					ID:          "payment-option",
					Description: "Payment option offered",
					Required:    false,
					Keywords:    []string{"payment plan", "settle", "arrangement"},
					Weight:      3,
				},
			},
		},
	}).Get(ctx, &scoreResult)
	if err != nil {
		return ContactWorkflowResult{ContactAttemptID: input.ContactAttemptID, Status: "failed"}, err
	}

	scorecardJSON, err := json.Marshal(scoreResult)
	if err != nil {
		return ContactWorkflowResult{ContactAttemptID: input.ContactAttemptID, Status: "failed"}, fmt.Errorf("marshalling scorecard result: %w", err)
	}

	// Step 5: Record contact result with all data.
	status := deliveryResult.Status
	err = workflow.ExecuteActivity(ctx, activities.RecordContactResult, RecordResultInput{
		ContactAttemptID: input.ContactAttemptID,
		Status:           status,
		MessageContent:   sanitizeResult.Sanitized,
		ComplianceResult: json.RawMessage(complianceJSON),
		ScorecardResult:  json.RawMessage(scorecardJSON),
	}).Get(ctx, nil)
	if err != nil {
		return ContactWorkflowResult{ContactAttemptID: input.ContactAttemptID, Status: "failed"}, err
	}

	// Step 6: Publish contact-attempted event.
	err = workflow.ExecuteActivity(ctx, activities.PublishContactAttempted, PublishAttemptedInput{
		ContactAttemptID: input.ContactAttemptID,
		ConsumerID:       input.ConsumerID,
		AccountID:        input.AccountID,
		Channel:          input.Channel,
		Status:           status,
		CorrelationID:    input.CorrelationID,
	}).Get(ctx, nil)
	if err != nil {
		return ContactWorkflowResult{ContactAttemptID: input.ContactAttemptID, Status: "failed"}, err
	}

	// Step 7: Publish interaction-created event.
	err = workflow.ExecuteActivity(ctx, activities.PublishInteractionCreated, PublishInteractionInput{
		ContactAttemptID: input.ContactAttemptID,
		ConsumerID:       input.ConsumerID,
		AccountID:        input.AccountID,
		Channel:          input.Channel,
		SanitizedContent: sanitizeResult.Sanitized,
		ScorecardResult:  json.RawMessage(scorecardJSON),
		CorrelationID:    input.CorrelationID,
	}).Get(ctx, nil)
	if err != nil {
		return ContactWorkflowResult{ContactAttemptID: input.ContactAttemptID, Status: "failed"}, err
	}

	return ContactWorkflowResult{
		ContactAttemptID: input.ContactAttemptID,
		Status:           status,
		Allowed:          true,
		MessageContent:   sanitizeResult.Sanitized,
	}, nil
}
