package payment

import "encore.dev/pubsub"

//encore:service
type Service struct{}

// PaymentUpdatedEvent is published when a payment plan changes state.
type PaymentUpdatedEvent struct {
	PlanID        int64   `json:"plan_id"`
	AccountID     int64   `json:"account_id"`
	EventType     string  `json:"event_type"` // proposed, accepted, active, completed, defaulted
	Amount        float64 `json:"amount,omitempty"`
	CorrelationID string  `json:"correlation_id,omitempty"`
}

// PaymentUpdated is the Pub/Sub topic for payment plan lifecycle events.
var PaymentUpdated = pubsub.NewTopic[*PaymentUpdatedEvent]("payment-updated", pubsub.TopicConfig{
	DeliveryGuarantee: pubsub.AtLeastOnce,
})
