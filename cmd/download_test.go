package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sperano/puckdb/graph/model"
)

func TestGraphQLClient_DownloadEverything(t *testing.T) {
	tests := []struct {
		name           string
		responseBody   string
		responseStatus int
		wantResult     bool
		wantErr        bool
	}{
		{
			name:           "successful start",
			responseBody:   `{"data": {"downloadEverything": true}}`,
			responseStatus: http.StatusOK,
			wantResult:     true,
			wantErr:        false,
		},
		{
			name:           "already running",
			responseBody:   `{"data": {"downloadEverything": false}}`,
			responseStatus: http.StatusOK,
			wantResult:     false,
			wantErr:        false,
		},
		{
			name:           "graphql error",
			responseBody:   `{"data": null, "errors": [{"message": "authentication required"}]}`,
			responseStatus: http.StatusOK,
			wantResult:     false,
			wantErr:        true,
		},
		{
			name:           "server error",
			responseBody:   `Internal Server Error`,
			responseStatus: http.StatusInternalServerError,
			wantResult:     false,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("expected POST, got %s", r.Method)
				}
				if r.URL.Path != "/graphql/query" {
					t.Errorf("expected /graphql/query, got %s", r.URL.Path)
				}
				if r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
				}

				var req graphQLRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("failed to decode request: %v", err)
				}
				if req.Query != `mutation { downloadEverything }` {
					t.Errorf("unexpected query: %s", req.Query)
				}

				w.WriteHeader(tt.responseStatus)
				w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			client := NewGraphQLClient(server.URL)
			result, err := client.DownloadEverything(context.Background())

			if (err != nil) != tt.wantErr {
				t.Errorf("DownloadEverything() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if result != tt.wantResult {
				t.Errorf("DownloadEverything() = %v, want %v", result, tt.wantResult)
			}
		})
	}
}

func TestGraphQLClient_GetDownloadEverythingStatus(t *testing.T) {
	tests := []struct {
		name              string
		responseBody      string
		responseStatus    int
		wantStatus        model.TemporalWorkflowStatus
		wantFailureReason string
		wantTotal         int
		wantCompleted     int
		wantErr           bool
	}{
		{
			name:           "running with progress",
			responseBody:   `{"data": {"downloadEverythingResult": {"status": "RUNNING", "failureReason": null}, "downloadEverythingProgress": {"total": 10, "completed": 3}}}`,
			responseStatus: http.StatusOK,
			wantStatus:     model.TemporalWorkflowStatusRunning,
			wantTotal:      10,
			wantCompleted:  3,
			wantErr:        false,
		},
		{
			name:           "completed",
			responseBody:   `{"data": {"downloadEverythingResult": {"status": "COMPLETED", "failureReason": null}, "downloadEverythingProgress": {"total": 10, "completed": 10}}}`,
			responseStatus: http.StatusOK,
			wantStatus:     model.TemporalWorkflowStatusCompleted,
			wantTotal:      10,
			wantCompleted:  10,
			wantErr:        false,
		},
		{
			name:              "failed with reason",
			responseBody:      `{"data": {"downloadEverythingResult": {"status": "FAILED", "failureReason": "connection timeout"}, "downloadEverythingProgress": {"total": 10, "completed": 5}}}`,
			responseStatus:    http.StatusOK,
			wantStatus:        model.TemporalWorkflowStatusFailed,
			wantFailureReason: "connection timeout",
			wantTotal:         10,
			wantCompleted:     5,
			wantErr:           false,
		},
		{
			name:           "graphql error",
			responseBody:   `{"data": null, "errors": [{"message": "internal error"}]}`,
			responseStatus: http.StatusOK,
			wantStatus:     "",
			wantErr:        true,
		},
	}

	expectedQuery := `query { downloadEverythingResult { status failureReason } downloadEverythingProgress { total completed message } }`

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req graphQLRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("failed to decode request: %v", err)
				}
				if req.Query != expectedQuery {
					t.Errorf("unexpected query: %s", req.Query)
				}

				w.WriteHeader(tt.responseStatus)
				w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			client := NewGraphQLClient(server.URL)
			status, err := client.GetDownloadEverythingStatus(context.Background())

			if (err != nil) != tt.wantErr {
				t.Errorf("GetDownloadEverythingStatus() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if status.Result.Status != tt.wantStatus {
				t.Errorf("GetDownloadEverythingStatus() status = %v, want %v", status.Result.Status, tt.wantStatus)
			}
			gotReason := ""
			if status.Result.FailureReason != nil {
				gotReason = *status.Result.FailureReason
			}
			if gotReason != tt.wantFailureReason {
				t.Errorf("GetDownloadEverythingStatus() failureReason = %v, want %v", gotReason, tt.wantFailureReason)
			}
			if status.Progress != nil {
				if status.Progress.Total != tt.wantTotal {
					t.Errorf("GetDownloadEverythingStatus() total = %v, want %v", status.Progress.Total, tt.wantTotal)
				}
				if status.Progress.Completed != tt.wantCompleted {
					t.Errorf("GetDownloadEverythingStatus() completed = %v, want %v", status.Progress.Completed, tt.wantCompleted)
				}
			}
		})
	}
}

func TestGraphQLClient_Execute(t *testing.T) {
	tests := []struct {
		name           string
		responseBody   string
		responseStatus int
		wantErr        bool
	}{
		{
			name:           "successful response",
			responseBody:   `{"data": {"test": "value"}}`,
			responseStatus: http.StatusOK,
			wantErr:        false,
		},
		{
			name:           "bad json response",
			responseBody:   `not json`,
			responseStatus: http.StatusOK,
			wantErr:        true,
		},
		{
			name:           "non-200 status",
			responseBody:   `{"error": "bad request"}`,
			responseStatus: http.StatusBadRequest,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.responseStatus)
				w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			client := NewGraphQLClient(server.URL)
			_, err := client.execute(context.Background(), "query { test }", nil)

			if (err != nil) != tt.wantErr {
				t.Errorf("execute() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGraphQLClient_DownloadEverythingForSeason(t *testing.T) {
	tests := []struct {
		name           string
		season         int
		responseBody   string
		responseStatus int
		wantResult     bool
		wantErr        bool
	}{
		{
			name:           "successful start",
			season:         2024,
			responseBody:   `{"data": {"downloadEverythingForSeason": true}}`,
			responseStatus: http.StatusOK,
			wantResult:     true,
			wantErr:        false,
		},
		{
			name:           "already running",
			season:         2024,
			responseBody:   `{"data": {"downloadEverythingForSeason": false}}`,
			responseStatus: http.StatusOK,
			wantResult:     false,
			wantErr:        false,
		},
		{
			name:           "graphql error",
			season:         2024,
			responseBody:   `{"data": null, "errors": [{"message": "invalid season"}]}`,
			responseStatus: http.StatusOK,
			wantResult:     false,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req graphQLRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("failed to decode request: %v", err)
				}
				if req.Query != `mutation($season: Int!) { downloadEverythingForSeason(season: $season) }` {
					t.Errorf("unexpected query: %s", req.Query)
				}
				if req.Variables["season"] != float64(tt.season) {
					t.Errorf("unexpected season variable: %v", req.Variables["season"])
				}

				w.WriteHeader(tt.responseStatus)
				w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			client := NewGraphQLClient(server.URL)
			result, err := client.DownloadEverythingForSeason(context.Background(), tt.season)

			if (err != nil) != tt.wantErr {
				t.Errorf("DownloadEverythingForSeason() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if result != tt.wantResult {
				t.Errorf("DownloadEverythingForSeason() = %v, want %v", result, tt.wantResult)
			}
		})
	}
}

func TestGraphQLClient_GetDownloadEverythingForSeasonStatus(t *testing.T) {
	tests := []struct {
		name              string
		season            int
		responseBody      string
		responseStatus    int
		wantStatus        model.TemporalWorkflowStatus
		wantFailureReason string
		wantTotal         int
		wantCompleted     int
		wantErr           bool
	}{
		{
			name:           "running with progress",
			season:         2024,
			responseBody:   `{"data": {"downloadEverythingForSeasonResult": {"status": "RUNNING", "failureReason": null}, "downloadEverythingForSeasonProgress": {"total": 8, "completed": 2}}}`,
			responseStatus: http.StatusOK,
			wantStatus:     model.TemporalWorkflowStatusRunning,
			wantTotal:      8,
			wantCompleted:  2,
			wantErr:        false,
		},
		{
			name:           "completed",
			season:         2024,
			responseBody:   `{"data": {"downloadEverythingForSeasonResult": {"status": "COMPLETED", "failureReason": null}, "downloadEverythingForSeasonProgress": {"total": 8, "completed": 8}}}`,
			responseStatus: http.StatusOK,
			wantStatus:     model.TemporalWorkflowStatusCompleted,
			wantTotal:      8,
			wantCompleted:  8,
			wantErr:        false,
		},
		{
			name:              "failed with reason",
			season:            2024,
			responseBody:      `{"data": {"downloadEverythingForSeasonResult": {"status": "FAILED", "failureReason": "season not found"}, "downloadEverythingForSeasonProgress": {"total": 8, "completed": 3}}}`,
			responseStatus:    http.StatusOK,
			wantStatus:        model.TemporalWorkflowStatusFailed,
			wantFailureReason: "season not found",
			wantTotal:         8,
			wantCompleted:     3,
			wantErr:           false,
		},
	}

	expectedQuery := `query($season: Int!) { downloadEverythingForSeasonResult(season: $season) { status failureReason } downloadEverythingForSeasonProgress(season: $season) { total completed } }`

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req graphQLRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("failed to decode request: %v", err)
				}
				if req.Query != expectedQuery {
					t.Errorf("unexpected query: %s", req.Query)
				}
				if req.Variables["season"] != float64(tt.season) {
					t.Errorf("unexpected season variable: %v", req.Variables["season"])
				}

				w.WriteHeader(tt.responseStatus)
				w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			client := NewGraphQLClient(server.URL)
			status, err := client.GetDownloadEverythingForSeasonStatus(context.Background(), tt.season)

			if (err != nil) != tt.wantErr {
				t.Errorf("GetDownloadEverythingForSeasonStatus() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if status.Result.Status != tt.wantStatus {
				t.Errorf("GetDownloadEverythingForSeasonStatus() status = %v, want %v", status.Result.Status, tt.wantStatus)
			}
			gotReason := ""
			if status.Result.FailureReason != nil {
				gotReason = *status.Result.FailureReason
			}
			if gotReason != tt.wantFailureReason {
				t.Errorf("GetDownloadEverythingForSeasonStatus() failureReason = %v, want %v", gotReason, tt.wantFailureReason)
			}
			if status.Progress != nil {
				if status.Progress.Total != tt.wantTotal {
					t.Errorf("GetDownloadEverythingForSeasonStatus() total = %v, want %v", status.Progress.Total, tt.wantTotal)
				}
				if status.Progress.Completed != tt.wantCompleted {
					t.Errorf("GetDownloadEverythingForSeasonStatus() completed = %v, want %v", status.Progress.Completed, tt.wantCompleted)
				}
			}
		})
	}
}

func TestNewGraphQLClient(t *testing.T) {
	client := NewGraphQLClient("http://localhost:8080")

	if client.endpoint != "http://localhost:8080/graphql/query" {
		t.Errorf("expected endpoint http://localhost:8080/graphql/query, got %s", client.endpoint)
	}
	if client.httpClient == nil {
		t.Error("expected httpClient to be set")
	}
}

func TestNewGraphQLClient_WithTrailingSlash(t *testing.T) {
	client := NewGraphQLClient("http://localhost:8080/")

	if client.endpoint != "http://localhost:8080/graphql/query" {
		t.Errorf("expected endpoint http://localhost:8080/graphql/query, got %s", client.endpoint)
	}
}

func TestFormatStatusMessage(t *testing.T) {
	tests := []struct {
		name   string
		status *WorkflowStatus
		want   string
	}{
		{
			name: "nil progress shows status only",
			status: &WorkflowStatus{
				Result: &model.WorkflowResult{
					Status: model.TemporalWorkflowStatusRunning,
				},
				Progress: nil,
			},
			want: "Workflow status: RUNNING",
		},
		{
			name: "progress with total zero and message",
			status: &WorkflowStatus{
				Result: &model.WorkflowResult{
					Status: model.TemporalWorkflowStatusRunning,
				},
				Progress: &model.WorkflowProgress{
					Total:     0,
					Completed: 0,
					Message:   ptr("Initializing..."),
				},
			},
			want: "Initializing...",
		},
		{
			name: "progress with total shows progress bar",
			status: &WorkflowStatus{
				Result: &model.WorkflowResult{
					Status: model.TemporalWorkflowStatusRunning,
				},
				Progress: &model.WorkflowProgress{
					Total:     10,
					Completed: 5,
					Header:    ptr("Processing"),
				},
			},
			// 17-char label area (x/y right-aligned) + 80-char bar + percent
			want: "▶ Processing\n\x00              5/10 [████████████████████████████████████████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░] 50%",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatStatusMessage(tt.status)
			if got != tt.want {
				t.Errorf("formatStatusMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ptr returns a pointer to the given string
func ptr(s string) *string {
	return &s
}

func displayStylePtr(s model.ProgressDisplayStyle) *model.ProgressDisplayStyle {
	return &s
}

func TestFormatStatusMessage_GroupedItemsAlignment(t *testing.T) {
	t.Parallel()

	// Test the right-alignment of progress numbers
	status := &WorkflowStatus{
		Progress: &model.WorkflowProgress{
			Total:        17406,
			Completed:    13441,
			Header:       ptr("Downloading seasons"),
			DisplayStyle: displayStylePtr(model.ProgressDisplayStyleParallel),
			Items: []*model.ProgressItem{
				{
					ID:          2003,
					Description: ptr("2003-04"),
					Total:       182,
					Completed:   0,
					Started:     true,
				},
				{
					ID:          2005,
					Description: ptr("2005-06"),
					Total:       198,
					Completed:   123,
					Started:     true,
				},
			},
		},
		Result: &model.WorkflowResult{Status: model.TemporalWorkflowStatusRunning},
	}

	got := formatStatusMessage(status)
	lines := strings.Split(got, "\n")

	// Should have: header, 2 items, total = 4 lines
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d: %q", len(lines), got)
	}

	// Check header
	if !strings.Contains(lines[0], "▶ Downloading seasons") {
		t.Errorf("line 0 should contain header, got %q", lines[0])
	}

	// Check alignment: item lines should have "/" at the same position
	line1SlashPos := strings.Index(lines[1], "/")
	line2SlashPos := strings.Index(lines[2], "/")

	if line1SlashPos != line2SlashPos {
		t.Errorf("slash positions differ: line1=%d, line2=%d", line1SlashPos, line2SlashPos)
	}

	// Verify right-alignment: "0/182" should have leading spaces
	if !strings.Contains(lines[1], "    0/182") {
		t.Errorf("line 1 should have right-aligned 0/182, got %q", lines[1])
	}

	// Verify totals present
	if !strings.Contains(lines[3], "Total") {
		t.Errorf("line 3 should contain Total, got %q", lines[3])
	}
	if !strings.Contains(lines[3], "13441/17406") {
		t.Errorf("line 3 should contain 13441/17406, got %q", lines[3])
	}
}

func TestFormatStatusMessage_GroupedItemsCompletedSkipped(t *testing.T) {
	t.Parallel()

	status := &WorkflowStatus{
		Progress: &model.WorkflowProgress{
			Total:        300,
			Completed:    200,
			DisplayStyle: displayStylePtr(model.ProgressDisplayStyleParallel),
			Items: []*model.ProgressItem{
				{
					ID:          1,
					Description: ptr("Completed"),
					Total:       100,
					Completed:   100, // completed - should be skipped
					Started:     true,
				},
				{
					ID:          2,
					Description: ptr("In progress"),
					Total:       100,
					Completed:   50,
					Started:     true,
				},
				{
					ID:          3,
					Description: ptr("Not started"),
					Total:       100,
					Completed:   0,
					Started:     false, // not started - should be skipped
				},
			},
		},
		Result: &model.WorkflowResult{Status: model.TemporalWorkflowStatusRunning},
	}

	got := formatStatusMessage(status)

	// Should only show "In progress" item + Total
	if strings.Contains(got, "Completed") {
		t.Errorf("should not contain completed item, got %q", got)
	}
	if strings.Contains(got, "Not started") {
		t.Errorf("should not contain not-started item, got %q", got)
	}
	if !strings.Contains(got, "In progress") {
		t.Errorf("should contain in-progress item, got %q", got)
	}
	if !strings.Contains(got, "Total") {
		t.Errorf("should contain Total line, got %q", got)
	}
}

func TestFormatStatusMessage_DefaultDisplayItems(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   *WorkflowStatus
		contains []string
		excludes []string
	}{
		{
			name: "completed item shows checkmark",
			status: &WorkflowStatus{
				Progress: &model.WorkflowProgress{
					Items: []*model.ProgressItem{
						{
							ID:                   1,
							Description:          ptr("Phase 1"),
							CompletedDescription: ptr("Phase 1 done in 5s"),
							Total:                100,
							Completed:            100,
							Started:              true,
						},
					},
				},
				Result: &model.WorkflowResult{Status: model.TemporalWorkflowStatusRunning},
			},
			contains: []string{"✓ Phase 1 done in 5s"},
		},
		{
			name: "in progress item shows arrow and bar",
			status: &WorkflowStatus{
				Progress: &model.WorkflowProgress{
					Items: []*model.ProgressItem{
						{
							ID:          1,
							Description: ptr("Downloading"),
							Total:       200,
							Completed:   100,
							Started:     true,
						},
					},
				},
				Result: &model.WorkflowResult{Status: model.TemporalWorkflowStatusRunning},
			},
			contains: []string{"▶ Downloading", "100/200", "50%"},
		},
		{
			name: "pending item is skipped",
			status: &WorkflowStatus{
				Progress: &model.WorkflowProgress{
					Items: []*model.ProgressItem{
						{
							ID:          1,
							Description: ptr("Pending task"),
							Total:       100,
							Completed:   0,
							Started:     false,
						},
					},
				},
				Result: &model.WorkflowResult{Status: model.TemporalWorkflowStatusRunning},
			},
			excludes: []string{"Pending task"},
		},
		{
			name: "message prepended to output",
			status: &WorkflowStatus{
				Progress: &model.WorkflowProgress{
					Message: ptr("Starting workflow"),
					Items: []*model.ProgressItem{
						{
							ID:          1,
							Description: ptr("Task 1"),
							Total:       50,
							Completed:   25,
							Started:     true,
						},
					},
				},
				Result: &model.WorkflowResult{Status: model.TemporalWorkflowStatusRunning},
			},
			contains: []string{"Starting workflow", "▶ Task 1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatStatusMessage(tt.status)
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
