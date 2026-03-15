package payment

import (
	"encoding/json"
	"time"
)

// PaymentPlan is the full payment plan record returned by API endpoints.
type PaymentPlan struct {
	ID              int64      `json:"id"`
	AccountID       int64      `json:"account_id"`
	TotalAmount     float64    `json:"total_amount"`
	NumInstallments int        `json:"num_installments"`
	InstallmentAmt  float64    `json:"installment_amt"`
	Frequency       string     `json:"frequency"`
	Status          string     `json:"status"`
	ProposedAt      time.Time  `json:"proposed_at"`
	AcceptedAt      *time.Time `json:"accepted_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
}

// PaymentPlanList wraps a slice of payment plans for list endpoints.
type PaymentPlanList struct {
	Plans []*PaymentPlan `json:"plans"`
}

// PaymentEvent is a single event in a payment plan's lifecycle.
type PaymentEvent struct {
	ID         int64           `json:"id"`
	PlanID     int64           `json:"plan_id"`
	EventType  string          `json:"event_type"`
	Amount     *float64        `json:"amount,omitempty"`
	OccurredAt time.Time       `json:"occurred_at"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

// ProposePlanReq is the request body for POST /payment-plans.
type ProposePlanReq struct {
	AccountID       int64   `json:"account_id"`
	TotalAmount     float64 `json:"total_amount"`
	NumInstallments int     `json:"num_installments"`
	InstallmentAmt  float64 `json:"installment_amt"`
	Frequency       string  `json:"frequency,omitempty"`
}

// RecordPaymentReq is the request body for POST /payment-plans/:id/payments.
type RecordPaymentReq struct {
	Amount float64 `json:"amount"`
}
