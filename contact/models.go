package contact

import (
	"encoding/json"
	"time"

	"compliance-platform/internal/domain"
)

// ContactAttempt is the full contact attempt record from the database.
type ContactAttempt struct {
	ID               int64           `json:"id"`
	ConsumerID       int64           `json:"consumer_id"`
	AccountID        int64           `json:"account_id"`
	Channel          domain.Channel  `json:"channel"`
	Status           string          `json:"status"`
	BlockReason      string          `json:"block_reason,omitempty"`
	WorkflowID       string          `json:"workflow_id,omitempty"`
	MessageContent   string          `json:"message_content,omitempty"`
	ComplianceResult json.RawMessage `json:"compliance_result,omitempty"`
	ScorecardResult  json.RawMessage `json:"scorecard_result,omitempty"`
	AttemptedAt      time.Time       `json:"attempted_at"`
	CompletedAt      *time.Time      `json:"completed_at,omitempty"`
}

// InitiateContactReq is the request body for POST /contact/initiate.
type InitiateContactReq struct {
	ConsumerID     int64          `json:"consumer_id"`
	AccountID      int64          `json:"account_id"`
	Channel        domain.Channel `json:"channel"`
	MessageContent string         `json:"message_content"`
}

// InitiateContactResp is the response from POST /contact/initiate.
type InitiateContactResp struct {
	ContactAttemptID int64  `json:"contact_attempt_id"`
	WorkflowID       string `json:"workflow_id"`
}

// ContactList wraps a slice of contact attempts for list endpoints.
type ContactList struct {
	Contacts []*ContactAttempt `json:"contacts"`
}

// UpdateContactResultReq is the request body for updating a contact attempt with results.
// Used by the Temporal worker callback.
type UpdateContactResultReq struct {
	Status           string          `json:"status"`
	MessageContent   string          `json:"message_content,omitempty"`
	ComplianceResult json.RawMessage `json:"compliance_result,omitempty"`
	ScorecardResult  json.RawMessage `json:"scorecard_result,omitempty"`
	BlockReason      string          `json:"block_reason,omitempty"`
}

// PublishContactAttemptedReq is the request body for internal publish endpoint.
type PublishContactAttemptedReq struct {
	ContactAttemptID int64          `json:"contact_attempt_id"`
	ConsumerID       int64          `json:"consumer_id"`
	AccountID        int64          `json:"account_id"`
	Channel          domain.Channel `json:"channel"`
	Status           string         `json:"status"`
	BlockReason      string         `json:"block_reason,omitempty"`
	CorrelationID    string         `json:"correlation_id,omitempty"`
}

// UpdateScorecardReq is the request body for PATCH /contact/attempts/:id/scorecard.
type UpdateScorecardReq struct {
	ScorecardResult json.RawMessage `json:"scorecard_result"`
}

// PublishInteractionCreatedReq is the request body for internal publish endpoint.
type PublishInteractionCreatedReq struct {
	ContactAttemptID int64           `json:"contact_attempt_id"`
	ConsumerID       int64           `json:"consumer_id"`
	AccountID        int64           `json:"account_id"`
	Channel          domain.Channel  `json:"channel"`
	SanitizedContent string          `json:"sanitized_content"`
	ScorecardResult  json.RawMessage `json:"scorecard_result,omitempty"`
	CorrelationID    string          `json:"correlation_id,omitempty"`
}
