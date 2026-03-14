package consumer

import (
	"encore.dev/pubsub"

	"compliance-platform/internal/domain"
)

// ConsentChangedEvent is published to the consent-changed Pub/Sub topic.
type ConsentChangedEvent struct {
	ConsumerID    int64                `json:"consumer_id"`
	ConsentStatus domain.ConsentStatus `json:"consent_status"`
	ChangedAt     string               `json:"changed_at"`
}

// ConsentChanged is published whenever a consumer's consent status is revoked.
var ConsentChanged = pubsub.NewTopic[*ConsentChangedEvent]("consent-changed", pubsub.TopicConfig{
	DeliveryGuarantee: pubsub.AtLeastOnce,
})
