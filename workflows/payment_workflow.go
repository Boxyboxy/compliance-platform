package workflows

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// PaymentPlanWorkflow orchestrates the lifecycle of a payment plan.
// It waits for an acceptance signal, then tracks installment payments
// with grace periods, marking the plan as defaulted or completed accordingly.
func PaymentPlanWorkflow(ctx workflow.Context, input PaymentPlanInput) error {
	activityOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
			InitialInterval: 1 * time.Second,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, activityOpts)

	var activities *Activities

	// Step 1: Wait for "accept" signal within 72 hours.
	acceptCh := workflow.GetSignalChannel(ctx, "accept")
	acceptCtx, cancelAccept := workflow.WithCancel(ctx)
	defer cancelAccept()

	timerCtx, cancelTimer := workflow.WithCancel(ctx)
	defer cancelTimer()

	accepted := false
	selector := workflow.NewSelector(acceptCtx)

	selector.AddReceive(acceptCh, func(ch workflow.ReceiveChannel, more bool) {
		ch.Receive(acceptCtx, nil)
		accepted = true
		cancelTimer()
	})

	selector.AddFuture(workflow.NewTimer(timerCtx, 72*time.Hour), func(f workflow.Future) {
		cancelAccept()
	})

	selector.Select(ctx)

	if !accepted {
		// Timed out waiting for acceptance — mark as defaulted.
		return workflow.ExecuteActivity(ctx, activities.MarkPlanDefaulted, MarkPlanInput{
			PlanID: input.PlanID,
		}).Get(ctx, nil)
	}

	// Step 2: Track installment payments.
	var interval time.Duration
	switch input.Frequency {
	case "weekly":
		interval = 7 * 24 * time.Hour
	case "biweekly":
		interval = 14 * 24 * time.Hour
	default: // monthly
		interval = 30 * 24 * time.Hour
	}

	gracePeriod := 3 * 24 * time.Hour
	missedCount := 0

	for i := 0; i < input.NumInstallments; i++ {
		// Wait for the installment interval.
		if err := workflow.Sleep(ctx, interval); err != nil {
			return err
		}

		// Wait for "payment_received" signal with grace period.
		paymentCh := workflow.GetSignalChannel(ctx, "payment_received")
		payCtx, cancelPay := workflow.WithCancel(ctx)

		graceCtx, cancelGrace := workflow.WithCancel(ctx)

		received := false
		paySel := workflow.NewSelector(payCtx)

		paySel.AddReceive(paymentCh, func(ch workflow.ReceiveChannel, more bool) {
			ch.Receive(payCtx, nil)
			received = true
			cancelGrace()
		})

		paySel.AddFuture(workflow.NewTimer(graceCtx, gracePeriod), func(f workflow.Future) {
			cancelPay()
		})

		paySel.Select(ctx)

		// Clean up contexts.
		cancelPay()
		cancelGrace()

		if !received {
			missedCount++
			if missedCount >= 3 {
				return workflow.ExecuteActivity(ctx, activities.MarkPlanDefaulted, MarkPlanInput{
					PlanID: input.PlanID,
				}).Get(ctx, nil)
			}
		}
	}

	// All installments tracked — mark as completed.
	return workflow.ExecuteActivity(ctx, activities.MarkPlanCompleted, MarkPlanInput{
		PlanID: input.PlanID,
	}).Get(ctx, nil)
}
