package account

import (
	"context"
	"fmt"
	"testing"
)

func newSvc() *Service { return &Service{} }

// TestCreateAccount covers the CreateAccount endpoint with table-driven cases.
func TestCreateAccount(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	tests := []struct {
		name    string
		req     *CreateAccountReq
		wantErr bool
		check   func(t *testing.T, got *Account)
	}{
		{
			name: "valid account with all fields",
			req: &CreateAccountReq{
				ConsumerID:       1001,
				OriginalCreditor: "First National Bank",
				AccountNumber:    "ACCT-0001",
				BalanceDue:       2500.00,
				DaysPastDue:      45,
				Status:           "delinquent",
			},
			check: func(t *testing.T, got *Account) {
				if got.ID == 0 {
					t.Error("expected non-zero ID")
				}
				if got.Status != "delinquent" {
					t.Errorf("Status = %q, want %q", got.Status, "delinquent")
				}
				if got.BalanceDue != 2500.00 {
					t.Errorf("BalanceDue = %v, want %v", got.BalanceDue, 2500.00)
				}
				if got.DaysPastDue != 45 {
					t.Errorf("DaysPastDue = %d, want %d", got.DaysPastDue, 45)
				}
			},
		},
		{
			name: "valid account — status defaults to current",
			req: &CreateAccountReq{
				ConsumerID:       1002,
				OriginalCreditor: "Credit Union",
				AccountNumber:    "ACCT-0002",
				BalanceDue:       150.75,
			},
			check: func(t *testing.T, got *Account) {
				if got.Status != "current" {
					t.Errorf("Status = %q, want %q", got.Status, "current")
				}
				if got.DaysPastDue != 0 {
					t.Errorf("DaysPastDue = %d, want 0", got.DaysPastDue)
				}
			},
		},
		{
			name:    "missing consumer_id",
			req:     &CreateAccountReq{OriginalCreditor: "Bank", AccountNumber: "X", BalanceDue: 100},
			wantErr: true,
		},
		{
			name:    "missing original_creditor",
			req:     &CreateAccountReq{ConsumerID: 1, AccountNumber: "X", BalanceDue: 100},
			wantErr: true,
		},
		{
			name:    "missing account_number",
			req:     &CreateAccountReq{ConsumerID: 1, OriginalCreditor: "Bank", BalanceDue: 100},
			wantErr: true,
		},
		{
			name:    "negative balance_due",
			req:     &CreateAccountReq{ConsumerID: 1, OriginalCreditor: "Bank", AccountNumber: "X", BalanceDue: -50},
			wantErr: true,
		},
		{
			name: "invalid status",
			req: &CreateAccountReq{
				ConsumerID: 1, OriginalCreditor: "Bank", AccountNumber: "X",
				BalanceDue: 100, Status: "overdue",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.CreateAccount(ctx, tt.req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CreateAccount() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

// TestGetAccount covers the GetAccount endpoint.
func TestGetAccount(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	created, err := svc.CreateAccount(ctx, &CreateAccountReq{
		ConsumerID:       2001,
		OriginalCreditor: "Midwest Credit",
		AccountNumber:    "ACCT-GET-001",
		BalanceDue:       800.00,
	})
	if err != nil {
		t.Fatalf("seed CreateAccount: %v", err)
	}

	tests := []struct {
		name    string
		id      int64
		wantErr bool
		check   func(t *testing.T, got *Account)
	}{
		{
			name: "existing account",
			id:   created.ID,
			check: func(t *testing.T, got *Account) {
				if got.ID != created.ID {
					t.Errorf("ID = %d, want %d", got.ID, created.ID)
				}
				if got.AccountNumber != "ACCT-GET-001" {
					t.Errorf("AccountNumber = %q, want %q", got.AccountNumber, "ACCT-GET-001")
				}
				if got.ConsumerID != 2001 {
					t.Errorf("ConsumerID = %d, want %d", got.ConsumerID, 2001)
				}
			},
		},
		{
			name:    "non-existent account",
			id:      999999999,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.GetAccount(ctx, tt.id)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetAccount() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

// TestListAccountsByConsumer covers the ListAccountsByConsumer endpoint.
func TestListAccountsByConsumer(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()
	consumerID := int64(3001)

	// Seed two accounts for consumerID=3001.
	for i := 1; i <= 2; i++ {
		_, err := svc.CreateAccount(ctx, &CreateAccountReq{
			ConsumerID:       consumerID,
			OriginalCreditor: fmt.Sprintf("Creditor-%d", i),
			AccountNumber:    fmt.Sprintf("ACCT-LIST-%03d", i),
			BalanceDue:       float64(i) * 100,
		})
		if err != nil {
			t.Fatalf("seed CreateAccount %d: %v", i, err)
		}
	}

	tests := []struct {
		name       string
		consumerID int64
		wantCount  int
	}{
		{
			name:       "consumer with two accounts",
			consumerID: consumerID,
			wantCount:  2,
		},
		{
			name:       "consumer with no accounts returns empty list",
			consumerID: 9999999,
			wantCount:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list, err := svc.ListAccountsByConsumer(ctx, tt.consumerID)
			if err != nil {
				t.Fatalf("ListAccountsByConsumer() error = %v", err)
			}
			if list == nil {
				t.Fatal("expected non-nil AccountList")
			}
			if len(list.Accounts) != tt.wantCount {
				t.Errorf("len(Accounts) = %d, want %d", len(list.Accounts), tt.wantCount)
			}
		})
	}
}

// TestUpdateAccountStatus covers the UpdateAccountStatus endpoint.
func TestUpdateAccountStatus(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	tests := []struct {
		name      string
		seedReq   *CreateAccountReq
		newStatus string
		wantErr   bool
		check     func(t *testing.T, got *Account)
	}{
		{
			name: "current to delinquent",
			seedReq: &CreateAccountReq{
				ConsumerID: 4001, OriginalCreditor: "Bank A",
				AccountNumber: "ACCT-STATUS-001", BalanceDue: 500,
			},
			newStatus: "delinquent",
			check: func(t *testing.T, got *Account) {
				if got.Status != "delinquent" {
					t.Errorf("Status = %q, want %q", got.Status, "delinquent")
				}
			},
		},
		{
			name: "delinquent to charged_off",
			seedReq: &CreateAccountReq{
				ConsumerID: 4002, OriginalCreditor: "Bank B",
				AccountNumber: "ACCT-STATUS-002", BalanceDue: 1200, Status: "delinquent",
			},
			newStatus: "charged_off",
			check: func(t *testing.T, got *Account) {
				if got.Status != "charged_off" {
					t.Errorf("Status = %q, want %q", got.Status, "charged_off")
				}
			},
		},
		{
			name: "to settled",
			seedReq: &CreateAccountReq{
				ConsumerID: 4003, OriginalCreditor: "Bank C",
				AccountNumber: "ACCT-STATUS-003", BalanceDue: 300, Status: "charged_off",
			},
			newStatus: "settled",
			check: func(t *testing.T, got *Account) {
				if got.Status != "settled" {
					t.Errorf("Status = %q, want %q", got.Status, "settled")
				}
			},
		},
		{
			name: "to closed",
			seedReq: &CreateAccountReq{
				ConsumerID: 4004, OriginalCreditor: "Bank D",
				AccountNumber: "ACCT-STATUS-004", BalanceDue: 0, Status: "settled",
			},
			newStatus: "closed",
			check: func(t *testing.T, got *Account) {
				if got.Status != "closed" {
					t.Errorf("Status = %q, want %q", got.Status, "closed")
				}
			},
		},
		{
			name: "invalid status",
			seedReq: &CreateAccountReq{
				ConsumerID: 4005, OriginalCreditor: "Bank E",
				AccountNumber: "ACCT-STATUS-005", BalanceDue: 100,
			},
			newStatus: "overdue",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account, err := svc.CreateAccount(ctx, tt.seedReq)
			if err != nil {
				t.Fatalf("seed CreateAccount: %v", err)
			}

			got, err := svc.UpdateAccountStatus(ctx, account.ID, &UpdateStatusReq{Status: tt.newStatus})
			if (err != nil) != tt.wantErr {
				t.Fatalf("UpdateAccountStatus() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

// TestUpdateAccountStatus_NotFound verifies a 404 for a missing account.
func TestUpdateAccountStatus_NotFound(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()
	_, err := svc.UpdateAccountStatus(ctx, 999999999, &UpdateStatusReq{Status: "closed"})
	if err == nil {
		t.Fatal("expected error for non-existent account, got nil")
	}
}
