package compliance

import (
	"context"
	"time"

	"encore.dev/beta/errs"
	"encore.dev/metrics"
	"encore.dev/rlog"
)

//encore:service
type Service struct{}

// Metrics per observability spec in CLAUDE.md / TD.md.
// Encore v1.52 does not expose a Histogram type; using a Gauge to track the
// latest check duration. Switch to metrics.NewHistogram once available.
var complianceCheckDuration = metrics.NewGauge[int64]("compliance_check_duration_ms", metrics.GaugeConfig{})

type complianceViolationLabels struct {
	Rule string
}

var complianceViolations = metrics.NewCounterGroup[complianceViolationLabels, uint64]("compliance_violation_total", metrics.CounterConfig{})

var validChannels = map[string]bool{
	"sms":   true,
	"email": true,
	"voice": true,
}

// CheckContact runs all pre-contact compliance rules and returns the aggregate
// result. All five rules evaluate on every call (non-short-circuit) so the
// caller receives the complete set of violations.
//
//encore:api public method=POST path=/compliance/check
func (s *Service) CheckContact(ctx context.Context, req *ContactCheckRequest) (*ContactCheckResult, error) {
	if req.ConsumerID <= 0 {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "consumer_id is required"}
	}
	if !validChannels[req.Channel] {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "channel must be sms, email, or voice"}
	}
	if req.Timezone == "" {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "timezone is required"}
	}

	start := time.Now()
	result := RunAllRules(req, DefaultRules())
	elapsed := time.Since(start).Milliseconds()

	complianceCheckDuration.Set(elapsed)
	for _, v := range result.Violations {
		complianceViolations.With(complianceViolationLabels{Rule: v.Rule}).Increment()
	}

	rlog.Info("compliance check completed",
		"service", "compliance",
		"consumer_id", req.ConsumerID,
		"channel", req.Channel,
		"allowed", result.Allowed,
		"violation_count", len(result.Violations),
		"duration_ms", elapsed,
	)

	return result, nil
}

// SanitizeText redacts PII patterns (SSN, credit card, phone) from text.
//
//encore:api public method=POST path=/compliance/sanitize
func (s *Service) SanitizeText(ctx context.Context, req *SanitizeRequest) (*SanitizeResponse, error) {
	if req.Text == "" {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "text is required"}
	}

	sanitized := SanitizePII(req.Text)

	rlog.Debug("pii sanitization completed",
		"service", "compliance",
		"redacted", sanitized != req.Text,
	)

	return &SanitizeResponse{
		Sanitized: sanitized,
		Redacted:  sanitized != req.Text,
	}, nil
}

// ScoreInteraction evaluates an interaction transcript against a scorecard rubric.
//
//encore:api public method=POST path=/compliance/score
func (s *Service) ScoreInteraction(ctx context.Context, req *ScoreRequest) (*ScoreResponse, error) {
	if req.Transcript == "" {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "transcript is required"}
	}
	if len(req.Rubric.Items) == 0 {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "rubric must have at least one item"}
	}

	result := EvaluateScorecard(req.Transcript, req.Rubric)

	rlog.Info("scorecard evaluation completed",
		"service", "compliance",
		"rubric", req.Rubric.Name,
		"total_score", result.TotalScore,
		"max_score", result.MaxScore,
		"required_passed", result.RequiredPassed,
	)

	return result, nil
}
