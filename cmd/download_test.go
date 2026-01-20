package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

	expectedQuery := `query {
		downloadEverythingResult { status failureReason }
		downloadEverythingProgress { total completed }
	}`

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

	expectedQuery := `query($season: Int!) {
		downloadEverythingForSeasonResult(season: $season) { status failureReason }
		downloadEverythingForSeasonProgress(season: $season) { total completed }
	}`

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
