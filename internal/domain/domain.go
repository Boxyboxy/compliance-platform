package domain

// Scanner is satisfied by both *sqldb.Row and *sqldb.Rows, so scan helpers
// can be used for single-row queries and inside rows.Next() loops alike.
type Scanner interface {
	Scan(dest ...any) error
}

// ---------------------------------------------------------------------------
// Channel
// ---------------------------------------------------------------------------

// Channel represents a communication channel (sms, email, voice).
type Channel string

const (
	ChannelSMS   Channel = "sms"
	ChannelEmail Channel = "email"
	ChannelVoice Channel = "voice"
)

// Valid returns true if c is a recognised channel.
func (c Channel) Valid() bool {
	switch c {
	case ChannelSMS, ChannelEmail, ChannelVoice:
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// AccountStatus
// ---------------------------------------------------------------------------

// AccountStatus represents the lifecycle status of an account.
type AccountStatus string

const (
	AccountStatusCurrent    AccountStatus = "current"
	AccountStatusDelinquent AccountStatus = "delinquent"
	AccountStatusChargedOff AccountStatus = "charged_off"
	AccountStatusSettled    AccountStatus = "settled"
	AccountStatusClosed     AccountStatus = "closed"
)

// Valid returns true if s is a recognised account status.
func (s AccountStatus) Valid() bool {
	switch s {
	case AccountStatusCurrent, AccountStatusDelinquent, AccountStatusChargedOff, AccountStatusSettled, AccountStatusClosed:
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// ConsentStatus
// ---------------------------------------------------------------------------

// ConsentStatus represents a consumer's consent state.
type ConsentStatus string

const (
	ConsentGranted ConsentStatus = "granted"
	ConsentRevoked ConsentStatus = "revoked"
)

// Valid returns true if s is a recognised consent status.
func (s ConsentStatus) Valid() bool {
	switch s {
	case ConsentGranted, ConsentRevoked:
		return true
	}
	return false
}
