package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"encore.dev/beta/errs"
	"encore.dev/metrics"
	"encore.dev/rlog"
	"encore.dev/storage/sqldb"

	"compliance-platform/internal/domain"
)

// db is the Encore-managed PostgreSQL database for the account service.
var db = sqldb.NewDatabase("account", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})

// --- Metrics ---

// statusTransitionLabels labels account status transition counters.
type statusTransitionLabels struct {
	From string
	To   string
}

// accountStatusTransitions counts status updates, labelled by the from→to pair.
// Used to observe the distribution of lifecycle transitions (e.g. how often
// accounts flow current→delinquent vs delinquent→charged_off).
var accountStatusTransitions = metrics.NewCounterGroup[statusTransitionLabels, uint64](
	"account_status_transition_total",
	metrics.CounterConfig{},
)

// Service is the Encore service for account management.
//
//encore:service
type Service struct{}

// CreateAccount creates a new account linked to an existing consumer.
//
//encore:api public method=POST path=/accounts
func (s *Service) CreateAccount(ctx context.Context, req *CreateAccountReq) (*Account, error) {
	if req.ConsumerID == 0 {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "consumer_id is required"}
	}
	if req.OriginalCreditor == "" {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "original_creditor is required"}
	}
	if req.AccountNumber == "" {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "account_number is required"}
	}
	if req.BalanceDue < 0 {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "balance_due must be non-negative"}
	}

	status := req.Status
	if status == "" {
		status = domain.AccountStatusCurrent
	}
	if !status.Valid() {
		return nil, &errs.Error{
			Code:    errs.InvalidArgument,
			Message: fmt.Sprintf("invalid status %q; must be one of: current, delinquent, charged_off, settled, closed", status),
		}
	}

	row := db.QueryRow(ctx, `
		INSERT INTO accounts
		    (consumer_id, original_creditor, account_number, balance_due, days_past_due, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, consumer_id, original_creditor, account_number,
		          balance_due, days_past_due, status, created_at, updated_at
	`, req.ConsumerID, req.OriginalCreditor, req.AccountNumber,
		req.BalanceDue, req.DaysPastDue, string(status))

	a, err := scanAccount(row)
	if err != nil {
		// Do not log account_number — it is GLBA-regulated PII.
		rlog.Error("failed to create account",
			"service", "account",
			"consumer_id", req.ConsumerID,
			"err", err)
		return nil, fmt.Errorf("creating account: %w", err)
	}

	rlog.Info("account created",
		"service", "account",
		"id", a.ID,
		"consumer_id", a.ConsumerID)

	// Publish lifecycle event for audit trail.
	if eventData, err := json.Marshal(a); err == nil {
		if _, pubErr := AccountLifecycle.Publish(ctx, &AccountLifecycleEvent{
			AccountID:  a.ID,
			ConsumerID: a.ConsumerID,
			Action:     "created",
			NewValue:   json.RawMessage(eventData),
		}); pubErr != nil {
			rlog.Error("account-lifecycle event publish failed",
				"service", "account",
				"account_id", a.ID,
				"err", pubErr)
		}
	}

	return a, nil
}

// GetAccount retrieves an account by its internal ID.
//
//encore:api public method=GET path=/accounts/:id
func (s *Service) GetAccount(ctx context.Context, id int64) (*Account, error) {
	rlog.Debug("account lookup", "service", "account", "id", id)

	row := db.QueryRow(ctx, `
		SELECT id, consumer_id, original_creditor, account_number,
		       balance_due, days_past_due, status, created_at, updated_at
		FROM accounts WHERE id = $1
	`, id)

	a, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &errs.Error{Code: errs.NotFound, Message: fmt.Sprintf("account %d not found", id)}
	}
	if err != nil {
		return nil, fmt.Errorf("fetching account %d: %w", id, err)
	}
	return a, nil
}

// ListAccountsByConsumer returns all accounts for a given consumer.
//
//encore:api public method=GET path=/consumers/:consumerId/accounts
func (s *Service) ListAccountsByConsumer(ctx context.Context, consumerId int64) (*AccountList, error) {
	rlog.Debug("listing accounts", "service", "account", "consumer_id", consumerId)

	rows, err := db.Query(ctx, `
		SELECT id, consumer_id, original_creditor, account_number,
		       balance_due, days_past_due, status, created_at, updated_at
		FROM accounts
		WHERE consumer_id = $1
		ORDER BY created_at DESC
	`, consumerId)
	if err != nil {
		return nil, fmt.Errorf("listing accounts for consumer %d: %w", consumerId, err)
	}
	defer rows.Close()

	var accounts []*Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning account row: %w", err)
		}
		accounts = append(accounts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating accounts: %w", err)
	}

	// Return an empty slice rather than nil so the JSON response is [] not null.
	if accounts == nil {
		accounts = []*Account{}
	}
	return &AccountList{Accounts: accounts}, nil
}

// UpdateAccountStatus updates the status of an account.
// The previous status is captured atomically (CTE) so the transition metric
// reflects the exact state at the time of the update, even under concurrency.
//
//encore:api public method=PATCH path=/accounts/:id/status
func (s *Service) UpdateAccountStatus(ctx context.Context, id int64, req *UpdateStatusReq) (*Account, error) {
	if !req.Status.Valid() {
		return nil, &errs.Error{
			Code:    errs.InvalidArgument,
			Message: fmt.Sprintf("invalid status %q; must be one of: current, delinquent, charged_off, settled, closed", req.Status),
		}
	}

	// The CTE captures the previous status in the same statement as the UPDATE,
	// avoiding a separate SELECT round-trip and preventing a TOCTOU race.
	var a Account
	var prevStatus string
	err := db.QueryRow(ctx, `
		WITH prev AS (SELECT status FROM accounts WHERE id = $2)
		UPDATE accounts
		SET status = $1, updated_at = now()
		WHERE id = $2
		RETURNING id, consumer_id, original_creditor, account_number,
		          balance_due, days_past_due, status, created_at, updated_at,
		          (SELECT status FROM prev)
	`, string(req.Status), id).Scan(
		&a.ID, &a.ConsumerID, &a.OriginalCreditor, &a.AccountNumber,
		&a.BalanceDue, &a.DaysPastDue, &a.Status, &a.CreatedAt, &a.UpdatedAt,
		&prevStatus,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &errs.Error{Code: errs.NotFound, Message: fmt.Sprintf("account %d not found", id)}
	}
	if err != nil {
		return nil, fmt.Errorf("updating status for account %d: %w", id, err)
	}

	accountStatusTransitions.With(statusTransitionLabels{From: prevStatus, To: string(a.Status)}).Increment()
	rlog.Info("account status updated",
		"service", "account",
		"id", a.ID,
		"from", prevStatus,
		"to", a.Status)

	// Publish lifecycle event for audit trail.
	oldVal, _ := json.Marshal(map[string]string{"status": prevStatus})
	newVal, _ := json.Marshal(map[string]string{"status": string(a.Status)})
	if _, pubErr := AccountLifecycle.Publish(ctx, &AccountLifecycleEvent{
		AccountID:  a.ID,
		ConsumerID: a.ConsumerID,
		Action:     "status_updated",
		OldValue:   json.RawMessage(oldVal),
		NewValue:   json.RawMessage(newVal),
	}); pubErr != nil {
		rlog.Error("account-lifecycle event publish failed",
			"service", "account",
			"account_id", a.ID,
			"err", pubErr)
	}

	return &a, nil
}

// scanAccount reads an Account from any row-like value.
func scanAccount(s domain.Scanner) (*Account, error) {
	var a Account
	if err := s.Scan(
		&a.ID, &a.ConsumerID, &a.OriginalCreditor, &a.AccountNumber,
		&a.BalanceDue, &a.DaysPastDue, &a.Status, &a.CreatedAt, &a.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &a, nil
}
