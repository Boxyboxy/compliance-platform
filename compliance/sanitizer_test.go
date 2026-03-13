package compliance

import "testing"

func TestSanitizePII(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		// SSN
		{"SSN redacted", "My SSN is 123-45-6789.", "My SSN is [SSN_REDACTED]."},
		{"SSN without dashes not matched", "SSN 123456789 stays.", "SSN 123456789 stays."},
		{"multiple SSNs", "First 111-22-3333 second 444-55-6666.",
			"First [SSN_REDACTED] second [SSN_REDACTED]."},

		// Credit card
		{"CC with spaces", "Card 4111 1111 1111 1111 on file.",
			"Card [CC_REDACTED] on file."},
		{"CC with dashes", "Card 4111-1111-1111-1111 on file.",
			"Card [CC_REDACTED] on file."},
		{"CC no separators", "Card 4111111111111111 on file.",
			"Card [CC_REDACTED] on file."},

		// Phone
		{"phone parens", "Call (555) 123-4567 today.",
			"Call [PHONE_REDACTED] today."},
		{"phone dashed", "Call 555-123-4567 today.",
			"Call [PHONE_REDACTED] today."},
		{"phone dotted", "Call 555.123.4567 today.",
			"Call [PHONE_REDACTED] today."},
		{"phone intl", "Call +15551234567 today.",
			"Call [PHONE_REDACTED] today."},

		// Mixed — all PII types redacted in one pass
		{"mixed PII",
			"SSN 123-45-6789, card 4111 1111 1111 1111, phone (555) 123-4567.",
			"SSN [SSN_REDACTED], card [CC_REDACTED], phone [PHONE_REDACTED]."},

		// Clean text
		{"no PII", "This is a clean message with no sensitive data.",
			"This is a clean message with no sensitive data."},
		{"empty string", "", ""},

		// Edge cases
		{"partial SSN not matched", "Code 12-34-5678 stays.", "Code 12-34-5678 stays."},
		{"phone without area code not matched", "Call 123-4567 today.", "Call 123-4567 today."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizePII(tt.input)
			if got != tt.want {
				t.Errorf("\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}
