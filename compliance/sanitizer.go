package compliance

import "regexp"

// PII patterns compiled once at package init for performance.
// Applied in order: credit card (longest match), then SSN, then phone
// to prevent substring collisions across pattern types.
var (
	// SSN: exactly XXX-XX-XXXX with dashes.
	ssnPattern = regexp.MustCompile(`\d{3}-\d{2}-\d{4}`)

	// Credit card: 16 digits in groups of 4 with optional space or dash separators.
	ccPattern = regexp.MustCompile(`\d{4}[\s-]?\d{4}[\s-]?\d{4}[\s-]?\d{4}`)

	// US phone numbers in common formats:
	//   +15551234567           — international with country code
	//   (555) 123-4567         — parenthesised area code
	//   555-123-4567           — dashed
	//   555.123.4567           — dotted
	phonePattern = regexp.MustCompile(`\+1\d{10}|\(\d{3}\)\s*\d{3}-\d{4}|\d{3}[-.]\d{3}[-.]\d{4}`)
)

// SanitizePII redacts SSNs, credit card numbers, and phone numbers from text.
// The original string should never be stored — only the sanitized result.
func SanitizePII(text string) string {
	// Apply CC first (16-digit pattern) to prevent substrings from matching phone.
	result := ccPattern.ReplaceAllString(text, "[CC_REDACTED]")
	result = ssnPattern.ReplaceAllString(result, "[SSN_REDACTED]")
	result = phonePattern.ReplaceAllString(result, "[PHONE_REDACTED]")
	return result
}
