package compliance

import (
	"testing"
	"time"
)

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// timeInLoc creates a time at the given hour:min in the named timezone.
// Uses a fixed summer date (June 15 2025) to make EDT/HST offsets predictable.
func timeInLoc(tz string, hour, min int) time.Time {
	return time.Date(2025, 6, 15, hour, min, 0, 0, mustLoadLocation(tz))
}

// nTimestamps returns n timestamps starting at base, each 1 minute apart.
func nTimestamps(n int, base time.Time) []time.Time {
	ts := make([]time.Time, n)
	for i := range ts {
		ts[i] = base.Add(time.Duration(i) * time.Minute)
	}
	return ts
}

// ---------------------------------------------------------------------------
// TimeWindowRule
// ---------------------------------------------------------------------------

func TestTimeWindowRule(t *testing.T) {
	rule := &TimeWindowRule{}

	tests := []struct {
		name      string
		timezone  string
		checkTime time.Time
		wantAllow bool
	}{
		// Standard cases
		{"NYC 10am allowed", "America/New_York", timeInLoc("America/New_York", 10, 0), true},
		{"NYC 7am blocked", "America/New_York", timeInLoc("America/New_York", 7, 0), false},
		{"NYC 9:01pm blocked", "America/New_York", timeInLoc("America/New_York", 21, 1), false},
		{"NYC noon allowed", "America/New_York", timeInLoc("America/New_York", 12, 0), true},
		{"NYC 7:42am blocked", "America/New_York", timeInLoc("America/New_York", 7, 42), false},

		// Hawaii (UTC-10, no DST)
		{"Hawaii 8pm allowed", "Pacific/Honolulu", timeInLoc("Pacific/Honolulu", 20, 0), true},
		{"Hawaii 6am blocked", "Pacific/Honolulu", timeInLoc("Pacific/Honolulu", 6, 0), false},

		// Edge cases: exactly 8am and 9pm are both allowed
		{"exactly 8am allowed", "America/New_York", timeInLoc("America/New_York", 8, 0), true},
		{"exactly 9pm allowed", "America/New_York", timeInLoc("America/New_York", 21, 0), true},
		{"one second before 8am blocked", "America/New_York",
			time.Date(2025, 6, 15, 7, 59, 59, 0, mustLoadLocation("America/New_York")), false},

		// DST transition: March 9 2025 is spring-forward in US Eastern.
		// Clocks jump from 2am EST to 3am EDT. Go handles this via tz database.
		{"DST spring forward 8:30am allowed", "America/New_York",
			time.Date(2025, 3, 9, 8, 30, 0, 0, mustLoadLocation("America/New_York")), true},
		{"DST spring forward 7:59am blocked", "America/New_York",
			time.Date(2025, 3, 9, 7, 59, 0, 0, mustLoadLocation("America/New_York")), false},

		// Midnight blocked
		{"midnight blocked", "America/Chicago", timeInLoc("America/Chicago", 0, 0), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &ContactCheckRequest{
				Timezone:      tt.timezone,
				CheckTime:     &tt.checkTime,
				Channel:       "voice",
				ConsentStatus: "granted",
			}
			v := rule.Evaluate(req)
			gotAllow := v == nil
			if gotAllow != tt.wantAllow {
				t.Errorf("got allowed=%v, want %v", gotAllow, tt.wantAllow)
			}
		})
	}
}

func TestTimeWindowRule_InvalidTimezone(t *testing.T) {
	rule := &TimeWindowRule{}
	now := time.Now()
	req := &ContactCheckRequest{
		Timezone:  "Invalid/Timezone",
		CheckTime: &now,
		Channel:   "voice",
	}
	v := rule.Evaluate(req)
	if v == nil {
		t.Fatal("expected violation for invalid timezone")
	}
	if v.Rule != "time_window" {
		t.Errorf("rule = %q, want time_window", v.Rule)
	}
}

// ---------------------------------------------------------------------------
// FrequencyCapRule
// ---------------------------------------------------------------------------

func TestFrequencyCapRule(t *testing.T) {
	rule := &FrequencyCapRule{MaxAttempts: 7, WindowDays: 7}
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		timestamps []time.Time
		wantAllow  bool
	}{
		{"no contacts - allow", nil, true},
		{"1 contact - allow", nTimestamps(1, now.Add(-1*time.Hour)), true},
		{"6 contacts - allow", nTimestamps(6, now.Add(-1*time.Hour)), true},
		{"7 contacts - block", nTimestamps(7, now.Add(-1*time.Hour)), false},
		{"8 contacts - block", nTimestamps(8, now.Add(-2*time.Hour)), false},

		// Contacts older than 7 days don't count
		{"7 contacts all older than 7 days - allow",
			nTimestamps(7, now.Add(-8*24*time.Hour)), true},

		// Mix of recent and old
		{"6 recent + 1 old - allow",
			append(nTimestamps(6, now.Add(-1*time.Hour)), now.Add(-8*24*time.Hour)), true},

		// Boundary: exactly 7 days ago counts (conservative for compliance)
		{"6 recent + 1 exactly 7 days ago - block",
			append(nTimestamps(6, now.Add(-1*time.Hour)), now.Add(-7*24*time.Hour)), false},

		// Just past the boundary: 7 days + 1 second ago does not count
		{"6 recent + 1 just over 7 days - allow",
			append(nTimestamps(6, now.Add(-1*time.Hour)), now.Add(-7*24*time.Hour-time.Second)), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &ContactCheckRequest{
				CheckTime:               &now,
				RecentContactTimestamps: tt.timestamps,
				Channel:                 "voice",
				ConsentStatus:           "granted",
			}
			v := rule.Evaluate(req)
			gotAllow := v == nil
			if gotAllow != tt.wantAllow {
				t.Errorf("got allowed=%v, want %v", gotAllow, tt.wantAllow)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// AttorneyBlockRule
// ---------------------------------------------------------------------------

func TestAttorneyBlockRule(t *testing.T) {
	rule := &AttorneyBlockRule{}

	tests := []struct {
		name           string
		attorneyOnFile bool
		wantAllow      bool
	}{
		{"no attorney - allow", false, true},
		{"attorney on file - block", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &ContactCheckRequest{
				AttorneyOnFile: tt.attorneyOnFile,
			}
			v := rule.Evaluate(req)
			gotAllow := v == nil
			if gotAllow != tt.wantAllow {
				t.Errorf("got allowed=%v, want %v", gotAllow, tt.wantAllow)
			}
			if v != nil && v.Rule != "attorney_block" {
				t.Errorf("rule = %q, want attorney_block", v.Rule)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ConsentCheckRule
// ---------------------------------------------------------------------------

func TestConsentCheckRule(t *testing.T) {
	rule := &ConsentCheckRule{}

	tests := []struct {
		name          string
		consentStatus string
		doNotContact  bool
		wantAllow     bool
	}{
		{"granted + no DNC - allow", "granted", false, true},
		{"revoked - block", "revoked", false, false},
		{"granted + DNC true - block", "granted", true, false},
		{"revoked + DNC true - block", "revoked", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &ContactCheckRequest{
				ConsentStatus: tt.consentStatus,
				DoNotContact:  tt.doNotContact,
			}
			v := rule.Evaluate(req)
			gotAllow := v == nil
			if gotAllow != tt.wantAllow {
				t.Errorf("got allowed=%v, want %v", gotAllow, tt.wantAllow)
			}
			if v != nil && v.Rule != "consent_check" {
				t.Errorf("rule = %q, want consent_check", v.Rule)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// OptOutValidationRule
// ---------------------------------------------------------------------------

func TestOptOutValidationRule(t *testing.T) {
	rule := &OptOutValidationRule{}

	tests := []struct {
		name           string
		channel        string
		messageContent string
		wantAllow      bool
	}{
		// SMS with opt-out keywords
		{"sms with reply STOP - pass", "sms", "Your balance is $500. Reply STOP to opt out.", true},
		{"sms with unsubscribe - pass", "sms", "Click here to unsubscribe from messages.", true},
		{"sms with stop - pass", "sms", "Text STOP to stop receiving messages.", true},
		{"sms without opt-out - fail", "sms", "Your balance is $500. Please pay now.", false},

		// Email with opt-out keywords
		{"email with unsubscribe - pass", "email", "To unsubscribe, click the link below.", true},
		{"email with opt out - pass", "email", "You may opt out of these communications.", true},
		{"email without opt-out - fail", "email", "Dear customer, please contact us.", false},

		// Voice is exempt
		{"voice skips check", "voice", "Hello, this is a call about your account.", true},
		{"voice with no content skips", "voice", "", true},

		// Empty message content — rule passes (message not yet generated)
		{"sms empty content - pass", "sms", "", true},
		{"email empty content - pass", "email", "", true},

		// Case insensitive
		{"sms UNSUBSCRIBE uppercase - pass", "sms", "UNSUBSCRIBE from this list.", true},
		{"sms OPT OUT mixed case - pass", "sms", "Opt Out of further contact.", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &ContactCheckRequest{
				Channel:        tt.channel,
				MessageContent: tt.messageContent,
			}
			v := rule.Evaluate(req)
			gotAllow := v == nil
			if gotAllow != tt.wantAllow {
				t.Errorf("got allowed=%v, want %v", gotAllow, tt.wantAllow)
			}
			if v != nil && v.Rule != "opt_out_validation" {
				t.Errorf("rule = %q, want opt_out_validation", v.Rule)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// RunAllRules — combined / integration tests
// ---------------------------------------------------------------------------

func TestRunAllRules_AllPass(t *testing.T) {
	now := timeInLoc("America/New_York", 10, 0)
	req := &ContactCheckRequest{
		ConsumerID:              1,
		Channel:                 "sms",
		Timezone:                "America/New_York",
		ConsentStatus:           "granted",
		DoNotContact:            false,
		AttorneyOnFile:          false,
		RecentContactTimestamps: nTimestamps(3, now.Add(-2*time.Hour)),
		MessageContent:          "Hello! Reply STOP to opt out.",
		CheckTime:               &now,
	}

	result := RunAllRules(req, DefaultRules())
	if !result.Allowed {
		t.Errorf("expected allowed, got violations: %+v", result.Violations)
	}
	if len(result.Violations) != 0 {
		t.Errorf("expected 0 violations, got %d", len(result.Violations))
	}
}

func TestRunAllRules_MultipleViolations(t *testing.T) {
	// Consumer with multiple problems: attorney on file, consent revoked,
	// contact at 7am, 7+ contacts in window, SMS without opt-out.
	now := timeInLoc("America/New_York", 7, 0)
	req := &ContactCheckRequest{
		ConsumerID:              1,
		Channel:                 "sms",
		Timezone:                "America/New_York",
		ConsentStatus:           "revoked",
		DoNotContact:            false,
		AttorneyOnFile:          true,
		RecentContactTimestamps: nTimestamps(8, now.Add(-2*time.Hour)),
		MessageContent:          "Pay your bill.",
		CheckTime:               &now,
	}

	result := RunAllRules(req, DefaultRules())
	if result.Allowed {
		t.Fatal("expected blocked, got allowed")
	}

	// Expect violations from: time_window, frequency_cap, attorney_block,
	// consent_check, opt_out_validation — all five rules.
	ruleSet := make(map[string]bool)
	for _, v := range result.Violations {
		ruleSet[v.Rule] = true
	}

	expected := []string{"time_window", "frequency_cap", "attorney_block", "consent_check", "opt_out_validation"}
	for _, r := range expected {
		if !ruleSet[r] {
			t.Errorf("missing expected violation for rule %q; got violations: %+v", r, result.Violations)
		}
	}
}

func TestRunAllRules_ViolationsAlwaysNonNil(t *testing.T) {
	now := timeInLoc("America/New_York", 10, 0)
	req := &ContactCheckRequest{
		ConsumerID:    1,
		Channel:       "voice",
		Timezone:      "America/New_York",
		ConsentStatus: "granted",
		CheckTime:     &now,
	}
	result := RunAllRules(req, DefaultRules())
	if result.Violations == nil {
		t.Error("Violations should be non-nil empty slice, got nil")
	}
}
