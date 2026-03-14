package consumer

import (
	"time"

	"compliance-platform/internal/domain"
)

// Consumer is the full consumer record returned by API endpoints.
type Consumer struct {
	ID             int64                `json:"id"`
	ExternalID     string               `json:"external_id"`
	FirstName      string               `json:"first_name"`
	LastName       string               `json:"last_name"`
	Phone          string               `json:"phone,omitempty"`
	Email          string               `json:"email,omitempty"`
	Timezone       string               `json:"timezone"`
	ConsentStatus  domain.ConsentStatus `json:"consent_status"`
	DoNotContact   bool                 `json:"do_not_contact"`
	AttorneyOnFile bool                 `json:"attorney_on_file"`
	CreatedAt      time.Time            `json:"created_at"`
	UpdatedAt      time.Time            `json:"updated_at"`
}

// CreateConsumerReq is the request body for POST /consumers.
type CreateConsumerReq struct {
	ExternalID string `json:"external_id"`
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
	Phone      string `json:"phone,omitempty"`
	Email      string `json:"email,omitempty"`
	Timezone   string `json:"timezone,omitempty"`
}

// UpdateConsentReq is the request body for PUT /consumers/:id/consent.
type UpdateConsentReq struct {
	ConsentStatus domain.ConsentStatus `json:"consent_status"` // "granted" or "revoked"
}
