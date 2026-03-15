package audit

import (
	"encoding/json"
	"time"
)

// AuditEntry is a single row in the append-only audit log.
type AuditEntry struct {
	ID         int64           `json:"id"`
	EntityType string          `json:"entity_type"`
	EntityID   int64           `json:"entity_id"`
	Action     string          `json:"action"`
	Actor      string          `json:"actor"`
	OldValue   json.RawMessage `json:"old_value,omitempty"`
	NewValue   json.RawMessage `json:"new_value,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// RecordAuditReq is the request body for POST /audit/record (private).
type RecordAuditReq struct {
	EntityType string          `json:"entity_type"`
	EntityID   int64           `json:"entity_id"`
	Action     string          `json:"action"`
	Actor      string          `json:"actor"`
	OldValue   json.RawMessage `json:"old_value,omitempty"`
	NewValue   json.RawMessage `json:"new_value,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

// AuditLogList wraps a slice of audit entries for list endpoints.
type AuditLogList struct {
	Entries []*AuditEntry `json:"entries"`
}

// GetAuditLogParams holds filter parameters for audit log queries.
// Used by POST /audit/search (body) and internally by GET /audit/:entityType/:entityId.
type GetAuditLogParams struct {
	EntityType string `json:"entity_type"`
	EntityId   int64  `json:"entity_id"`
	Action     string `json:"action,omitempty"`
	Since      string `json:"since,omitempty"` // RFC3339
	Until      string `json:"until,omitempty"` // RFC3339
}
