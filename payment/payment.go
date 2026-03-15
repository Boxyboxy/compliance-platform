package payment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"encore.dev/beta/errs"
	"encore.dev/rlog"
	"encore.dev/storage/sqldb"

	"compliance-platform/internal/domain"
)

// publishPaymentEvent publishes a payment-updated event, logging on failure.
// This is fire-and-forget; publish errors do not propagate to the caller.
func publishPaymentEvent(ctx context.Context, planID, accountID int64, eventType string, amount float64) {
	event := &PaymentUpdatedEvent{
		PlanID:     planID,
		AccountID:  accountID,
		EventType:  eventType,
		OccurredAt: time.Now(),
	}
	if amount > 0 {
		event.Amount = amount
	}
	if _, err := PaymentUpdated.Publish(ctx, event); err != nil {
		rlog.Error("payment-updated event publish failed",
			"service", "payment",
			"id", planID,
			"event_type", eventType,
			"err", err)
	}
}

// completeIfFullyPaid checks whether total payments reach the plan's total amount
// within the given transaction. If so, it marks the plan as completed and records
// the completion event. Returns true if the plan was completed.
func completeIfFullyPaid(ctx context.Context, tx *sqldb.Tx, planID int64, totalAmount float64) (bool, error) {
	var totalPaid float64
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0) FROM payment_events
		WHERE plan_id = $1 AND event_type = 'payment_received'
	`, planID).Scan(&totalPaid)
	if err != nil {
		return false, fmt.Errorf("summing payments for plan %d: %w", planID, err)
	}

	if totalPaid < totalAmount {
		return false, nil
	}

	_, err = tx.Exec(ctx, `
		UPDATE payment_plans SET status = 'completed', completed_at = now() WHERE id = $1
	`, planID)
	if err != nil {
		return false, fmt.Errorf("completing plan %d: %w", planID, err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO payment_events (plan_id, event_type) VALUES ($1, 'completed')
	`, planID)
	if err != nil {
		return false, fmt.Errorf("recording completed event: %w", err)
	}

	return true, nil
}

// db is the Encore-managed PostgreSQL database for the payment service.
var db = sqldb.NewDatabase("payment", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})

// Service is the Encore service for payment plan management.
//
//encore:service
type Service struct{}

// ProposePlan creates a new payment plan in "proposed" status.
//
//encore:api public method=POST path=/payment-plans
func (s *Service) ProposePlan(ctx context.Context, req *ProposePlanReq) (*PaymentPlan, error) {
	if req.AccountID == 0 {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "account_id is required"}
	}
	if req.TotalAmount <= 0 {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "total_amount must be positive"}
	}
	if req.NumInstallments <= 0 {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "num_installments must be positive"}
	}
	if req.InstallmentAmt <= 0 {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "installment_amt must be positive"}
	}

	freq := req.Frequency
	if freq == "" {
		freq = "monthly"
	}
	if freq != "weekly" && freq != "biweekly" && freq != "monthly" {
		return nil, &errs.Error{
			Code:    errs.InvalidArgument,
			Message: fmt.Sprintf("invalid frequency %q; must be one of: weekly, biweekly, monthly", freq),
		}
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRow(ctx, `
		INSERT INTO payment_plans
		    (account_id, total_amount, num_installments, installment_amt, frequency, status)
		VALUES ($1, $2, $3, $4, $5, 'proposed')
		RETURNING id, account_id, total_amount, num_installments, installment_amt,
		          frequency, status, proposed_at, accepted_at, completed_at
	`, req.AccountID, req.TotalAmount, req.NumInstallments, req.InstallmentAmt, freq)

	p, err := scanPlan(row)
	if err != nil {
		rlog.Error("failed to create payment plan",
			"service", "payment",
			"account_id", req.AccountID,
			"err", err)
		return nil, fmt.Errorf("creating payment plan: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO payment_events (plan_id, event_type) VALUES ($1, 'proposed')
	`, p.ID)
	if err != nil {
		return nil, fmt.Errorf("recording proposed event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	rlog.Info("payment plan proposed",
		"service", "payment",
		"id", p.ID,
		"account_id", p.AccountID)

	publishPaymentEvent(ctx, p.ID, p.AccountID, "proposed", 0)

	return p, nil
}

// AcceptPlan transitions a plan from "proposed" to "accepted".
//
//encore:api public method=PATCH path=/payment-plans/:id/accept
func (s *Service) AcceptPlan(ctx context.Context, id int64) (*PaymentPlan, error) {
	var currentStatus string
	err := db.QueryRow(ctx, `SELECT status FROM payment_plans WHERE id = $1`, id).Scan(&currentStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &errs.Error{Code: errs.NotFound, Message: fmt.Sprintf("payment plan %d not found", id)}
	}
	if err != nil {
		return nil, fmt.Errorf("fetching payment plan %d: %w", id, err)
	}
	if currentStatus != "proposed" {
		return nil, &errs.Error{
			Code:    errs.InvalidArgument,
			Message: fmt.Sprintf("plan %d has status %q; only proposed plans can be accepted", id, currentStatus),
		}
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRow(ctx, `
		UPDATE payment_plans
		SET status = 'accepted', accepted_at = now()
		WHERE id = $1
		RETURNING id, account_id, total_amount, num_installments, installment_amt,
		          frequency, status, proposed_at, accepted_at, completed_at
	`, id)

	p, err := scanPlan(row)
	if err != nil {
		return nil, fmt.Errorf("accepting payment plan %d: %w", id, err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO payment_events (plan_id, event_type) VALUES ($1, 'accepted')
	`, p.ID)
	if err != nil {
		return nil, fmt.Errorf("recording accepted event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	rlog.Info("payment plan accepted",
		"service", "payment",
		"id", p.ID,
		"account_id", p.AccountID)

	publishPaymentEvent(ctx, p.ID, p.AccountID, "accepted", 0)

	return p, nil
}

// RecordPayment records a payment against a plan. Transitions accepted→active on first payment,
// and marks completed when the sum of payments reaches total_amount.
//
//encore:api public method=POST path=/payment-plans/:id/payments
func (s *Service) RecordPayment(ctx context.Context, id int64, req *RecordPaymentReq) (*PaymentEvent, error) {
	if req.Amount <= 0 {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "amount must be positive"}
	}

	var p PaymentPlan
	err := db.QueryRow(ctx, `
		SELECT id, account_id, total_amount, num_installments, installment_amt,
		       frequency, status, proposed_at, accepted_at, completed_at
		FROM payment_plans WHERE id = $1
	`, id).Scan(
		&p.ID, &p.AccountID, &p.TotalAmount, &p.NumInstallments, &p.InstallmentAmt,
		&p.Frequency, &p.Status, &p.ProposedAt, &p.AcceptedAt, &p.CompletedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &errs.Error{Code: errs.NotFound, Message: fmt.Sprintf("payment plan %d not found", id)}
	}
	if err != nil {
		return nil, fmt.Errorf("fetching payment plan %d: %w", id, err)
	}

	if p.Status != "accepted" && p.Status != "active" {
		return nil, &errs.Error{
			Code:    errs.InvalidArgument,
			Message: fmt.Sprintf("plan %d has status %q; payments can only be recorded on accepted or active plans", id, p.Status),
		}
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// Transition accepted → active on first payment.
	if p.Status == "accepted" {
		_, err = tx.Exec(ctx, `UPDATE payment_plans SET status = 'active' WHERE id = $1`, id)
		if err != nil {
			return nil, fmt.Errorf("activating plan %d: %w", id, err)
		}
	}

	// Record the payment event.
	evRow := tx.QueryRow(ctx, `
		INSERT INTO payment_events (plan_id, event_type, amount)
		VALUES ($1, 'payment_received', $2)
		RETURNING id, plan_id, event_type, amount, occurred_at, metadata
	`, id, req.Amount)

	ev, err := scanEvent(evRow)
	if err != nil {
		return nil, fmt.Errorf("recording payment event: %w", err)
	}

	completed, err := completeIfFullyPaid(ctx, tx, id, p.TotalAmount)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	rlog.Info("payment recorded",
		"service", "payment",
		"id", p.ID,
		"account_id", p.AccountID)

	if p.Status == "accepted" {
		publishPaymentEvent(ctx, p.ID, p.AccountID, "active", 0)
	}
	publishPaymentEvent(ctx, p.ID, p.AccountID, "payment_received", req.Amount)
	if completed {
		publishPaymentEvent(ctx, p.ID, p.AccountID, "completed", 0)
	}

	return ev, nil
}

// GetPlan retrieves a payment plan by its ID.
//
//encore:api public method=GET path=/payment-plans/:id
func (s *Service) GetPlan(ctx context.Context, id int64) (*PaymentPlan, error) {
	rlog.Debug("payment plan lookup", "service", "payment", "id", id)

	row := db.QueryRow(ctx, `
		SELECT id, account_id, total_amount, num_installments, installment_amt,
		       frequency, status, proposed_at, accepted_at, completed_at
		FROM payment_plans WHERE id = $1
	`, id)

	p, err := scanPlan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &errs.Error{Code: errs.NotFound, Message: fmt.Sprintf("payment plan %d not found", id)}
	}
	if err != nil {
		return nil, fmt.Errorf("fetching payment plan %d: %w", id, err)
	}
	return p, nil
}

// MarkDefaulted transitions a plan to "defaulted" status. Called by the Temporal workflow.
//
//encore:api private method=PATCH path=/payment-plans/:id/default
func (s *Service) MarkDefaulted(ctx context.Context, id int64) (*PaymentPlan, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRow(ctx, `
		UPDATE payment_plans
		SET status = 'defaulted', completed_at = now()
		WHERE id = $1
		RETURNING id, account_id, total_amount, num_installments, installment_amt,
		          frequency, status, proposed_at, accepted_at, completed_at
	`, id)

	p, err := scanPlan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &errs.Error{Code: errs.NotFound, Message: fmt.Sprintf("payment plan %d not found", id)}
	}
	if err != nil {
		return nil, fmt.Errorf("marking plan %d as defaulted: %w", id, err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO payment_events (plan_id, event_type) VALUES ($1, 'defaulted')
	`, p.ID)
	if err != nil {
		return nil, fmt.Errorf("recording defaulted event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	rlog.Info("payment plan defaulted",
		"service", "payment",
		"id", p.ID,
		"account_id", p.AccountID)

	publishPaymentEvent(ctx, p.ID, p.AccountID, "defaulted", 0)

	return p, nil
}

// MarkCompleted transitions a plan to "completed" status. Called by the Temporal workflow.
//
//encore:api private method=PATCH path=/payment-plans/:id/complete
func (s *Service) MarkCompleted(ctx context.Context, id int64) (*PaymentPlan, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRow(ctx, `
		UPDATE payment_plans
		SET status = 'completed', completed_at = now()
		WHERE id = $1
		RETURNING id, account_id, total_amount, num_installments, installment_amt,
		          frequency, status, proposed_at, accepted_at, completed_at
	`, id)

	p, err := scanPlan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &errs.Error{Code: errs.NotFound, Message: fmt.Sprintf("payment plan %d not found", id)}
	}
	if err != nil {
		return nil, fmt.Errorf("marking plan %d as completed: %w", id, err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO payment_events (plan_id, event_type) VALUES ($1, 'completed')
	`, p.ID)
	if err != nil {
		return nil, fmt.Errorf("recording completed event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	rlog.Info("payment plan completed",
		"service", "payment",
		"id", p.ID,
		"account_id", p.AccountID)

	publishPaymentEvent(ctx, p.ID, p.AccountID, "completed", 0)

	return p, nil
}

// scanPlan reads a PaymentPlan from any row-like value.
func scanPlan(s domain.Scanner) (*PaymentPlan, error) {
	var p PaymentPlan
	if err := s.Scan(
		&p.ID, &p.AccountID, &p.TotalAmount, &p.NumInstallments, &p.InstallmentAmt,
		&p.Frequency, &p.Status, &p.ProposedAt, &p.AcceptedAt, &p.CompletedAt,
	); err != nil {
		return nil, err
	}
	return &p, nil
}

// scanEvent reads a PaymentEvent from any row-like value.
func scanEvent(s domain.Scanner) (*PaymentEvent, error) {
	var e PaymentEvent
	if err := s.Scan(
		&e.ID, &e.PlanID, &e.EventType, &e.Amount, &e.OccurredAt, &e.Metadata,
	); err != nil {
		return nil, err
	}
	return &e, nil
}
