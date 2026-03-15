package contact

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"encore.dev/beta/errs"
	"encore.dev/metrics"
	"encore.dev/rlog"
	"encore.dev/storage/sqldb"

	"compliance-platform/account"
	"compliance-platform/consumer"
	"compliance-platform/internal/domain"
	"compliance-platform/workflows"

	"go.temporal.io/sdk/client"
)

// db is the Encore-managed PostgreSQL database for the contact service.
var db = sqldb.NewDatabase("contact", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})

// --- Metrics ---

type contactAttemptLabels struct {
	Channel string
	Outcome string
}

var contactAttemptTotal = metrics.NewCounterGroup[contactAttemptLabels, uint64](
	"contact_attempt_total",
	metrics.CounterConfig{},
)

var contactWorkflowDuration = metrics.NewGauge[int64]("contact_workflow_duration_ms", metrics.GaugeConfig{})

// --- Lazy Temporal client ---

var (
	temporalOnce   sync.Once
	temporalClient client.Client
	temporalErr    error
)

func getTemporalClient() (client.Client, error) {
	temporalOnce.Do(func() {
		temporalClient, temporalErr = client.Dial(client.Options{})
	})
	return temporalClient, temporalErr
}

// Service is the Encore service for contact management.
//
//encore:service
type Service struct{}

// InitiateContact starts a contact workflow for a consumer.
//
//encore:api public method=POST path=/contact/initiate
func (s *Service) InitiateContact(ctx context.Context, req *InitiateContactReq) (*InitiateContactResp, error) {
	if req.ConsumerID == 0 {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "consumer_id is required"}
	}
	if req.AccountID == 0 {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "account_id is required"}
	}
	if !req.Channel.Valid() {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "channel must be sms, email, or voice"}
	}
	if req.MessageContent == "" {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "message_content is required"}
	}

	// Cross-service calls to fetch consumer and account state.
	c, err := consumer.GetConsumer(ctx, req.ConsumerID)
	if err != nil {
		return nil, fmt.Errorf("fetching consumer %d: %w", req.ConsumerID, err)
	}

	a, err := account.GetAccount(ctx, req.AccountID)
	if err != nil {
		return nil, fmt.Errorf("fetching account %d: %w", req.AccountID, err)
	}
	if a.ConsumerID != req.ConsumerID {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "account does not belong to consumer"}
	}

	attemptID, err := insertPendingAttempt(ctx, req)
	if err != nil {
		return nil, err
	}

	recentTimestamps, err := fetchRecentContactTimestamps(ctx, req.ConsumerID)
	if err != nil {
		return nil, err
	}

	workflowInput := buildWorkflowInput(attemptID, req, c, recentTimestamps)

	tc, err := getTemporalClient()
	if err != nil {
		rlog.Error("failed to connect to Temporal",
			"service", "contact",
			"err", err)
		return nil, fmt.Errorf("connecting to Temporal: %w", err)
	}

	workflowID := fmt.Sprintf("contact-%d-%d", attemptID, time.Now().UnixMilli())

	run, err := tc.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                       workflowID,
		TaskQueue:                "contact-queue",
		WorkflowExecutionTimeout: 60 * time.Second,
	}, workflows.ContactWorkflow, workflowInput)
	if err != nil {
		rlog.Error("failed to start Temporal workflow",
			"service", "contact",
			"id", attemptID,
			"err", err)
		return nil, fmt.Errorf("starting workflow: %w", err)
	}

	// Update the contact attempt with the workflow ID.
	_, err = db.Exec(ctx, `
		UPDATE contact_attempts SET workflow_id = $1 WHERE id = $2
	`, run.GetID(), attemptID)
	if err != nil {
		rlog.Error("failed to update workflow_id on contact attempt",
			"service", "contact",
			"id", attemptID,
			"err", err)
	}

	contactAttemptTotal.With(contactAttemptLabels{Channel: string(req.Channel), Outcome: "initiated"}).Increment()
	rlog.Info("contact attempt initiated",
		"service", "contact",
		"id", attemptID,
		"consumer_id", req.ConsumerID,
		"workflow_id", run.GetID())

	return &InitiateContactResp{
		ContactAttemptID: attemptID,
		WorkflowID:       run.GetID(),
	}, nil
}

// insertPendingAttempt inserts a new pending contact attempt and returns its ID.
func insertPendingAttempt(ctx context.Context, req *InitiateContactReq) (int64, error) {
	var attemptID int64
	err := db.QueryRow(ctx, `
		INSERT INTO contact_attempts (consumer_id, account_id, channel, message_content)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, req.ConsumerID, req.AccountID, string(req.Channel), req.MessageContent).Scan(&attemptID)
	if err != nil {
		rlog.Error("failed to insert contact attempt",
			"service", "contact",
			"consumer_id", req.ConsumerID,
			"err", err)
		return 0, fmt.Errorf("inserting contact attempt: %w", err)
	}
	return attemptID, nil
}

// fetchRecentContactTimestamps queries contact attempt timestamps within the
// trailing 7-day window for frequency cap evaluation.
func fetchRecentContactTimestamps(ctx context.Context, consumerID int64) ([]string, error) {
	sevenDaysAgo := time.Now().UTC().Add(-7 * 24 * time.Hour)
	rows, err := db.Query(ctx, `
		SELECT attempted_at FROM contact_attempts
		WHERE consumer_id = $1 AND attempted_at >= $2
		ORDER BY attempted_at DESC
	`, consumerID, sevenDaysAgo)
	if err != nil {
		return nil, fmt.Errorf("querying recent contacts: %w", err)
	}
	defer rows.Close()

	var timestamps []string
	for rows.Next() {
		var ts time.Time
		if err := rows.Scan(&ts); err != nil {
			return nil, fmt.Errorf("scanning contact timestamp: %w", err)
		}
		timestamps = append(timestamps, ts.Format(time.RFC3339))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating contact timestamps: %w", err)
	}
	return timestamps, nil
}

// buildWorkflowInput assembles a ContactWorkflowInput from the request,
// consumer state, and recent contact timestamps.
func buildWorkflowInput(
	attemptID int64,
	req *InitiateContactReq,
	c *consumer.Consumer,
	recentTimestamps []string,
) workflows.ContactWorkflowInput {
	return workflows.ContactWorkflowInput{
		ContactAttemptID:        attemptID,
		ConsumerID:              req.ConsumerID,
		AccountID:               req.AccountID,
		Channel:                 string(req.Channel),
		MessageContent:          req.MessageContent,
		ConsumerTimezone:        c.Timezone,
		ConsumerConsent:         string(c.ConsentStatus),
		AttorneyOnFile:          c.AttorneyOnFile,
		DoNotContact:            c.DoNotContact,
		RecentContactTimestamps: recentTimestamps,
	}
}

// ListContacts returns contact attempts for a consumer.
//
//encore:api public method=GET path=/consumers/:consumerId/contacts
func (s *Service) ListContacts(ctx context.Context, consumerId int64) (*ContactList, error) {
	rlog.Debug("listing contacts", "service", "contact", "consumer_id", consumerId)

	rows, err := db.Query(ctx, `
		SELECT id, consumer_id, account_id, channel, status, block_reason,
		       workflow_id, message_content, compliance_result, scorecard_result,
		       attempted_at, completed_at
		FROM contact_attempts
		WHERE consumer_id = $1
		ORDER BY attempted_at DESC
	`, consumerId)
	if err != nil {
		return nil, fmt.Errorf("listing contacts for consumer %d: %w", consumerId, err)
	}
	defer rows.Close()

	var contacts []*ContactAttempt
	for rows.Next() {
		ca, err := scanContactAttempt(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning contact attempt: %w", err)
		}
		contacts = append(contacts, ca)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating contacts: %w", err)
	}

	if contacts == nil {
		contacts = []*ContactAttempt{}
	}
	return &ContactList{Contacts: contacts}, nil
}

// UpdateContactResult updates a contact attempt with workflow results.
// Called by the Temporal worker via HTTP.
//
//encore:api private method=POST path=/contact/attempts/:id/result
func (s *Service) UpdateContactResult(ctx context.Context, id int64, req *UpdateContactResultReq) error {
	completedAt := time.Now().UTC()
	_, err := db.Exec(ctx, `
		UPDATE contact_attempts
		SET status = $1, message_content = COALESCE(NULLIF($2, ''), message_content),
		    compliance_result = COALESCE($3, compliance_result),
		    scorecard_result = COALESCE($4, scorecard_result),
		    block_reason = COALESCE(NULLIF($5, ''), block_reason),
		    completed_at = $6
		WHERE id = $7
	`, req.Status, req.MessageContent, jsonOrNull(req.ComplianceResult),
		jsonOrNull(req.ScorecardResult), req.BlockReason, completedAt, id)
	if err != nil {
		rlog.Error("failed to update contact result",
			"service", "contact",
			"id", id,
			"err", err)
		return fmt.Errorf("updating contact result %d: %w", id, err)
	}

	rlog.Info("contact result updated",
		"service", "contact",
		"id", id,
		"status", req.Status)
	return nil
}

// UpdateScorecardResult updates the scorecard result on a contact attempt.
// Used by the scoring service for async re-scoring after delivery.
//
//encore:api private method=PATCH path=/contact/attempts/:id/scorecard
func (s *Service) UpdateScorecardResult(ctx context.Context, id int64, req *UpdateScorecardReq) error {
	_, err := db.Exec(ctx, `
		UPDATE contact_attempts SET scorecard_result = $1 WHERE id = $2
	`, []byte(req.ScorecardResult), id)
	if err != nil {
		rlog.Error("failed to update scorecard result",
			"service", "contact",
			"id", id,
			"err", err)
		return fmt.Errorf("updating scorecard result %d: %w", id, err)
	}

	rlog.Info("scorecard result updated",
		"service", "contact",
		"id", id)
	return nil
}

// PublishContactAttempted publishes a contact-attempted event.
// Called by the Temporal worker via HTTP since it can't use Encore Pub/Sub directly.
//
//encore:api private method=POST path=/contact/internal/publish-attempted
func (s *Service) PublishContactAttempted(ctx context.Context, req *PublishContactAttemptedReq) error {
	_, err := ContactAttempted.Publish(ctx, &ContactAttemptedEvent{
		ContactAttemptID: req.ContactAttemptID,
		ConsumerID:       req.ConsumerID,
		AccountID:        req.AccountID,
		Channel:          req.Channel,
		Status:           req.Status,
		BlockReason:      req.BlockReason,
		CorrelationID:    req.CorrelationID,
		Timestamp:        time.Now().UTC(),
	})
	if err != nil {
		rlog.Error("failed to publish contact-attempted event",
			"service", "contact",
			"contact_attempt_id", req.ContactAttemptID,
			"err", err)
		return fmt.Errorf("publishing contact-attempted event: %w", err)
	}

	rlog.Info("contact-attempted event published",
		"service", "contact",
		"contact_attempt_id", req.ContactAttemptID)
	return nil
}

// PublishInteractionCreated publishes an interaction-created event.
// Called by the Temporal worker via HTTP since it can't use Encore Pub/Sub directly.
//
//encore:api private method=POST path=/contact/internal/publish-interaction
func (s *Service) PublishInteractionCreated(ctx context.Context, req *PublishInteractionCreatedReq) error {
	_, err := InteractionCreated.Publish(ctx, &InteractionCreatedEvent{
		ContactAttemptID: req.ContactAttemptID,
		ConsumerID:       req.ConsumerID,
		AccountID:        req.AccountID,
		Channel:          req.Channel,
		SanitizedContent: req.SanitizedContent,
		ScorecardResult:  req.ScorecardResult,
		CorrelationID:    req.CorrelationID,
		Timestamp:        time.Now().UTC(),
	})
	if err != nil {
		rlog.Error("failed to publish interaction-created event",
			"service", "contact",
			"contact_attempt_id", req.ContactAttemptID,
			"err", err)
		return fmt.Errorf("publishing interaction-created event: %w", err)
	}

	rlog.Info("interaction-created event published",
		"service", "contact",
		"contact_attempt_id", req.ContactAttemptID)
	return nil
}

func scanContactAttempt(s domain.Scanner) (*ContactAttempt, error) {
	var ca ContactAttempt
	var blockReason, workflowID, messageContent sql.NullString
	var complianceResult, scorecardResult []byte
	var completedAt sql.NullTime

	if err := s.Scan(
		&ca.ID, &ca.ConsumerID, &ca.AccountID, &ca.Channel, &ca.Status,
		&blockReason, &workflowID, &messageContent,
		&complianceResult, &scorecardResult,
		&ca.AttemptedAt, &completedAt,
	); err != nil {
		return nil, err
	}

	ca.BlockReason = blockReason.String
	ca.WorkflowID = workflowID.String
	ca.MessageContent = messageContent.String
	if complianceResult != nil {
		ca.ComplianceResult = json.RawMessage(complianceResult)
	}
	if scorecardResult != nil {
		ca.ScorecardResult = json.RawMessage(scorecardResult)
	}
	if completedAt.Valid {
		ca.CompletedAt = &completedAt.Time
	}
	return &ca, nil
}

// jsonOrNull returns nil if the input is nil or empty, otherwise returns the raw JSON.
func jsonOrNull(data json.RawMessage) interface{} {
	if len(data) == 0 {
		return nil
	}
	return []byte(data)
}
