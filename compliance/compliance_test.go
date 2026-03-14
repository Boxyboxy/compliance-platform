package compliance

import (
	"context"
	"testing"
	"time"

	"compliance-platform/internal/domain"
)

func newSvc() *Service {
	return &Service{}
}

// ---------------------------------------------------------------------------
// CheckContact handler tests
// ---------------------------------------------------------------------------

func TestCheckContact_Valid(t *testing.T) {
	svc := newSvc()
	now := timeInLoc("America/New_York", 10, 0)

	result, err := svc.CheckContact(context.Background(), &ContactCheckRequest{
		ConsumerID:    1,
		Channel:       domain.ChannelVoice,
		Timezone:      "America/New_York",
		ConsentStatus: domain.ConsentGranted,
		CheckTime:     &now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Errorf("expected allowed, got violations: %+v", result.Violations)
	}
}

func TestCheckContact_Blocked(t *testing.T) {
	svc := newSvc()
	now := timeInLoc("America/New_York", 7, 0)

	result, err := svc.CheckContact(context.Background(), &ContactCheckRequest{
		ConsumerID:    1,
		Channel:       domain.ChannelVoice,
		Timezone:      "America/New_York",
		ConsentStatus: domain.ConsentGranted,
		CheckTime:     &now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Allowed {
		t.Error("expected blocked (7am), got allowed")
	}
}

func TestCheckContact_ValidationErrors(t *testing.T) {
	svc := newSvc()

	tests := []struct {
		name string
		req  *ContactCheckRequest
	}{
		{"missing consumer_id", &ContactCheckRequest{Channel: domain.ChannelSMS, Timezone: "America/New_York"}},
		{"invalid channel", &ContactCheckRequest{ConsumerID: 1, Channel: domain.Channel("fax"), Timezone: "America/New_York"}},
		{"missing timezone", &ContactCheckRequest{ConsumerID: 1, Channel: domain.ChannelSMS}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.CheckContact(context.Background(), tt.req)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SanitizeText handler tests
// ---------------------------------------------------------------------------

func TestSanitizeText_Valid(t *testing.T) {
	svc := newSvc()
	resp, err := svc.SanitizeText(context.Background(), &SanitizeRequest{
		Text: "SSN is 123-45-6789",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Redacted {
		t.Error("expected Redacted=true")
	}
	if resp.Sanitized != "SSN is [SSN_REDACTED]" {
		t.Errorf("Sanitized = %q, want %q", resp.Sanitized, "SSN is [SSN_REDACTED]")
	}
}

func TestSanitizeText_NoRedaction(t *testing.T) {
	svc := newSvc()
	resp, err := svc.SanitizeText(context.Background(), &SanitizeRequest{
		Text: "Hello world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Redacted {
		t.Error("expected Redacted=false for clean text")
	}
}

func TestSanitizeText_EmptyText(t *testing.T) {
	svc := newSvc()
	_, err := svc.SanitizeText(context.Background(), &SanitizeRequest{Text: ""})
	if err == nil {
		t.Fatal("expected error for empty text")
	}
}

// ---------------------------------------------------------------------------
// ScoreInteraction handler tests
// ---------------------------------------------------------------------------

func TestScoreInteraction_Valid(t *testing.T) {
	svc := newSvc()
	resp, err := svc.ScoreInteraction(context.Background(), &ScoreRequest{
		Transcript: "Hello, this is Daniel calling from Krew. This is an attempt to collect a debt.",
		Rubric:     testRubric(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TotalScore == 0 {
		t.Error("expected non-zero score")
	}
}

func TestScoreInteraction_EmptyTranscript(t *testing.T) {
	svc := newSvc()
	_, err := svc.ScoreInteraction(context.Background(), &ScoreRequest{
		Transcript: "",
		Rubric:     testRubric(),
	})
	if err == nil {
		t.Fatal("expected error for empty transcript")
	}
}

func TestScoreInteraction_EmptyRubric(t *testing.T) {
	svc := newSvc()
	_, err := svc.ScoreInteraction(context.Background(), &ScoreRequest{
		Transcript: "Hello world",
		Rubric:     ScorecardRubric{Name: "empty", Items: []ScorecardItem{}},
	})
	if err == nil {
		t.Fatal("expected error for empty rubric")
	}
}

// ---------------------------------------------------------------------------
// CheckContact with metrics — verify multiple violations are all recorded
// ---------------------------------------------------------------------------

func TestCheckContact_MetricsRecorded(t *testing.T) {
	svc := newSvc()
	now := timeInLoc("America/New_York", 7, 0)

	result, err := svc.CheckContact(context.Background(), &ContactCheckRequest{
		ConsumerID:              1,
		Channel:                 domain.ChannelSMS,
		Timezone:                "America/New_York",
		ConsentStatus:           domain.ConsentRevoked,
		AttorneyOnFile:          true,
		RecentContactTimestamps: nTimestamps(8, now.Add(-1*time.Hour)),
		MessageContent:          "Pay now.",
		CheckTime:               &now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Allowed {
		t.Error("expected blocked")
	}
	if len(result.Violations) < 4 {
		t.Errorf("expected at least 4 violations, got %d", len(result.Violations))
	}
}
