package workflows

import (
	"testing"

	"fmt"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

func baseInput() ContactWorkflowInput {
	return ContactWorkflowInput{
		ContactAttemptID:        1,
		ConsumerID:              100,
		AccountID:               200,
		Channel:                 "sms",
		MessageContent:          "Hello, this is a test message. To opt out, reply STOP.",
		ConsumerTimezone:        "America/New_York",
		ConsumerConsent:         "granted",
		AttorneyOnFile:          false,
		DoNotContact:            false,
		RecentContactTimestamps: []string{},
		CorrelationID:           "test-correlation-123",
	}
}

// registerAllMocks sets up default happy-path mocks for all activities.
// Individual tests can override specific mocks after calling this.
func registerAllMocks(env *testsuite.TestWorkflowEnvironment) {
	env.RegisterActivity(&Activities{})
	env.OnActivity("CheckCompliance", mock.Anything, mock.Anything).
		Return(&ComplianceCheckOutput{Allowed: true, Violations: []ComplianceViolation{}}, nil)

	env.OnActivity("SanitizePII", mock.Anything, mock.Anything).
		Return(&SanitizeOutput{Sanitized: "Hello, this is a test message. To opt out, reply STOP.", Redacted: false}, nil)

	env.OnActivity("SimulateDelivery", mock.Anything, mock.Anything).
		Return(&DeliveryResult{Delivered: true, Status: "delivered"}, nil)

	env.OnActivity("ScoreInteraction", mock.Anything, mock.Anything).
		Return(&ScoreOutput{TotalScore: 7, MaxScore: 10, Percentage: 70.0, RequiredPassed: true}, nil)

	env.OnActivity("RecordContactResult", mock.Anything, mock.Anything).
		Return(nil)

	env.OnActivity("PublishContactAttempted", mock.Anything, mock.Anything).
		Return(nil)

	env.OnActivity("PublishInteractionCreated", mock.Anything, mock.Anything).
		Return(nil)
}

func TestContactWorkflow_HappyPath(t *testing.T) {
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	registerAllMocks(env)

	input := baseInput()
	env.ExecuteWorkflow(ContactWorkflow, input)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result ContactWorkflowResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "delivered", result.Status)
	require.True(t, result.Allowed)
	require.Equal(t, int64(1), result.ContactAttemptID)
}

func TestContactWorkflow_ComplianceBlocked(t *testing.T) {
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{})

	// Override: compliance returns blocked.
	env.OnActivity("CheckCompliance", mock.Anything, mock.Anything).
		Return(&ComplianceCheckOutput{
			Allowed: false,
			Violations: []ComplianceViolation{
				{Rule: "attorney_block", Details: "Attorney on file"},
			},
		}, nil)

	env.OnActivity("RecordContactResult", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("PublishContactAttempted", mock.Anything, mock.Anything).Return(nil)

	input := baseInput()
	input.AttorneyOnFile = true
	env.ExecuteWorkflow(ContactWorkflow, input)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result ContactWorkflowResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "blocked", result.Status)
	require.False(t, result.Allowed)
	require.Equal(t, "attorney_block", result.BlockReason)
}

func TestContactWorkflow_DeliveryFailure(t *testing.T) {
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{})

	env.OnActivity("CheckCompliance", mock.Anything, mock.Anything).
		Return(&ComplianceCheckOutput{Allowed: true, Violations: []ComplianceViolation{}}, nil)

	env.OnActivity("SanitizePII", mock.Anything, mock.Anything).
		Return(&SanitizeOutput{Sanitized: "Clean message", Redacted: false}, nil)

	// Delivery fails.
	env.OnActivity("SimulateDelivery", mock.Anything, mock.Anything).
		Return(&DeliveryResult{Delivered: false, Status: "failed"}, nil)

	env.OnActivity("ScoreInteraction", mock.Anything, mock.Anything).
		Return(&ScoreOutput{TotalScore: 5, MaxScore: 10, Percentage: 50.0, RequiredPassed: false}, nil)

	env.OnActivity("RecordContactResult", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("PublishContactAttempted", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("PublishInteractionCreated", mock.Anything, mock.Anything).Return(nil)

	input := baseInput()
	input.ContactAttemptID = 10
	env.ExecuteWorkflow(ContactWorkflow, input)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result ContactWorkflowResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "failed", result.Status)
	require.True(t, result.Allowed)
}

func TestContactWorkflow_ComplianceActivityError(t *testing.T) {
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{})

	env.OnActivity("CheckCompliance", mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("compliance service unavailable"))

	input := baseInput()
	env.ExecuteWorkflow(ContactWorkflow, input)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
}

func TestContactWorkflow_ScorecardIncludedInResult(t *testing.T) {
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{})

	env.OnActivity("CheckCompliance", mock.Anything, mock.Anything).
		Return(&ComplianceCheckOutput{Allowed: true, Violations: []ComplianceViolation{}}, nil)

	env.OnActivity("SanitizePII", mock.Anything, mock.Anything).
		Return(&SanitizeOutput{Sanitized: "Sanitized content", Redacted: true}, nil)

	env.OnActivity("SimulateDelivery", mock.Anything, mock.Anything).
		Return(&DeliveryResult{Delivered: true, Status: "delivered"}, nil)

	env.OnActivity("ScoreInteraction", mock.Anything, mock.Anything).
		Return(&ScoreOutput{TotalScore: 8, MaxScore: 10, Percentage: 80.0, RequiredPassed: true}, nil)

	env.OnActivity("RecordContactResult", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("PublishContactAttempted", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("PublishInteractionCreated", mock.Anything, mock.Anything).Return(nil)

	input := baseInput()
	input.ContactAttemptID = 5
	env.ExecuteWorkflow(ContactWorkflow, input)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result ContactWorkflowResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "delivered", result.Status)
	require.Equal(t, "Sanitized content", result.MessageContent)
}
