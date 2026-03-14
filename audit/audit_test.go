package audit

import (
	"context"
	"encoding/json"
	"testing"
)

func newSvc() *Service { return &Service{} }

func TestRecordAudit(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	tests := []struct {
		name    string
		req     *RecordAuditReq
		wantErr bool
		check   func(t *testing.T, got *AuditEntry)
	}{
		{
			name: "valid entry with all fields",
			req: &RecordAuditReq{
				EntityType: "consumer",
				EntityID:   100,
				Action:     "created",
				Actor:      "api",
				NewValue:   json.RawMessage(`{"first_name":"Jane"}`),
				Metadata:   json.RawMessage(`{"correlation_id":"abc-123"}`),
			},
			check: func(t *testing.T, got *AuditEntry) {
				if got.ID == 0 {
					t.Error("expected non-zero ID")
				}
				if got.EntityType != "consumer" {
					t.Errorf("EntityType = %q, want %q", got.EntityType, "consumer")
				}
				if got.Action != "created" {
					t.Errorf("Action = %q, want %q", got.Action, "created")
				}
			},
		},
		{
			name: "valid entry minimal fields",
			req: &RecordAuditReq{
				EntityType: "account",
				EntityID:   200,
				Action:     "status_updated",
				Actor:      "system",
			},
			check: func(t *testing.T, got *AuditEntry) {
				if got.EntityType != "account" {
					t.Errorf("EntityType = %q, want %q", got.EntityType, "account")
				}
			},
		},
		{
			name: "missing entity_type",
			req: &RecordAuditReq{
				EntityID: 100,
				Action:   "created",
				Actor:    "api",
			},
			wantErr: true,
		},
		{
			name: "missing entity_id",
			req: &RecordAuditReq{
				EntityType: "consumer",
				Action:     "created",
				Actor:      "api",
			},
			wantErr: true,
		},
		{
			name: "missing action",
			req: &RecordAuditReq{
				EntityType: "consumer",
				EntityID:   100,
				Actor:      "api",
			},
			wantErr: true,
		},
		{
			name: "missing actor",
			req: &RecordAuditReq{
				EntityType: "consumer",
				EntityID:   100,
				Action:     "created",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.RecordAudit(ctx, tt.req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("RecordAudit() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

func TestGetAuditLog(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	// Seed entries.
	_, err := svc.RecordAudit(ctx, &RecordAuditReq{
		EntityType: "contact",
		EntityID:   300,
		Action:     "contact_attempted",
		Actor:      "system:pubsub",
		NewValue:   json.RawMessage(`{"status":"blocked"}`),
	})
	if err != nil {
		t.Fatalf("seed 1: %v", err)
	}
	_, err = svc.RecordAudit(ctx, &RecordAuditReq{
		EntityType: "contact",
		EntityID:   300,
		Action:     "interaction_created",
		Actor:      "system:pubsub",
	})
	if err != nil {
		t.Fatalf("seed 2: %v", err)
	}

	tests := []struct {
		name       string
		entityType string
		entityID   int64
		wantMin    int
	}{
		{
			name:       "entity with entries",
			entityType: "contact",
			entityID:   300,
			wantMin:    2,
		},
		{
			name:       "entity with no entries",
			entityType: "contact",
			entityID:   999999,
			wantMin:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.GetAuditLog(ctx, tt.entityType, tt.entityID)
			if err != nil {
				t.Fatalf("GetAuditLog() error = %v", err)
			}
			if len(got.Entries) < tt.wantMin {
				t.Errorf("got %d entries, want at least %d", len(got.Entries), tt.wantMin)
			}
			// Verify DESC ordering.
			if len(got.Entries) >= 2 {
				if got.Entries[0].CreatedAt.Before(got.Entries[1].CreatedAt) {
					t.Error("entries not in DESC order by created_at")
				}
			}
		})
	}
}
