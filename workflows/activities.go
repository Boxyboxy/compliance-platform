package workflows

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
)

// Activities holds the HTTP client and base URL for calling Encore APIs.
// The Temporal worker cannot import Encore packages, so all interactions
// with Encore services happen via HTTP.
type Activities struct {
	Client  *http.Client
	BaseURL string
}

// CheckCompliance calls POST /compliance/check via HTTP.
func (a *Activities) CheckCompliance(ctx context.Context, input ComplianceCheckInput) (*ComplianceCheckOutput, error) {
	log.Printf("[activity] CheckCompliance consumer_id=%d channel=%s correlation_id=%s",
		input.ConsumerID, input.Channel, "")

	var result ComplianceCheckOutput
	if err := a.post(ctx, "/compliance/check", input, &result); err != nil {
		return nil, fmt.Errorf("compliance check: %w", err)
	}
	return &result, nil
}

// SanitizePII calls POST /compliance/sanitize via HTTP.
func (a *Activities) SanitizePII(ctx context.Context, input SanitizeInput) (*SanitizeOutput, error) {
	log.Printf("[activity] SanitizePII")

	var result SanitizeOutput
	if err := a.post(ctx, "/compliance/sanitize", input, &result); err != nil {
		return nil, fmt.Errorf("sanitize PII: %w", err)
	}
	return &result, nil
}

// SimulateDelivery deterministically simulates message delivery.
// attemptID % 10 == 0 means failure (no math/rand in workflow code).
func (a *Activities) SimulateDelivery(ctx context.Context, input SimulateDeliveryInput) (*DeliveryResult, error) {
	log.Printf("[activity] SimulateDelivery attempt_id=%d", input.AttemptID)

	if input.AttemptID%10 == 0 {
		return &DeliveryResult{Delivered: false, Status: "failed"}, nil
	}
	return &DeliveryResult{Delivered: true, Status: "delivered"}, nil
}

// RecordContactResult calls POST /contact/attempts/:id/result via HTTP.
func (a *Activities) RecordContactResult(ctx context.Context, input RecordResultInput) error {
	log.Printf("[activity] RecordContactResult attempt_id=%d status=%s", input.ContactAttemptID, input.Status)

	body := map[string]interface{}{
		"status":            input.Status,
		"message_content":   input.MessageContent,
		"compliance_result": input.ComplianceResult,
		"scorecard_result":  input.ScorecardResult,
		"block_reason":      input.BlockReason,
	}

	return a.post(ctx, fmt.Sprintf("/contact/attempts/%d/result", input.ContactAttemptID), body, nil)
}

// ScoreInteraction calls POST /compliance/score via HTTP.
func (a *Activities) ScoreInteraction(ctx context.Context, input ScoreInput) (*ScoreOutput, error) {
	log.Printf("[activity] ScoreInteraction")

	var result ScoreOutput
	if err := a.post(ctx, "/compliance/score", input, &result); err != nil {
		return nil, fmt.Errorf("score interaction: %w", err)
	}
	return &result, nil
}

// PublishContactAttempted calls POST /contact/internal/publish-attempted via HTTP.
func (a *Activities) PublishContactAttempted(ctx context.Context, input PublishAttemptedInput) error {
	log.Printf("[activity] PublishContactAttempted attempt_id=%d", input.ContactAttemptID)
	return a.post(ctx, "/contact/internal/publish-attempted", input, nil)
}

// PublishInteractionCreated calls POST /contact/internal/publish-interaction via HTTP.
func (a *Activities) PublishInteractionCreated(ctx context.Context, input PublishInteractionInput) error {
	log.Printf("[activity] PublishInteractionCreated attempt_id=%d", input.ContactAttemptID)
	return a.post(ctx, "/contact/internal/publish-interaction", input, nil)
}

// post is a helper that POSTs JSON to an Encore API and decodes the response.
func (a *Activities) post(ctx context.Context, path string, payload interface{}, result interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshalling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.Client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request to %s: %w", path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response from %s: %w", path, err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, path, string(respBody))
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decoding response from %s: %w", path, err)
		}
	}
	return nil
}
