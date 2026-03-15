package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"encore.dev/beta/errs"
	"encore.dev/rlog"
	"encore.dev/storage/sqldb"
)

// db is the Encore-managed PostgreSQL database for the audit service.
var db = sqldb.NewDatabase("audit", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})

// Service is the Encore service for audit logging.
//
//encore:service
type Service struct{}

// RecordAudit inserts an entry into the append-only audit log.
//
//encore:api private method=POST path=/audit/record
func (s *Service) RecordAudit(ctx context.Context, req *RecordAuditReq) (*AuditEntry, error) {
	return recordAuditEntry(ctx, req)
}

// recordAuditEntry is the shared implementation for recording audit entries.
// Used by both the RecordAudit API handler and Pub/Sub subscribers.
func recordAuditEntry(ctx context.Context, req *RecordAuditReq) (*AuditEntry, error) {
	if req.EntityType == "" {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "entity_type is required"}
	}
	if req.EntityID == 0 {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "entity_id is required"}
	}
	if req.Action == "" {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "action is required"}
	}
	if req.Actor == "" {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "actor is required"}
	}

	var entry AuditEntry
	var oldValue, newValue, metadata []byte

	err := db.QueryRow(ctx, `
		INSERT INTO audit_log (entity_type, entity_id, action, actor, old_value, new_value, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, entity_type, entity_id, action, actor, old_value, new_value, metadata, created_at
	`, req.EntityType, req.EntityID, req.Action, req.Actor,
		jsonOrNull(req.OldValue), jsonOrNull(req.NewValue), jsonOrNull(req.Metadata),
	).Scan(&entry.ID, &entry.EntityType, &entry.EntityID, &entry.Action, &entry.Actor,
		&oldValue, &newValue, &metadata, &entry.CreatedAt)
	if err != nil {
		rlog.Error("failed to record audit entry",
			"service", "audit",
			"entity_type", req.EntityType,
			"entity_id", req.EntityID,
			"err", err)
		return nil, fmt.Errorf("recording audit entry: %w", err)
	}

	if oldValue != nil {
		entry.OldValue = json.RawMessage(oldValue)
	}
	if newValue != nil {
		entry.NewValue = json.RawMessage(newValue)
	}
	if metadata != nil {
		entry.Metadata = json.RawMessage(metadata)
	}

	rlog.Info("audit entry recorded",
		"service", "audit",
		"id", entry.ID,
		"entity_type", entry.EntityType,
		"entity_id", entry.EntityID,
		"action", entry.Action)
	return &entry, nil
}

// GetAuditLog retrieves audit entries for a given entity.
//
//encore:api public method=GET path=/audit/:entityType/:entityId
func (s *Service) GetAuditLog(ctx context.Context, entityType string, entityId int64) (*AuditLogList, error) {
	return queryAuditLog(ctx, &GetAuditLogParams{
		EntityType: entityType,
		EntityId:   entityId,
	})
}

// SearchAuditLog retrieves filtered audit entries.
// Supports optional filters: action, since (RFC3339), until (RFC3339).
//
//encore:api public method=POST path=/audit/search
func (s *Service) SearchAuditLog(ctx context.Context, params *GetAuditLogParams) (*AuditLogList, error) {
	return queryAuditLog(ctx, params)
}

// queryAuditLog is the shared query implementation for GetAuditLog and SearchAuditLog.
func queryAuditLog(ctx context.Context, params *GetAuditLogParams) (*AuditLogList, error) {
	rlog.Debug("audit log lookup",
		"service", "audit",
		"entity_type", params.EntityType,
		"entity_id", params.EntityId)

	query := `
		SELECT id, entity_type, entity_id, action, actor, old_value, new_value, metadata, created_at
		FROM audit_log
		WHERE entity_type = $1 AND entity_id = $2`
	args := []interface{}{params.EntityType, params.EntityId}
	argIdx := 3

	if params.Action != "" {
		query += fmt.Sprintf(" AND action = $%d", argIdx)
		args = append(args, params.Action)
		argIdx++
	}
	if params.Since != "" {
		sinceTime, err := time.Parse(time.RFC3339, params.Since)
		if err != nil {
			return nil, &errs.Error{Code: errs.InvalidArgument, Message: "since must be RFC3339 format"}
		}
		query += fmt.Sprintf(" AND created_at >= $%d", argIdx)
		args = append(args, sinceTime)
		argIdx++
	}
	if params.Until != "" {
		untilTime, err := time.Parse(time.RFC3339, params.Until)
		if err != nil {
			return nil, &errs.Error{Code: errs.InvalidArgument, Message: "until must be RFC3339 format"}
		}
		query += fmt.Sprintf(" AND created_at <= $%d", argIdx)
		args = append(args, untilTime)
		argIdx++
	}

	query += " ORDER BY created_at DESC"

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying audit log: %w", err)
	}
	defer rows.Close()

	var entries []*AuditEntry
	for rows.Next() {
		var e AuditEntry
		var oldValue, newValue, metadata []byte
		if err := rows.Scan(&e.ID, &e.EntityType, &e.EntityID, &e.Action, &e.Actor,
			&oldValue, &newValue, &metadata, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning audit entry: %w", err)
		}
		if oldValue != nil {
			e.OldValue = json.RawMessage(oldValue)
		}
		if newValue != nil {
			e.NewValue = json.RawMessage(newValue)
		}
		if metadata != nil {
			e.Metadata = json.RawMessage(metadata)
		}
		entries = append(entries, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating audit entries: %w", err)
	}

	if entries == nil {
		entries = []*AuditEntry{}
	}
	return &AuditLogList{Entries: entries}, nil
}

func jsonOrNull(data json.RawMessage) interface{} {
	if len(data) == 0 {
		return nil
	}
	return []byte(data)
}
