package payment

import (
	"time"

	"encore.dev/pubsub"
)

// PaymentUpdatedEvent is published when a payment plan changes state.
type PaymentUpdatedEvent struct {
	PlanID        int64     `json:"plan_id"`
	AccountID     int64     `json:"account_id"`
	EventType     string    `json:"event_type"` // proposed, accepted, active, completed, defaulted, payment_received
	Amount        float64   `json:"amount,omitempty"`
	OccurredAt    time.Time `json:"occurred_at"`
	CorrelationID string    `json:"correlation_id,omitempty"`
}

// PaymentUpdated is the Pub/Sub topic for payment plan lifecycle events.
var PaymentUpdated = pubsub.NewTopic[*PaymentUpdatedEvent]("payment-updated", pubsub.TopicConfig{
	DeliveryGuarantee: pubsub.AtLeastOnce,
})
