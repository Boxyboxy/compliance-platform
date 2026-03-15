package payment

import (
	"context"
	"testing"
)

func newSvc() *Service { return &Service{} }

// TestProposePlan covers the ProposePlan endpoint with table-driven cases.
func TestProposePlan(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	tests := []struct {
		name    string
		req     *ProposePlanReq
		wantErr bool
		check   func(t *testing.T, got *PaymentPlan)
	}{
		{
			name: "valid proposal",
			req: &ProposePlanReq{
				AccountID:       1001,
				TotalAmount:     1000,
				NumInstallments: 4,
				InstallmentAmt:  250,
			},
			check: func(t *testing.T, got *PaymentPlan) {
				if got.ID == 0 {
					t.Error("expected non-zero ID")
				}
				if got.Status != "proposed" {
					t.Errorf("Status = %q, want %q", got.Status, "proposed")
				}
				if got.Frequency != "monthly" {
					t.Errorf("Frequency = %q, want %q", got.Frequency, "monthly")
				}
				if got.TotalAmount != 1000 {
					t.Errorf("TotalAmount = %v, want %v", got.TotalAmount, 1000.0)
				}
				if got.NumInstallments != 4 {
					t.Errorf("NumInstallments = %d, want %d", got.NumInstallments, 4)
				}
			},
		},
		{
			name: "valid proposal with weekly frequency",
			req: &ProposePlanReq{
				AccountID:       1002,
				TotalAmount:     500,
				NumInstallments: 10,
				InstallmentAmt:  50,
				Frequency:       "weekly",
			},
			check: func(t *testing.T, got *PaymentPlan) {
				if got.Frequency != "weekly" {
					t.Errorf("Frequency = %q, want %q", got.Frequency, "weekly")
				}
			},
		},
		{
			name:    "missing account_id",
			req:     &ProposePlanReq{TotalAmount: 1000, NumInstallments: 4, InstallmentAmt: 250},
			wantErr: true,
		},
		{
			name:    "zero total_amount",
			req:     &ProposePlanReq{AccountID: 1, TotalAmount: 0, NumInstallments: 4, InstallmentAmt: 250},
			wantErr: true,
		},
		{
			name:    "invalid frequency",
			req:     &ProposePlanReq{AccountID: 1, TotalAmount: 1000, NumInstallments: 4, InstallmentAmt: 250, Frequency: "yearly"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.ProposePlan(ctx, tt.req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ProposePlan() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

// TestAcceptPlan covers the AcceptPlan endpoint.
func TestAcceptPlan(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	// Seed a proposed plan.
	proposed, err := svc.ProposePlan(ctx, &ProposePlanReq{
		AccountID:       2001,
		TotalAmount:     600,
		NumInstallments: 3,
		InstallmentAmt:  200,
	})
	if err != nil {
		t.Fatalf("seed ProposePlan: %v", err)
	}

	tests := []struct {
		name    string
		id      int64
		wantErr bool
		check   func(t *testing.T, got *PaymentPlan)
	}{
		{
			name: "accept proposed plan",
			id:   proposed.ID,
			check: func(t *testing.T, got *PaymentPlan) {
				if got.Status != "accepted" {
					t.Errorf("Status = %q, want %q", got.Status, "accepted")
				}
				if got.AcceptedAt == nil {
					t.Error("expected non-nil AcceptedAt")
				}
			},
		},
		{
			name:    "accept already accepted plan",
			id:      proposed.ID,
			wantErr: true,
		},
		{
			name:    "accept non-existent plan",
			id:      999999999,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.AcceptPlan(ctx, tt.id)
			if (err != nil) != tt.wantErr {
				t.Fatalf("AcceptPlan() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

// TestRecordPayment covers the RecordPayment endpoint.
func TestRecordPayment(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	// Seed and accept a plan (total=600, 3 installments of 200).
	plan, err := svc.ProposePlan(ctx, &ProposePlanReq{
		AccountID:       3001,
		TotalAmount:     600,
		NumInstallments: 3,
		InstallmentAmt:  200,
	})
	if err != nil {
		t.Fatalf("seed ProposePlan: %v", err)
	}
	plan, err = svc.AcceptPlan(ctx, plan.ID)
	if err != nil {
		t.Fatalf("seed AcceptPlan: %v", err)
	}

	// Seed a proposed-only plan for the error case.
	proposedOnly, err := svc.ProposePlan(ctx, &ProposePlanReq{
		AccountID:       3002,
		TotalAmount:     100,
		NumInstallments: 1,
		InstallmentAmt:  100,
	})
	if err != nil {
		t.Fatalf("seed ProposePlan (proposed-only): %v", err)
	}

	t.Run("first payment transitions accepted to active", func(t *testing.T) {
		ev, err := svc.RecordPayment(ctx, plan.ID, &RecordPaymentReq{Amount: 200})
		if err != nil {
			t.Fatalf("RecordPayment() error = %v", err)
		}
		if ev.EventType != "payment_received" {
			t.Errorf("EventType = %q, want %q", ev.EventType, "payment_received")
		}

		// Verify plan is now active.
		got, err := svc.GetPlan(ctx, plan.ID)
		if err != nil {
			t.Fatalf("GetPlan() error = %v", err)
		}
		if got.Status != "active" {
			t.Errorf("Status = %q, want %q", got.Status, "active")
		}
	})

	t.Run("second payment keeps active", func(t *testing.T) {
		_, err := svc.RecordPayment(ctx, plan.ID, &RecordPaymentReq{Amount: 200})
		if err != nil {
			t.Fatalf("RecordPayment() error = %v", err)
		}

		got, err := svc.GetPlan(ctx, plan.ID)
		if err != nil {
			t.Fatalf("GetPlan() error = %v", err)
		}
		if got.Status != "active" {
			t.Errorf("Status = %q, want %q", got.Status, "active")
		}
	})

	t.Run("final payment triggers completed", func(t *testing.T) {
		_, err := svc.RecordPayment(ctx, plan.ID, &RecordPaymentReq{Amount: 200})
		if err != nil {
			t.Fatalf("RecordPayment() error = %v", err)
		}

		got, err := svc.GetPlan(ctx, plan.ID)
		if err != nil {
			t.Fatalf("GetPlan() error = %v", err)
		}
		if got.Status != "completed" {
			t.Errorf("Status = %q, want %q", got.Status, "completed")
		}
		if got.CompletedAt == nil {
			t.Error("expected non-nil CompletedAt")
		}
	})

	t.Run("payment on proposed plan fails", func(t *testing.T) {
		_, err := svc.RecordPayment(ctx, proposedOnly.ID, &RecordPaymentReq{Amount: 100})
		if err == nil {
			t.Fatal("expected error for payment on proposed plan, got nil")
		}
	})
}

// TestGetPlan covers the GetPlan endpoint.
func TestGetPlan(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	created, err := svc.ProposePlan(ctx, &ProposePlanReq{
		AccountID:       4001,
		TotalAmount:     500,
		NumInstallments: 5,
		InstallmentAmt:  100,
	})
	if err != nil {
		t.Fatalf("seed ProposePlan: %v", err)
	}

	tests := []struct {
		name    string
		id      int64
		wantErr bool
		check   func(t *testing.T, got *PaymentPlan)
	}{
		{
			name: "existing plan",
			id:   created.ID,
			check: func(t *testing.T, got *PaymentPlan) {
				if got.ID != created.ID {
					t.Errorf("ID = %d, want %d", got.ID, created.ID)
				}
				if got.TotalAmount != 500 {
					t.Errorf("TotalAmount = %v, want %v", got.TotalAmount, 500.0)
				}
			},
		},
		{
			name:    "non-existent plan",
			id:      999999999,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.GetPlan(ctx, tt.id)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetPlan() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}
