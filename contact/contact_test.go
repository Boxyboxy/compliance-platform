package contact

import (
	"context"
	"encoding/json"
	"testing"

	"compliance-platform/consumer"
	"compliance-platform/internal/domain"
)

func newSvc() *Service { return &Service{} }

func TestListContacts(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	// Seed contact attempts directly via DB.
	var id1, id2 int64
	err := db.QueryRow(ctx, `
		INSERT INTO contact_attempts (consumer_id, account_id, channel, message_content)
		VALUES (9001, 8001, 'sms', 'Hello from test 1')
		RETURNING id
	`).Scan(&id1)
	if err != nil {
		t.Fatalf("seed row 1: %v", err)
	}
	err = db.QueryRow(ctx, `
		INSERT INTO contact_attempts (consumer_id, account_id, channel, message_content)
		VALUES (9001, 8001, 'email', 'Hello from test 2')
		RETURNING id
	`).Scan(&id2)
	if err != nil {
		t.Fatalf("seed row 2: %v", err)
	}

	tests := []struct {
		name       string
		consumerID int64
		wantCount  int
	}{
		{
			name:       "consumer with contacts",
			consumerID: 9001,
			wantCount:  2,
		},
		{
			name:       "consumer with no contacts",
			consumerID: 99999,
			wantCount:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.ListContacts(ctx, tt.consumerID)
			if err != nil {
				t.Fatalf("ListContacts() error = %v", err)
			}
			if len(got.Contacts) < tt.wantCount {
				t.Errorf("got %d contacts, want at least %d", len(got.Contacts), tt.wantCount)
			}
			// Verify DESC ordering: most recent first.
			if len(got.Contacts) >= 2 {
				if got.Contacts[0].AttemptedAt.Before(got.Contacts[1].AttemptedAt) {
					t.Error("contacts not in DESC order by attempted_at")
				}
			}
		})
	}
}

func TestUpdateContactResult(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	// Seed a pending contact attempt.
	var attemptID int64
	err := db.QueryRow(ctx, `
		INSERT INTO contact_attempts (consumer_id, account_id, channel, message_content)
		VALUES (9002, 8002, 'voice', 'Test message')
		RETURNING id
	`).Scan(&attemptID)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	tests := []struct {
		name   string
		id     int64
		req    *UpdateContactResultReq
		verify func(t *testing.T)
	}{
		{
			name: "update to delivered",
			id:   attemptID,
			req: &UpdateContactResultReq{
				Status:           "delivered",
				MessageContent:   "Sanitized message",
				ComplianceResult: json.RawMessage(`{"allowed":true,"violations":[]}`),
				ScorecardResult:  json.RawMessage(`{"total_score":8,"max_score":10}`),
			},
			verify: func(t *testing.T) {
				var status, content string
				err := db.QueryRow(ctx, `SELECT status, message_content FROM contact_attempts WHERE id = $1`, attemptID).
					Scan(&status, &content)
				if err != nil {
					t.Fatalf("verify query: %v", err)
				}
				if status != "delivered" {
					t.Errorf("status = %q, want %q", status, "delivered")
				}
				if content != "Sanitized message" {
					t.Errorf("message_content = %q, want %q", content, "Sanitized message")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.UpdateContactResult(ctx, tt.id, tt.req)
			if err != nil {
				t.Fatalf("UpdateContactResult() error = %v", err)
			}
			if tt.verify != nil {
				tt.verify(t)
			}
		})
	}
}

func TestInitiateContact_Validation(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	tests := []struct {
		name    string
		req     *InitiateContactReq
		wantErr bool
	}{
		{
			name:    "missing consumer_id",
			req:     &InitiateContactReq{AccountID: 1, Channel: domain.ChannelSMS, MessageContent: "hi"},
			wantErr: true,
		},
		{
			name:    "missing account_id",
			req:     &InitiateContactReq{ConsumerID: 1, Channel: domain.ChannelSMS, MessageContent: "hi"},
			wantErr: true,
		},
		{
			name:    "invalid channel",
			req:     &InitiateContactReq{ConsumerID: 1, AccountID: 1, Channel: domain.Channel("fax"), MessageContent: "hi"},
			wantErr: true,
		},
		{
			name:    "missing message_content",
			req:     &InitiateContactReq{ConsumerID: 1, AccountID: 1, Channel: domain.ChannelSMS},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.InitiateContact(ctx, tt.req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("InitiateContact() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConsentRevocationBlocksPending(t *testing.T) {
	ctx := context.Background()

	// Seed a pending contact attempt.
	var attemptID int64
	err := db.QueryRow(ctx, `
		INSERT INTO contact_attempts (consumer_id, account_id, channel, status, message_content)
		VALUES (9003, 8003, 'sms', 'pending', 'Test consent block')
		RETURNING id
	`).Scan(&attemptID)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Simulate consent revocation event.
	err = handleConsentChanged(ctx, &consumer.ConsentChangedEvent{
		ConsumerID:    9003,
		ConsentStatus: domain.ConsentRevoked,
		ChangedAt:     "2025-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("handleConsentChanged() error = %v", err)
	}

	// Verify the contact is now blocked.
	var status, blockReason string
	err = db.QueryRow(ctx, `SELECT status, block_reason FROM contact_attempts WHERE id = $1`, attemptID).
		Scan(&status, &blockReason)
	if err != nil {
		t.Fatalf("verify query: %v", err)
	}
	if status != "blocked" {
		t.Errorf("status = %q, want %q", status, "blocked")
	}
	if blockReason != "consent_revoked" {
		t.Errorf("block_reason = %q, want %q", blockReason, "consent_revoked")
	}
}
