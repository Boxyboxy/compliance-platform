package compliance

import (
	"math"
	"testing"
)

// testRubric returns the standard rubric from the TD.md spec.
func testRubric() ScorecardRubric {
	return ScorecardRubric{
		Name: "default_rubric",
		Items: []ScorecardItem{
			{
				ID:          "agent_id",
				Description: "Agent identified themselves",
				Required:    true,
				Keywords:    []string{"my name is", "this is", "calling from"},
				Weight:      10,
			},
			{
				ID:          "mini_miranda",
				Description: "Mini-Miranda disclosure",
				Required:    true,
				Keywords:    []string{"attempt to collect a debt", "information will be used"},
				Weight:      20,
			},
			{
				ID:          "payment_option",
				Description: "Payment option offered",
				Required:    false,
				Keywords:    []string{"payment plan", "settle", "arrangement"},
				Weight:      15,
			},
		},
	}
}

func TestEvaluateScorecard_AllPass(t *testing.T) {
	transcript := "Hello, this is Daniel calling from Krew. " +
		"This is an attempt to collect a debt and any information will be used for that purpose. " +
		"We can offer you a payment plan to resolve this balance."

	result := EvaluateScorecard(transcript, testRubric())

	if result.TotalScore != 45 {
		t.Errorf("TotalScore = %d, want 45", result.TotalScore)
	}
	if result.MaxScore != 45 {
		t.Errorf("MaxScore = %d, want 45", result.MaxScore)
	}
	if result.Percentage != 100 {
		t.Errorf("Percentage = %f, want 100", result.Percentage)
	}
	if !result.RequiredPassed {
		t.Error("RequiredPassed = false, want true")
	}
	for _, item := range result.ItemResults {
		if !item.Passed {
			t.Errorf("item %q should have passed", item.ID)
		}
	}
}

func TestEvaluateScorecard_RequiredMissing(t *testing.T) {
	// Missing Mini-Miranda disclosure (required)
	transcript := "Hello, this is Daniel calling from Krew. " +
		"We can offer you a payment plan."

	result := EvaluateScorecard(transcript, testRubric())

	if result.RequiredPassed {
		t.Error("RequiredPassed = true, want false (mini_miranda missing)")
	}
	// agent_id (10) + payment_option (15) = 25 out of 45
	if result.TotalScore != 25 {
		t.Errorf("TotalScore = %d, want 25", result.TotalScore)
	}

	// Verify individual items
	for _, item := range result.ItemResults {
		switch item.ID {
		case "agent_id":
			if !item.Passed {
				t.Error("agent_id should pass")
			}
		case "mini_miranda":
			if item.Passed {
				t.Error("mini_miranda should fail")
			}
		case "payment_option":
			if !item.Passed {
				t.Error("payment_option should pass")
			}
		}
	}
}

func TestEvaluateScorecard_OptionalMissing(t *testing.T) {
	// Payment option (optional) missing — required check still passes
	transcript := "Hello, this is Daniel calling from Krew. " +
		"This is an attempt to collect a debt and any information will be used for that purpose."

	result := EvaluateScorecard(transcript, testRubric())

	if !result.RequiredPassed {
		t.Error("RequiredPassed = false, want true (only optional item missing)")
	}
	// agent_id (10) + mini_miranda (20) = 30 out of 45
	if result.TotalScore != 30 {
		t.Errorf("TotalScore = %d, want 30", result.TotalScore)
	}
	wantPct := float64(30) / float64(45) * 100
	if math.Abs(result.Percentage-wantPct) > 0.01 {
		t.Errorf("Percentage = %f, want %f", result.Percentage, wantPct)
	}
}

func TestEvaluateScorecard_CaseInsensitive(t *testing.T) {
	transcript := "HELLO, THIS IS DANIEL CALLING FROM KREW. " +
		"THIS IS AN ATTEMPT TO COLLECT A DEBT. " +
		"WE OFFER A PAYMENT PLAN."

	result := EvaluateScorecard(transcript, testRubric())

	if result.TotalScore != 45 {
		t.Errorf("TotalScore = %d, want 45 (case insensitive match)", result.TotalScore)
	}
	if !result.RequiredPassed {
		t.Error("RequiredPassed should be true")
	}
}

func TestEvaluateScorecard_NoneMatch(t *testing.T) {
	transcript := "The weather is nice today."

	result := EvaluateScorecard(transcript, testRubric())

	if result.TotalScore != 0 {
		t.Errorf("TotalScore = %d, want 0", result.TotalScore)
	}
	if result.RequiredPassed {
		t.Error("RequiredPassed = true, want false")
	}
	if result.Percentage != 0 {
		t.Errorf("Percentage = %f, want 0", result.Percentage)
	}
}

func TestEvaluateScorecard_PartialMatch(t *testing.T) {
	// Only agent_id matches via second keyword "this is"
	transcript := "Hello, this is Daniel."

	result := EvaluateScorecard(transcript, testRubric())

	if result.TotalScore != 10 {
		t.Errorf("TotalScore = %d, want 10", result.TotalScore)
	}
	// Required mini_miranda missing
	if result.RequiredPassed {
		t.Error("RequiredPassed = true, want false")
	}
}

func TestEvaluateScorecard_EmptyRubric(t *testing.T) {
	rubric := ScorecardRubric{Name: "empty", Items: []ScorecardItem{}}
	result := EvaluateScorecard("any text", rubric)

	if result.TotalScore != 0 {
		t.Errorf("TotalScore = %d, want 0", result.TotalScore)
	}
	if result.MaxScore != 0 {
		t.Errorf("MaxScore = %d, want 0", result.MaxScore)
	}
	if !result.RequiredPassed {
		t.Error("RequiredPassed should be true with no items")
	}
}

func TestEvaluateScorecard_MatchedKeywordReported(t *testing.T) {
	transcript := "My name is Daniel and this is an attempt to collect a debt."
	result := EvaluateScorecard(transcript, testRubric())

	for _, item := range result.ItemResults {
		if item.Passed && item.MatchedKeyword == "" {
			t.Errorf("item %q passed but MatchedKeyword is empty", item.ID)
		}
	}
}
