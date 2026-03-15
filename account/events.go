package account

import (
	"encoding/json"

	"encore.dev/pubsub"
)

// AccountLifecycleEvent is published when an account is created or its status changes.
type AccountLifecycleEvent struct {
	AccountID  int64           `json:"account_id"`
	ConsumerID int64           `json:"consumer_id"`
	Action     string          `json:"action"` // "created" or "status_updated"
	OldValue   json.RawMessage `json:"old_value,omitempty"`
	NewValue   json.RawMessage `json:"new_value,omitempty"`
}

// AccountLifecycle is the Pub/Sub topic for account lifecycle events.
var AccountLifecycle = pubsub.NewTopic[*AccountLifecycleEvent]("account-lifecycle", pubsub.TopicConfig{
	DeliveryGuarantee: pubsub.AtLeastOnce,
})
