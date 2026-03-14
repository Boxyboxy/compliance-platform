package workflows

import "encoding/json"

// ContactWorkflowInput contains all data needed to run the contact workflow.
// Consumer state and recent timestamps are passed in so the workflow is self-contained.
type ContactWorkflowInput struct {
	ContactAttemptID        int64    `json:"ContactAttemptID"`
	ConsumerID              int64    `json:"ConsumerID"`
	AccountID               int64    `json:"AccountID"`
	Channel                 string   `json:"Channel"`
	MessageContent          string   `json:"MessageContent"`
	ConsumerTimezone        string   `json:"ConsumerTimezone"`
	ConsumerConsent         string   `json:"ConsumerConsent"`
	AttorneyOnFile          bool     `json:"AttorneyOnFile"`
	DoNotContact            bool     `json:"DoNotContact"`
	RecentContactTimestamps []string `json:"RecentContactTimestamps"`
	CorrelationID           string   `json:"CorrelationID"`
}

// ContactWorkflowResult is the output of the contact workflow.
type ContactWorkflowResult struct {
	ContactAttemptID int64  `json:"ContactAttemptID"`
	Status           string `json:"Status"`
	Allowed          bool   `json:"Allowed"`
	MessageContent   string `json:"MessageContent"`
	BlockReason      string `json:"BlockReason,omitempty"`
}

// ComplianceCheckInput mirrors the compliance service request.
// No Encore imports — used by the Temporal worker.
type ComplianceCheckInput struct {
	ConsumerID              int64    `json:"consumer_id"`
	Channel                 string   `json:"channel"`
	Timezone                string   `json:"timezone"`
	ConsentStatus           string   `json:"consent_status"`
	DoNotContact            bool     `json:"do_not_contact"`
	AttorneyOnFile          bool     `json:"attorney_on_file"`
	RecentContactTimestamps []string `json:"recent_contact_timestamps,omitempty"`
	MessageContent          string   `json:"message_content,omitempty"`
}

// ComplianceCheckOutput mirrors the compliance service response.
type ComplianceCheckOutput struct {
	Allowed    bool                 `json:"allowed"`
	Violations []ComplianceViolation `json:"violations"`
}

// ComplianceViolation mirrors the compliance Violation type.
type ComplianceViolation struct {
	Rule    string `json:"rule"`
	Details string `json:"details"`
}

// SanitizeInput mirrors the compliance sanitize request.
type SanitizeInput struct {
	Text string `json:"text"`
}

// SanitizeOutput mirrors the compliance sanitize response.
type SanitizeOutput struct {
	Sanitized string `json:"sanitized"`
	Redacted  bool   `json:"redacted"`
}

// SimulateDeliveryInput wraps the attempt ID for delivery simulation.
type SimulateDeliveryInput struct {
	AttemptID int64 `json:"attempt_id"`
}

// DeliveryResult is the result of a simulated delivery.
type DeliveryResult struct {
	Delivered bool   `json:"delivered"`
	Status    string `json:"status"`
}

// RecordResultInput is the payload for recording contact results.
type RecordResultInput struct {
	ContactAttemptID int64           `json:"contact_attempt_id"`
	Status           string          `json:"status"`
	MessageContent   string          `json:"message_content,omitempty"`
	ComplianceResult json.RawMessage `json:"compliance_result,omitempty"`
	ScorecardResult  json.RawMessage `json:"scorecard_result,omitempty"`
	BlockReason      string          `json:"block_reason,omitempty"`
}

// ScoreInput mirrors the compliance score request.
type ScoreInput struct {
	Transcript string         `json:"transcript"`
	Rubric     ScoreRubric    `json:"rubric"`
}

// ScoreRubric mirrors the compliance ScorecardRubric type.
type ScoreRubric struct {
	Name  string      `json:"name"`
	Items []ScoreItem `json:"items"`
}

// ScoreItem mirrors the compliance ScorecardItem type.
type ScoreItem struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Required    bool     `json:"required"`
	Keywords    []string `json:"keywords"`
	Weight      int      `json:"weight"`
}

// ScoreOutput mirrors the compliance ScoreResponse type.
type ScoreOutput struct {
	TotalScore     int     `json:"total_score"`
	MaxScore       int     `json:"max_score"`
	Percentage     float64 `json:"percentage"`
	RequiredPassed bool    `json:"required_passed"`
}

// PublishAttemptedInput is the payload for publishing contact-attempted events.
type PublishAttemptedInput struct {
	ContactAttemptID int64  `json:"contact_attempt_id"`
	ConsumerID       int64  `json:"consumer_id"`
	AccountID        int64  `json:"account_id"`
	Channel          string `json:"channel"`
	Status           string `json:"status"`
	BlockReason      string `json:"block_reason,omitempty"`
	CorrelationID    string `json:"correlation_id,omitempty"`
}

// PublishInteractionInput is the payload for publishing interaction-created events.
type PublishInteractionInput struct {
	ContactAttemptID int64           `json:"contact_attempt_id"`
	ConsumerID       int64           `json:"consumer_id"`
	AccountID        int64           `json:"account_id"`
	Channel          string          `json:"channel"`
	SanitizedContent string          `json:"sanitized_content"`
	ScorecardResult  json.RawMessage `json:"scorecard_result,omitempty"`
	CorrelationID    string          `json:"correlation_id,omitempty"`
}
