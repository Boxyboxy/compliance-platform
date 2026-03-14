package consumer

import (
	"context"
	"fmt"
	"testing"

	"compliance-platform/internal/domain"
)

// newSvc returns a zero-value Service for testing.
// Encore injects a real test database when running with `encore test`.
func newSvc() *Service { return &Service{} }

// TestCreateConsumer covers the CreateConsumer endpoint with table-driven cases.
func TestCreateConsumer(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	tests := []struct {
		name    string
		req     *CreateConsumerReq
		wantErr bool
		check   func(t *testing.T, got *Consumer)
	}{
		{
			name: "valid consumer with all fields",
			req: &CreateConsumerReq{
				ExternalID: "ext-consumer-001",
				FirstName:  "Jane",
				LastName:   "Doe",
				Phone:      "+18005550100",
				Email:      "jane.doe@example.com",
				Timezone:   "America/Los_Angeles",
			},
			check: func(t *testing.T, got *Consumer) {
				if got.ID == 0 {
					t.Error("expected non-zero ID")
				}
				if got.ConsentStatus != "granted" {
					t.Errorf("ConsentStatus = %q, want %q", got.ConsentStatus, "granted")
				}
				if got.DoNotContact {
					t.Error("DoNotContact should default to false")
				}
				if got.AttorneyOnFile {
					t.Error("AttorneyOnFile should default to false")
				}
				if got.Timezone != "America/Los_Angeles" {
					t.Errorf("Timezone = %q, want %q", got.Timezone, "America/Los_Angeles")
				}
			},
		},
		{
			name: "valid consumer minimal fields — timezone defaults to America/New_York",
			req: &CreateConsumerReq{
				ExternalID: "ext-consumer-002",
				FirstName:  "John",
				LastName:   "Smith",
			},
			check: func(t *testing.T, got *Consumer) {
				if got.Timezone != "America/New_York" {
					t.Errorf("Timezone = %q, want %q", got.Timezone, "America/New_York")
				}
				if got.Phone != "" {
					t.Errorf("Phone = %q, want empty", got.Phone)
				}
			},
		},
		{
			name:    "missing external_id",
			req:     &CreateConsumerReq{FirstName: "John", LastName: "Smith"},
			wantErr: true,
		},
		{
			name:    "missing first_name",
			req:     &CreateConsumerReq{ExternalID: "ext-consumer-003", LastName: "Smith"},
			wantErr: true,
		},
		{
			name:    "missing last_name",
			req:     &CreateConsumerReq{ExternalID: "ext-consumer-004", FirstName: "John"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.CreateConsumer(ctx, tt.req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CreateConsumer() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

// TestCreateConsumerDuplicateExternalID verifies the unique constraint on external_id.
func TestCreateConsumerDuplicateExternalID(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()
	req := &CreateConsumerReq{
		ExternalID: "ext-dup-001",
		FirstName:  "Alice",
		LastName:   "Wonder",
	}
	if _, err := svc.CreateConsumer(ctx, req); err != nil {
		t.Fatalf("first insert failed: %v", err)
	}
	if _, err := svc.CreateConsumer(ctx, req); err == nil {
		t.Fatal("expected error on duplicate external_id, got nil")
	}
}

// TestGetConsumer covers the GetConsumer endpoint.
func TestGetConsumer(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	// Seed a consumer to retrieve.
	created, err := svc.CreateConsumer(ctx, &CreateConsumerReq{
		ExternalID: "ext-get-001",
		FirstName:  "Bob",
		LastName:   "Builder",
		Timezone:   "America/Chicago",
	})
	if err != nil {
		t.Fatalf("seed CreateConsumer: %v", err)
	}

	tests := []struct {
		name    string
		id      int64
		wantErr bool
		check   func(t *testing.T, got *Consumer)
	}{
		{
			name: "existing consumer",
			id:   created.ID,
			check: func(t *testing.T, got *Consumer) {
				if got.ID != created.ID {
					t.Errorf("ID = %d, want %d", got.ID, created.ID)
				}
				if got.ExternalID != "ext-get-001" {
					t.Errorf("ExternalID = %q, want %q", got.ExternalID, "ext-get-001")
				}
				if got.FirstName != "Bob" {
					t.Errorf("FirstName = %q, want %q", got.FirstName, "Bob")
				}
			},
		},
		{
			name:    "non-existent consumer",
			id:      999999999,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.GetConsumer(ctx, tt.id)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetConsumer() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

// TestUpdateConsent covers the UpdateConsent endpoint.
func TestUpdateConsent(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	tests := []struct {
		name          string
		externalID    string
		consentStatus domain.ConsentStatus
		wantErr       bool
		check         func(t *testing.T, got *Consumer)
	}{
		{
			name:          "revoke consent",
			externalID:    "ext-consent-001",
			consentStatus: domain.ConsentRevoked,
			check: func(t *testing.T, got *Consumer) {
				if got.ConsentStatus != "revoked" {
					t.Errorf("ConsentStatus = %q, want %q", got.ConsentStatus, "revoked")
				}
			},
		},
		{
			name:          "grant consent",
			externalID:    "ext-consent-002",
			consentStatus: domain.ConsentGranted,
			check: func(t *testing.T, got *Consumer) {
				if got.ConsentStatus != "granted" {
					t.Errorf("ConsentStatus = %q, want %q", got.ConsentStatus, "granted")
				}
			},
		},
		{
			name:          "invalid consent status",
			externalID:    "ext-consent-003",
			consentStatus: domain.ConsentStatus("unknown"),
			wantErr:       true,
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Each subtest creates its own consumer so updates don't interfere.
			c, err := svc.CreateConsumer(ctx, &CreateConsumerReq{
				ExternalID: fmt.Sprintf("%s-%d", tt.externalID, i),
				FirstName:  "Test",
				LastName:   "User",
			})
			if err != nil {
				t.Fatalf("seed CreateConsumer: %v", err)
			}

			got, err := svc.UpdateConsent(ctx, c.ID, &UpdateConsentReq{
				ConsentStatus: tt.consentStatus,
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("UpdateConsent() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

// TestUpdateConsent_NotFound verifies a 404 is returned for a missing consumer.
func TestUpdateConsent_NotFound(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()
	_, err := svc.UpdateConsent(ctx, 999999999, &UpdateConsentReq{ConsentStatus: "revoked"})
	if err == nil {
		t.Fatal("expected error for non-existent consumer, got nil")
	}
}
