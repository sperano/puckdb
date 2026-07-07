package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sperano/puckdb/graph/model"
)

// TestProgressReportStatusQueries tests all status methods that use executeProgressReportQuery.
// These methods are structurally identical (same parser, different field names), so we use
// a table of method references to avoid duplicating the httptest boilerplate.
func TestProgressReportStatusQueries(t *testing.T) {
	t.Parallel()

	type statusMethod struct {
		name          string
		call          func(ctx context.Context, client *GraphQLClient) (*WorkflowStatus, error)
		resultField   string
		progressField string
	}

	methods := []statusMethod{
		{"GetInitializeStatus", func(ctx context.Context, c *GraphQLClient) (*WorkflowStatus, error) {
			return c.GetInitializeStatus(ctx)
		}, "initializeResult", "initializeProgress"},
		{"GetFetchSeasonsStatus", func(ctx context.Context, c *GraphQLClient) (*WorkflowStatus, error) {
			return c.GetFetchSeasonsStatus(ctx)
		}, "fetchSeasonsResult", "fetchSeasonsProgress"},
		{"GetFetchPlayerLogsStatus", func(ctx context.Context, c *GraphQLClient) (*WorkflowStatus, error) {
			return c.GetFetchPlayerLogsStatus(ctx)
		}, "fetchPlayerLogsResult", "fetchPlayerLogsProgress"},
		{"GetFetchYahooPlayersStatus", func(ctx context.Context, c *GraphQLClient) (*WorkflowStatus, error) {
			return c.GetFetchYahooPlayersStatus(ctx)
		}, "fetchYahooPlayersResult", "fetchYahooPlayersProgress"},
		{"GetProcessPlayersStatus", func(ctx context.Context, c *GraphQLClient) (*WorkflowStatus, error) {
			return c.GetProcessPlayersStatus(ctx)
		}, "processPlayersResult", "processPlayersProgress"},
		{"GetImportSeasonsStatus", func(ctx context.Context, c *GraphQLClient) (*WorkflowStatus, error) {
			return c.GetImportSeasonsStatus(ctx)
		}, "importSeasonsResult", "importSeasonsProgress"},
		{"GetImportPlayerLogsStatus", func(ctx context.Context, c *GraphQLClient) (*WorkflowStatus, error) {
			return c.GetImportPlayerLogsStatus(ctx)
		}, "importPlayerLogsResult", "importPlayerLogsProgress"},
		{"GetExtractBoxscorePlayersStatus", func(ctx context.Context, c *GraphQLClient) (*WorkflowStatus, error) {
			return c.GetExtractBoxscorePlayersStatus(ctx)
		}, "extractBoxscorePlayersResult", "extractBoxscorePlayersProgress"},
		{"GetFetchPlayerLandingsStatus", func(ctx context.Context, c *GraphQLClient) (*WorkflowStatus, error) {
			return c.GetFetchPlayerLandingsStatus(ctx)
		}, "fetchPlayerLandingsResult", "fetchPlayerLandingsProgress"},
	}

	for _, m := range methods {
		t.Run(m.name, func(t *testing.T) {
			t.Parallel()
			resultField := m.resultField
			progressField := m.progressField

			t.Run("running with progress report", func(t *testing.T) {
				t.Parallel()
				server := newProgressReportServer(t, resultField, progressField,
					model.TemporalWorkflowStatusRunning, nil,
					&model.ProgressReport{
						Total:     100,
						Completed: 42,
						Groups: []*model.ProgressGroup{
							{Header: "Downloading", Bars: []*model.ProgressBar{{Current: 42, Total: 100, Started: true}}},
						},
					})
				defer server.Close()

				client := NewGraphQLClient(server.URL)
				status, err := m.call(context.Background(), client)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status.Result.Status != model.TemporalWorkflowStatusRunning {
					t.Errorf("status = %v, want RUNNING", status.Result.Status)
				}
				if status.Progress == nil {
					t.Fatal("expected ProgressReport to be set")
				}
				if status.Progress.Total != 100 {
					t.Errorf("total = %d, want 100", status.Progress.Total)
				}
				if status.Progress.Completed != 42 {
					t.Errorf("completed = %d, want 42", status.Progress.Completed)
				}
				if len(status.Progress.Groups) != 1 {
					t.Fatalf("groups = %d, want 1", len(status.Progress.Groups))
				}
				if status.Progress.Groups[0].Header != "Downloading" {
					t.Errorf("group header = %q, want %q", status.Progress.Groups[0].Header, "Downloading")
				}
			})

			t.Run("completed", func(t *testing.T) {
				t.Parallel()
				server := newProgressReportServer(t, resultField, progressField,
					model.TemporalWorkflowStatusCompleted, nil,
					&model.ProgressReport{Total: 100, Completed: 100})
				defer server.Close()

				client := NewGraphQLClient(server.URL)
				status, err := m.call(context.Background(), client)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status.Result.Status != model.TemporalWorkflowStatusCompleted {
					t.Errorf("status = %v, want COMPLETED", status.Result.Status)
				}
			})

			t.Run("failed with reason", func(t *testing.T) {
				t.Parallel()
				reason := "connection timeout"
				server := newProgressReportServer(t, resultField, progressField,
					model.TemporalWorkflowStatusFailed, &reason, nil)
				defer server.Close()

				client := NewGraphQLClient(server.URL)
				status, err := m.call(context.Background(), client)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status.Result.Status != model.TemporalWorkflowStatusFailed {
					t.Errorf("status = %v, want FAILED", status.Result.Status)
				}
				if status.Result.FailureReason == nil || *status.Result.FailureReason != reason {
					t.Errorf("failureReason = %v, want %q", status.Result.FailureReason, reason)
				}
			})

			t.Run("graphql error", func(t *testing.T) {
				t.Parallel()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(`{"data": null, "errors": [{"message": "internal error"}]}`))
				}))
				defer server.Close()

				client := NewGraphQLClient(server.URL)
				_, err := m.call(context.Background(), client)
				if err == nil {
					t.Error("expected error for graphql error response")
				}
			})

			t.Run("server error", func(t *testing.T) {
				t.Parallel()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte("Internal Server Error"))
				}))
				defer server.Close()

				client := NewGraphQLClient(server.URL)
				_, err := m.call(context.Background(), client)
				if err == nil {
					t.Error("expected error for 500 response")
				}
			})
		})
	}
}

// TestExecuteProgressReportQuery_QueryFormat verifies that the correct GraphQL query
// string is sent for a representative ProgressReport-based status method.
func TestExecuteProgressReportQuery_QueryFormat(t *testing.T) {
	t.Parallel()

	expectedQuery := `query { initializeResult ` + resultFields + ` initializeProgress ` + progressReportFields + ` ` + yahooTokenStatusField + ` }`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req graphQLRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
		}
		if req.Query != expectedQuery {
			t.Errorf("query = %q, want %q", req.Query, expectedQuery)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %s, want application/json", r.Header.Get("Content-Type"))
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": {"initializeResult": {"status": "RUNNING"}, "initializeProgress": {"total": 1, "completed": 0, "groups": []}}}`))
	}))
	defer server.Close()

	client := NewGraphQLClient(server.URL)
	_, err := client.GetInitializeStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestGetProcessPlayersResultData tests the custom result data query.
func TestGetProcessPlayersResultData(t *testing.T) {
	t.Parallel()

	t.Run("successful response", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"data": {"processPlayersResultData": {
				"totalPlayers": 500,
				"importedPlayers": 480,
				"matchedWithYahoo": 450,
				"downloaded": 100,
				"cacheHits": 400,
				"missing": 20,
				"totalYahooPlayers": 600,
				"skippedNonNHL": 50,
				"verifiedNonNHLThisRun": 10,
				"trulyUnmatched": [],
				"errors": []
			}}}`))
		}))
		defer server.Close()

		client := NewGraphQLClient(server.URL)
		result, err := client.GetProcessPlayersResultData(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.TotalPlayers != 500 {
			t.Errorf("totalPlayers = %d, want 500", result.TotalPlayers)
		}
		if result.ImportedPlayers != 480 {
			t.Errorf("importedPlayers = %d, want 480", result.ImportedPlayers)
		}
		if result.MatchedWithYahoo != 450 {
			t.Errorf("matchedWithYahoo = %d, want 450", result.MatchedWithYahoo)
		}
		if result.Downloaded != 100 {
			t.Errorf("downloaded = %d, want 100", result.Downloaded)
		}
		if result.CacheHits != 400 {
			t.Errorf("cacheHits = %d, want 400", result.CacheHits)
		}
		if result.Missing != 20 {
			t.Errorf("missing = %d, want 20", result.Missing)
		}
	})

	t.Run("with unmatched players", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"data": {"processPlayersResultData": {
				"totalPlayers": 10, "importedPlayers": 8, "matchedWithYahoo": 7,
				"downloaded": 5, "cacheHits": 3, "missing": 2,
				"totalYahooPlayers": 12, "skippedNonNHL": 1, "verifiedNonNHLThisRun": 0,
				"trulyUnmatched": [{"yahooID": 999, "firstName": "John", "lastName": "Doe", "nhlGames": 5, "nhlPlayerID": 123, "nhlName": "J. Doe"}],
				"errors": ["error1"]
			}}}`))
		}))
		defer server.Close()

		client := NewGraphQLClient(server.URL)
		result, err := client.GetProcessPlayersResultData(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.TrulyUnmatched) != 1 {
			t.Fatalf("trulyUnmatched = %d, want 1", len(result.TrulyUnmatched))
		}
		if result.TrulyUnmatched[0].YahooID != 999 {
			t.Errorf("yahooID = %d, want 999", result.TrulyUnmatched[0].YahooID)
		}
		if len(result.Errors) != 1 {
			t.Fatalf("errors = %d, want 1", len(result.Errors))
		}
	})

	t.Run("graphql error", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"data": null, "errors": [{"message": "not found"}]}`))
		}))
		defer server.Close()

		client := NewGraphQLClient(server.URL)
		_, err := client.GetProcessPlayersResultData(context.Background())
		if err == nil {
			t.Error("expected error for graphql error response")
		}
	})
}

// newProgressReportServer creates an httptest server that returns a GraphQL response
// with the given result status and optional ProgressReport for the specified field names.
func newProgressReportServer(t *testing.T, resultField, progressField string, status model.TemporalWorkflowStatus, failureReason *string, report *model.ProgressReport) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		result := map[string]any{
			"status":        status,
			"failureReason": failureReason,
		}

		data := map[string]any{
			resultField: result,
		}
		if report != nil {
			data[progressField] = report
		}

		resp := map[string]any{"data": data}
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
}
