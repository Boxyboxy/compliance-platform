package compliance

import "strings"

// EvaluateScorecard scores an interaction transcript against a rubric.
// Matching is case-insensitive keyword search — no AI/NLP.
func EvaluateScorecard(transcript string, rubric ScorecardRubric) *ScoreResponse {
	lower := strings.ToLower(transcript)

	var results []ItemResult
	totalScore := 0
	maxScore := 0
	requiredPassed := true

	for _, item := range rubric.Items {
		maxScore += item.Weight

		result := ItemResult{
			ID:          item.ID,
			Description: item.Description,
			Required:    item.Required,
			Weight:      item.Weight,
		}

		for _, kw := range item.Keywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				result.Passed = true
				result.MatchedKeyword = kw
				break
			}
		}

		if result.Passed {
			totalScore += item.Weight
		} else if item.Required {
			requiredPassed = false
		}

		results = append(results, result)
	}

	var pct float64
	if maxScore > 0 {
		pct = float64(totalScore) / float64(maxScore) * 100
	}

	return &ScoreResponse{
		TotalScore:     totalScore,
		MaxScore:       maxScore,
		Percentage:     pct,
		RequiredPassed: requiredPassed,
		ItemResults:    results,
	}
}
