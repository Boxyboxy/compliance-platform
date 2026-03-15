package workflows

// PaymentPlanInput contains all data needed to run the PaymentPlanWorkflow.
// No Encore imports — pure Go types used by the Temporal worker.
type PaymentPlanInput struct {
	PlanID          int64  `json:"PlanID"`
	AccountID       int64  `json:"AccountID"`
	NumInstallments int    `json:"NumInstallments"`
	Frequency       string `json:"Frequency"`
	CorrelationID   string `json:"CorrelationID"`
}

// MarkPlanInput is the payload for MarkPlanDefaulted / MarkPlanCompleted activities.
type MarkPlanInput struct {
	PlanID        int64  `json:"PlanID"`
	CorrelationID string `json:"CorrelationID,omitempty"`
}
