package account

import (
	"time"

	"compliance-platform/internal/domain"
)

// Account is the full account record returned by API endpoints.
type Account struct {
	ID               int64                `json:"id"`
	ConsumerID       int64                `json:"consumer_id"`
	OriginalCreditor string               `json:"original_creditor"`
	AccountNumber    string               `json:"account_number"`
	BalanceDue       float64              `json:"balance_due"`
	DaysPastDue      int                  `json:"days_past_due"`
	Status           domain.AccountStatus `json:"status"`
	CreatedAt        time.Time            `json:"created_at"`
	UpdatedAt        time.Time            `json:"updated_at"`
}

// AccountList wraps a slice of accounts for list endpoints.
type AccountList struct {
	Accounts []*Account `json:"accounts"`
}

// CreateAccountReq is the request body for POST /accounts.
type CreateAccountReq struct {
	ConsumerID       int64                `json:"consumer_id"`
	OriginalCreditor string               `json:"original_creditor"`
	AccountNumber    string               `json:"account_number"`
	BalanceDue       float64              `json:"balance_due"`
	DaysPastDue      int                  `json:"days_past_due,omitempty"`
	Status           domain.AccountStatus `json:"status,omitempty"`
}

// UpdateStatusReq is the request body for PATCH /accounts/:id/status.
type UpdateStatusReq struct {
	Status domain.AccountStatus `json:"status"`
}
