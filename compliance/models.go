package compliance

import "time"

// Rule defines the interface for a compliance rule. Each rule evaluates
// independently and returns a violation if the check fails, or nil if it passes.
type Rule interface {
	Evaluate(req *ContactCheckRequest) *Violation
}

// ContactCheckRequest contains all data needed to evaluate compliance rules.
//
// Design decision: the caller provides consumer state and contact history
// rather than the compliance service fetching it via cross-service calls.
// This keeps the compliance service stateless (no database, no service
// dependencies), makes every rule a pure function of its input (trivial to
// test), and avoids circular dependencies when the contact service later
// calls compliance for pre-checks.
type ContactCheckRequest struct {
	ConsumerID              int64      `json:"consumer_id"`
	Channel                 string     `json:"channel"`                            // "sms", "email", "voice"
	Timezone                string     `json:"timezone"`                           // IANA timezone e.g. "America/New_York"
	ConsentStatus           string     `json:"consent_status"`                     // "granted" or "revoked"
	DoNotContact            bool       `json:"do_not_contact"`                     // regulatory do-not-contact flag
	AttorneyOnFile          bool       `json:"attorney_on_file"`                   // FDCPA § 805(a)(2)
	RecentContactTimestamps []time.Time `json:"recent_contact_timestamps,omitempty"` // trailing contact attempts for frequency cap
	MessageContent          string     `json:"message_content,omitempty"`          // outbound payload for opt-out validation
	CheckTime               *time.Time `json:"check_time,omitempty"`               // override for testing; defaults to time.Now()
}

// ContactCheckResult is the response from a pre-contact compliance check.
type ContactCheckResult struct {
	Allowed    bool        `json:"allowed"`
	Violations []Violation `json:"violations"`
}

// Violation describes a single compliance rule failure.
type Violation struct {
	Rule    string `json:"rule"`
	Details string `json:"details"`
}

// SanitizeRequest is the input for PII sanitization.
type SanitizeRequest struct {
	Text string `json:"text"`
}

// SanitizeResponse is the output from PII sanitization.
type SanitizeResponse struct {
	Sanitized string `json:"sanitized"`
	Redacted  bool   `json:"redacted"`
}

// ScoreRequest is the input for scorecard evaluation.
type ScoreRequest struct {
	Transcript string          `json:"transcript"`
	Rubric     ScorecardRubric `json:"rubric"`
}

// ScoreResponse is the output from scorecard evaluation.
type ScoreResponse struct {
	TotalScore     int          `json:"total_score"`
	MaxScore       int          `json:"max_score"`
	Percentage     float64      `json:"percentage"`
	RequiredPassed bool         `json:"required_passed"`
	ItemResults    []ItemResult `json:"item_results"`
}

// ScorecardRubric is a JSON-configured list of scoring items.
type ScorecardRubric struct {
	Name  string          `json:"name"`
	Items []ScorecardItem `json:"items"`
}

// ScorecardItem is a single item in a scoring rubric.
type ScorecardItem struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Required    bool     `json:"required"`
	Keywords    []string `json:"keywords"`
	Weight      int      `json:"weight"`
}

// ItemResult is the evaluation result for a single scorecard item.
type ItemResult struct {
	ID             string `json:"id"`
	Description    string `json:"description"`
	Passed         bool   `json:"passed"`
	Required       bool   `json:"required"`
	Weight         int    `json:"weight"`
	MatchedKeyword string `json:"matched_keyword,omitempty"`
}
