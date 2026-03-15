package consumer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"encore.dev/beta/errs"
	"encore.dev/metrics"
	"encore.dev/rlog"
	"encore.dev/storage/sqldb"

	"compliance-platform/internal/domain"
)

// db is the Encore-managed PostgreSQL database for the consumer service.
var db = sqldb.NewDatabase("consumer", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})

// --- Metrics ---
// Defined here per the observability spec in TD.md.

// consentRevocations counts successful consent revocations.
// Spikes are a leading indicator of compliance problems upstream.
var consentRevocations = metrics.NewCounter[uint64]("consent_revocation_total", metrics.CounterConfig{})

// consentPublishErrors counts failures to publish the consent-changed event.
// Any value > 0 requires immediate investigation — pending contacts may not be cancelled.
var consentPublishErrors = metrics.NewCounter[uint64]("consent_event_publish_error_total", metrics.CounterConfig{})

// Service is the Encore service for consumer management.
//
//encore:service
type Service struct{}

// CreateConsumer creates a new consumer record.
//
//encore:api public method=POST path=/consumers
func (s *Service) CreateConsumer(ctx context.Context, req *CreateConsumerReq) (*Consumer, error) {
	if req.ExternalID == "" {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "external_id is required"}
	}
	if req.FirstName == "" {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "first_name is required"}
	}
	if req.LastName == "" {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "last_name is required"}
	}
	if req.Timezone == "" {
		req.Timezone = "America/New_York"
	}

	// Store NULL rather than empty string for optional contact fields.
	var phone, email *string
	if req.Phone != "" {
		phone = &req.Phone
	}
	if req.Email != "" {
		email = &req.Email
	}

	row := db.QueryRow(ctx, `
		INSERT INTO consumers (external_id, first_name, last_name, phone, email, timezone)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, external_id, first_name, last_name, phone, email, timezone,
		          consent_status, do_not_contact, attorney_on_file, created_at, updated_at
	`, req.ExternalID, req.FirstName, req.LastName, phone, email, req.Timezone)

	c, err := scanConsumer(row)
	if err != nil {
		rlog.Error("failed to create consumer",
			"service", "consumer",
			"external_id", req.ExternalID,
			"err", err)
		return nil, fmt.Errorf("creating consumer: %w", err)
	}

	rlog.Info("consumer created",
		"service", "consumer",
		"id", c.ID,
		"external_id", c.ExternalID)

	// Publish lifecycle event for audit trail.
	if eventData, err := json.Marshal(c); err == nil {
		if _, pubErr := ConsumerLifecycle.Publish(ctx, &ConsumerLifecycleEvent{
			ConsumerID: c.ID,
			Action:     "created",
			NewValue:   json.RawMessage(eventData),
		}); pubErr != nil {
			rlog.Error("consumer-lifecycle event publish failed",
				"service", "consumer",
				"consumer_id", c.ID,
				"err", pubErr)
		}
	}

	return c, nil
}

// GetConsumer retrieves a consumer by internal ID.
//
//encore:api public method=GET path=/consumers/:id
func (s *Service) GetConsumer(ctx context.Context, id int64) (*Consumer, error) {
	rlog.Debug("consumer lookup", "service", "consumer", "id", id)

	row := db.QueryRow(ctx, `
		SELECT id, external_id, first_name, last_name, phone, email, timezone,
		       consent_status, do_not_contact, attorney_on_file, created_at, updated_at
		FROM consumers WHERE id = $1
	`, id)

	c, err := scanConsumer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &errs.Error{Code: errs.NotFound, Message: fmt.Sprintf("consumer %d not found", id)}
	}
	if err != nil {
		return nil, fmt.Errorf("fetching consumer %d: %w", id, err)
	}
	return c, nil
}

// UpdateConsent updates a consumer's consent status.
// When consent is revoked, a consent-changed event is published.
// If the event publish fails the error is returned — the caller should retry,
// as the DB has already been updated and a retry is idempotent.
//
//encore:api public method=PUT path=/consumers/:id/consent
func (s *Service) UpdateConsent(ctx context.Context, id int64, req *UpdateConsentReq) (*Consumer, error) {
	if !req.ConsentStatus.Valid() {
		return nil, &errs.Error{
			Code:    errs.InvalidArgument,
			Message: "consent_status must be 'granted' or 'revoked'",
		}
	}

	row := db.QueryRow(ctx, `
		UPDATE consumers
		SET consent_status = $1, updated_at = now()
		WHERE id = $2
		RETURNING id, external_id, first_name, last_name, phone, email, timezone,
		          consent_status, do_not_contact, attorney_on_file, created_at, updated_at
	`, string(req.ConsentStatus), id)

	c, err := scanConsumer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &errs.Error{Code: errs.NotFound, Message: fmt.Sprintf("consumer %d not found", id)}
	}
	if err != nil {
		return nil, fmt.Errorf("updating consent for consumer %d: %w", id, err)
	}

	// Publish consent-changed event on both grant and revoke.
	_, pubErr := ConsentChanged.Publish(ctx, &ConsentChangedEvent{
		ConsumerID:    c.ID,
		ConsentStatus: c.ConsentStatus,
		ChangedAt:     time.Now().UTC().Format(time.RFC3339),
	})
	if pubErr != nil {
		// The DB row is already updated. Return an error so the caller retries;
		// a retry is safe because the UPDATE is idempotent and Publish is at-least-once.
		consentPublishErrors.Increment()
		rlog.Error("consent-changed event publish failed — caller should retry",
			"service", "consumer",
			"consumer_id", c.ID,
			"err", pubErr)
		return nil, fmt.Errorf("consent updated but event publish failed: %w", pubErr)
	}

	if req.ConsentStatus == domain.ConsentRevoked {
		consentRevocations.Increment()
		rlog.Info("consent revoked, event published",
			"service", "consumer",
			"consumer_id", c.ID)
	} else {
		rlog.Info("consent granted, event published",
			"service", "consumer",
			"consumer_id", c.ID)
	}

	return c, nil
}

// scanConsumer reads a Consumer from any row-like value.
// Handles nullable phone/email via sql.NullString.
func scanConsumer(s domain.Scanner) (*Consumer, error) {
	var c Consumer
	var dbPhone, dbEmail sql.NullString
	if err := s.Scan(
		&c.ID, &c.ExternalID, &c.FirstName, &c.LastName, &dbPhone, &dbEmail, &c.Timezone,
		&c.ConsentStatus, &c.DoNotContact, &c.AttorneyOnFile, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	c.Phone = dbPhone.String
	c.Email = dbEmail.String
	return &c, nil
}
