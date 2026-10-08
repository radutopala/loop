package events

import "fmt"

// planApprovePrompt is the message inserted when the user approves a plan.
// Kept short and explicit so the agent treats it as a continuation of the
// prior turn rather than a fresh task.
const planApprovePrompt = "I approve the plan. Please proceed with the implementation."

// PlanApprovePrompt returns the message inserted when the user approves a
// plan. When the plan recorded the file it was written to, the prompt names
// that path so the agent re-reads the exact plan file rather than relying on
// conversation memory; otherwise it falls back to the stock, path-less prompt.
func PlanApprovePrompt(planFilePath string) string {
	if planFilePath == "" {
		return planApprovePrompt
	}
	return fmt.Sprintf("I approve the plan at %s. Please proceed with the implementation.", planFilePath)
}
