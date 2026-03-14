package contact

import (
	"encoding/json"
	"time"

	"encore.dev/pubsub"

	"compliance-platform/internal/domain"
)

// ContactAttemptedEvent is published when a contact attempt is recorded.
type ContactAttemptedEvent struct {
	ContactAttemptID int64          `json:"contact_attempt_id"`
	ConsumerID       int64          `json:"consumer_id"`
	AccountID        int64          `json:"account_id"`
	Channel          domain.Channel `json:"channel"`
	Status           string         `json:"status"`
	BlockReason      string         `json:"block_reason,omitempty"`
	CorrelationID    string         `json:"correlation_id,omitempty"`
	Timestamp        time.Time      `json:"timestamp"`
}

// InteractionCreatedEvent is published when a contact interaction is completed.
type InteractionCreatedEvent struct {
	ContactAttemptID int64           `json:"contact_attempt_id"`
	ConsumerID       int64           `json:"consumer_id"`
	AccountID        int64           `json:"account_id"`
	Channel          domain.Channel  `json:"channel"`
	SanitizedContent string          `json:"sanitized_content"`
	ScorecardResult  json.RawMessage `json:"scorecard_result,omitempty"`
	CorrelationID    string          `json:"correlation_id,omitempty"`
	Timestamp        time.Time       `json:"timestamp"`
}

// ContactAttempted is published whenever a contact attempt is recorded (allowed or blocked).
var ContactAttempted = pubsub.NewTopic[*ContactAttemptedEvent]("contact-attempted", pubsub.TopicConfig{
	DeliveryGuarantee: pubsub.AtLeastOnce,
})

// InteractionCreated is published when a contact interaction is completed and scored.
var InteractionCreated = pubsub.NewTopic[*InteractionCreatedEvent]("interaction-created", pubsub.TopicConfig{
	DeliveryGuarantee: pubsub.AtLeastOnce,
})
