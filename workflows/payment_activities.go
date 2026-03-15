package workflows

import (
	"context"
	"fmt"
	"log"
	"net/http"
)

// MarkPlanDefaulted calls PATCH /payment-plans/:id/default via HTTP.
func (a *Activities) MarkPlanDefaulted(ctx context.Context, input MarkPlanInput) error {
	log.Printf("[activity] MarkPlanDefaulted plan_id=%d correlation_id=%s", input.PlanID, input.CorrelationID)
	return a.doRequest(ctx, http.MethodPatch, fmt.Sprintf("/payment-plans/%d/default", input.PlanID), nil, nil)
}

// MarkPlanCompleted calls PATCH /payment-plans/:id/complete via HTTP.
func (a *Activities) MarkPlanCompleted(ctx context.Context, input MarkPlanInput) error {
	log.Printf("[activity] MarkPlanCompleted plan_id=%d correlation_id=%s", input.PlanID, input.CorrelationID)
	return a.doRequest(ctx, http.MethodPatch, fmt.Sprintf("/payment-plans/%d/complete", input.PlanID), nil, nil)
}
