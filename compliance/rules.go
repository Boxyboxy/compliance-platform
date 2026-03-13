package compliance

import (
	"fmt"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// TimeWindowRule — TCPA: no contact before 8am or after 9pm local time
// ---------------------------------------------------------------------------

// TimeWindowRule blocks contact outside the 8am–9pm window in the consumer's
// local timezone. Uses time.LoadLocation so DST transitions are handled by
// Go's tz database (no manual offset arithmetic).
type TimeWindowRule struct{}

func (r *TimeWindowRule) Evaluate(req *ContactCheckRequest) *Violation {
	loc, err := time.LoadLocation(req.Timezone)
	if err != nil {
		return &Violation{
			Rule:    "time_window",
			Details: fmt.Sprintf("invalid timezone %q: %v", req.Timezone, err),
		}
	}

	checkTime := time.Now()
	if req.CheckTime != nil {
		checkTime = *req.CheckTime
	}

	local := checkTime.In(loc)
	hour, min, sec := local.Clock()
	totalSeconds := hour*3600 + min*60 + sec

	// Allowed window: 08:00:00 (28800s) to 21:00:00 (75600s) inclusive.
	// "exactly 8am" and "exactly 9pm" are both permitted.
	if totalSeconds < 8*3600 || totalSeconds > 21*3600 {
		return &Violation{
			Rule:    "time_window",
			Details: fmt.Sprintf("Contact attempted at %s in %s", local.Format("3:04pm"), req.Timezone),
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// FrequencyCapRule — FDCPA Reg F: max N contacts in a rolling window
// ---------------------------------------------------------------------------

// FrequencyCapRule blocks contact when the consumer has received MaxAttempts
// or more contacts within the trailing WindowDays rolling window.
//
// The frequency cap is evaluated against RecentContactTimestamps provided by
// the caller. This avoids a cross-service call to the contact service, keeping
// compliance stateless and the rule a pure function of its input.
type FrequencyCapRule struct {
	MaxAttempts int
	WindowDays  int
}

func (r *FrequencyCapRule) Evaluate(req *ContactCheckRequest) *Violation {
	checkTime := time.Now()
	if req.CheckTime != nil {
		checkTime = *req.CheckTime
	}

	windowStart := checkTime.Add(-time.Duration(r.WindowDays) * 24 * time.Hour)

	count := 0
	for _, ts := range req.RecentContactTimestamps {
		// A timestamp counts if it falls within [windowStart, checkTime].
		// Inclusive on both ends — conservative for compliance.
		if !ts.Before(windowStart) && !ts.After(checkTime) {
			count++
		}
	}

	if count >= r.MaxAttempts {
		return &Violation{
			Rule:    "frequency_cap",
			Details: fmt.Sprintf("%d contact attempts in the last %d days (max %d)", count, r.WindowDays, r.MaxAttempts),
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// AttorneyBlockRule — FDCPA § 805(a)(2)
// ---------------------------------------------------------------------------

// AttorneyBlockRule blocks all contact if the consumer has an attorney on file.
type AttorneyBlockRule struct{}

func (r *AttorneyBlockRule) Evaluate(req *ContactCheckRequest) *Violation {
	if req.AttorneyOnFile {
		return &Violation{
			Rule:    "attorney_block",
			Details: "consumer has attorney on file",
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// ConsentCheckRule — consent revoked or do-not-contact flagged
// ---------------------------------------------------------------------------

// ConsentCheckRule blocks contact if consent is revoked or do-not-contact is set.
type ConsentCheckRule struct{}

func (r *ConsentCheckRule) Evaluate(req *ContactCheckRequest) *Violation {
	if req.ConsentStatus == "revoked" {
		return &Violation{
			Rule:    "consent_check",
			Details: "consumer consent has been revoked",
		}
	}
	if req.DoNotContact {
		return &Violation{
			Rule:    "consent_check",
			Details: "consumer is flagged as do-not-contact",
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// OptOutValidationRule — SMS/email must include opt-out instructions
// ---------------------------------------------------------------------------

// OptOutValidationRule validates that SMS and email payloads contain opt-out
// instructions. Voice channel is exempt. If no message content is provided
// (e.g. during a pre-check before message generation), the rule passes.
type OptOutValidationRule struct{}

var optOutKeywords = []string{"opt out", "unsubscribe", "stop", "reply stop"}

func (r *OptOutValidationRule) Evaluate(req *ContactCheckRequest) *Violation {
	channel := strings.ToLower(req.Channel)
	if channel != "sms" && channel != "email" {
		return nil
	}

	if req.MessageContent == "" {
		return nil
	}

	lower := strings.ToLower(req.MessageContent)
	for _, kw := range optOutKeywords {
		if strings.Contains(lower, kw) {
			return nil
		}
	}

	return &Violation{
		Rule:    "opt_out_validation",
		Details: fmt.Sprintf("%s message does not contain opt-out instructions", req.Channel),
	}
}

// ---------------------------------------------------------------------------
// Engine helpers
// ---------------------------------------------------------------------------

// DefaultRules returns the standard set of TCPA/FDCPA compliance rules.
func DefaultRules() []Rule {
	return []Rule{
		&TimeWindowRule{},
		&FrequencyCapRule{MaxAttempts: 7, WindowDays: 7},
		&AttorneyBlockRule{},
		&ConsentCheckRule{},
		&OptOutValidationRule{},
	}
}

// RunAllRules evaluates every rule against a request (non-short-circuit) and
// returns the aggregate result. All rules run regardless of prior failures so
// the caller receives the complete set of violations.
func RunAllRules(req *ContactCheckRequest, rules []Rule) *ContactCheckResult {
	var violations []Violation
	for _, rule := range rules {
		if v := rule.Evaluate(req); v != nil {
			violations = append(violations, *v)
		}
	}
	if violations == nil {
		violations = []Violation{}
	}
	return &ContactCheckResult{
		Allowed:    len(violations) == 0,
		Violations: violations,
	}
}
