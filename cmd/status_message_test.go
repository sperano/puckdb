package cmd

import (
	"strings"
	"testing"

	"github.com/sperano/puckdb/internal/graph/model"
)

func TestFormatStatusMessage_NilProgress(t *testing.T) {
	t.Parallel()

	got := formatStatusMessage(
		&WorkflowStatus{
			Result:   &model.WorkflowResult{Status: model.TemporalWorkflowStatusRunning},
			Progress: nil,
		},
		workflowFetchSeasons,
	)
	want := SpinnerPlaceholder + " Workflow fetchSeasons is starting..."
	if got != want {
		t.Errorf("formatStatusMessage() = %q, want %q", got, want)
	}
}

func TestFormatStatusMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   *WorkflowStatus
		contains []string
		excludes []string
	}{
		{
			name: "single bar group with progress",
			status: &WorkflowStatus{
				Result: &model.WorkflowResult{Status: model.TemporalWorkflowStatusRunning},
				Progress: &model.ProgressReport{
					Groups: []*model.ProgressGroup{
						{
							Header: "Downloading",
							Bars:   []*model.ProgressBar{{Current: 5, Total: 10, Started: true}},
						},
					},
				},
			},
			contains: []string{"▶ Downloading", "5/10", "50%"},
		},
		{
			name: "completed group shows checkmark",
			status: &WorkflowStatus{
				Result: &model.WorkflowResult{Status: model.TemporalWorkflowStatusRunning},
				Progress: &model.ProgressReport{
					Groups: []*model.ProgressGroup{
						{
							Header:       "Fetching seasons",
							CompletedMsg: "Fetched 10 seasons in 5s",
							CompletedAt:  1,
							Bars:         []*model.ProgressBar{{Current: 10, Total: 10, Started: true}},
						},
					},
				},
			},
			contains: []string{"✓ Fetched 10 seasons in 5s"},
			excludes: []string{"▶"},
		},
		{
			name: "single bar with total 1 shows spinner only",
			status: &WorkflowStatus{
				Result: &model.WorkflowResult{Status: model.TemporalWorkflowStatusRunning},
				Progress: &model.ProgressReport{
					Groups: []*model.ProgressGroup{
						{
							Header: "Initializing",
							Bars:   []*model.ProgressBar{{Current: 0, Total: 1, Started: true}},
						},
					},
				},
			},
			contains: []string{"Initializing"},
			excludes: []string{"▶", "0/1"},
		},
		{
			name: "multi-bar group with labels",
			status: &WorkflowStatus{
				Result: &model.WorkflowResult{Status: model.TemporalWorkflowStatusRunning},
				Progress: &model.ProgressReport{
					Groups: []*model.ProgressGroup{
						{
							Header: "Downloading seasons",
							Bars: []*model.ProgressBar{
								{Label: ptr("2003-04"), Current: 0, Total: 182, Started: true},
								{Label: ptr("2005-06"), Current: 123, Total: 198, Started: true},
							},
						},
					},
				},
			},
			contains: []string{"▶ Downloading seasons", "2003-04", "0/182", "2005-06", "123/198", "Total"},
		},
		{
			name: "multi-bar skips completed and unstarted bars",
			status: &WorkflowStatus{
				Result: &model.WorkflowResult{Status: model.TemporalWorkflowStatusRunning},
				Progress: &model.ProgressReport{
					Groups: []*model.ProgressGroup{
						{
							Header: "Processing",
							Bars: []*model.ProgressBar{
								{Label: ptr("Done"), Current: 100, Total: 100, Started: true},
								{Label: ptr("Active"), Current: 50, Total: 100, Started: true},
								{Label: ptr("Pending"), Current: 0, Total: 100, Started: false},
							},
						},
					},
				},
			},
			contains: []string{"Active", "50/100", "Total"},
			excludes: []string{"Done", "Pending"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatStatusMessage(tt.status, workflowFetchSeasons)
			for _, substr := range tt.contains {
				if !strings.Contains(got, substr) {
					t.Errorf("formatStatusMessage() = %q, missing %q", got, substr)
				}
			}
			for _, substr := range tt.excludes {
				if strings.Contains(got, substr) {
					t.Errorf("formatStatusMessage() = %q, should not contain %q", got, substr)
				}
			}
		})
	}
}
