package consumer

import (
	"encoding/json"

	"encore.dev/pubsub"

	"compliance-platform/internal/domain"
)

// ConsentChangedEvent is published to the consent-changed Pub/Sub topic.
type ConsentChangedEvent struct {
	ConsumerID    int64                `json:"consumer_id"`
	ConsentStatus domain.ConsentStatus `json:"consent_status"`
	ChangedAt     string               `json:"changed_at"`
}

// ConsentChanged is published whenever a consumer's consent status changes (grant or revoke).
var ConsentChanged = pubsub.NewTopic[*ConsentChangedEvent]("consent-changed", pubsub.TopicConfig{
	DeliveryGuarantee: pubsub.AtLeastOnce,
})

// ConsumerLifecycleEvent is published when a consumer is created.
type ConsumerLifecycleEvent struct {
	ConsumerID int64           `json:"consumer_id"`
	Action     string          `json:"action"` // "created"
	NewValue   json.RawMessage `json:"new_value,omitempty"`
}

// ConsumerLifecycle is the Pub/Sub topic for consumer lifecycle events.
var ConsumerLifecycle = pubsub.NewTopic[*ConsumerLifecycleEvent]("consumer-lifecycle", pubsub.TopicConfig{
	DeliveryGuarantee: pubsub.AtLeastOnce,
})
