package cmd

import "fmt"

func formatStatusMessage(status *WorkflowStatus, wt workflowType) string {
	if status.Progress != nil {
		return formatProgressReport(status.Progress)
	}
	// No progress yet — workflow has either just started, or its query
	// handler hasn't been picked up by a worker (saturated worker pool).
	// Render with SpinnerPlaceholder so each line gets its own animated
	// frame in multi-workflow displays, and name the workflow so the
	// user knows which one is in this state.
	return fmt.Sprintf("%s Workflow %s is starting...", SpinnerPlaceholder, wt)
}

// yahooWarning returns the Yahoo token warning line if the token is missing,
// or empty string if the token is present.
func yahooWarning(status *WorkflowStatus) string {
	if status.YahooTokenMissing {
		return formatYahooTokenWarning(status.YahooLoginURL)
	}
	return ""
}

// formatYahooTokenWarning returns a red ANSI-colored warning line for missing Yahoo token.
func formatYahooTokenWarning(loginURL string) string {
	return fmt.Sprintf("\033[38;5;196m⚠ No Yahoo token — visit %s\033[0m", loginURL)
}
