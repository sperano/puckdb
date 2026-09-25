package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ────────────────────────────────────────────────────────────────────────────
// parseLeagueIDs
// ────────────────────────────────────────────────────────────────────────────

func TestParseLeagueIDs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		want    []int
		wantErr bool
	}{
		{"two_ids", "1001,1002", []int{1001, 1002}, false},
		{"surrounding_spaces", " 1001 , 1002 ", []int{1001, 1002}, false},
		{"trailing_comma", "1001,1002,", []int{1001, 1002}, false},
		{"empty_is_error", "", nil, true},
		{"blank_is_error", "   ", nil, true},
		{"non_numeric_is_error", "x", nil, true},
		{"zero_is_error", "0", nil, true},
		{"negative_is_error", "-5", nil, true},
		{"one_valid_one_invalid", "1001,x", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseLeagueIDs(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// writeDraftReport
// ────────────────────────────────────────────────────────────────────────────

const draftReportTestBody = "# report body\n"

func TestWriteDraftReport_ToStdout(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer

	err := writeDraftReport(&stdout, "", func(w io.Writer) error {
		_, writeErr := io.WriteString(w, draftReportTestBody)
		return writeErr
	})

	require.NoError(t, err)
	assert.Equal(t, draftReportTestBody, stdout.String())
}

func TestWriteDraftReport_ToFile(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	path := filepath.Join(t.TempDir(), "report.md")

	err := writeDraftReport(&stdout, path, func(w io.Writer) error {
		_, writeErr := io.WriteString(w, draftReportTestBody)
		return writeErr
	})

	require.NoError(t, err)
	data, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, draftReportTestBody, string(data))
	assert.Equal(t, "Wrote "+path+"\n", stdout.String())
}

func TestWriteDraftReport_RenderErrorLeavesNoFile(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	path := filepath.Join(t.TempDir(), "report.md")
	renderErr := errors.New("render failed")

	err := writeDraftReport(&stdout, path, func(w io.Writer) error {
		return renderErr
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, renderErr)
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "a failed render must not create the output file")
	assert.Empty(t, stdout.String())
}

// ────────────────────────────────────────────────────────────────────────────
// draftRequestFromFlags
// ────────────────────────────────────────────────────────────────────────────

func setDraftFlags(t *testing.T, season int, leagues, output string, staleAfter int) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set(config.FlagDraftSeason, season)
	viper.Set(config.FlagDraftLeagues, leagues)
	viper.Set(config.FlagDraftOutput, output)
	viper.Set(config.FlagDraftStaleAfter, staleAfter)
}

func TestDraftRequestFromFlags_MissingSeason(t *testing.T) {
	setDraftFlags(t, 0, "1001", "", config.DefaultDraftStaleAfter)

	_, err := draftRequestFromFlags()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--"+config.FlagDraftSeason+" is required")
}

func TestDraftRequestFromFlags_NonPositiveStaleAfter(t *testing.T) {
	setDraftFlags(t, 2026, "1001", "", 0)

	_, err := draftRequestFromFlags()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--"+config.FlagDraftStaleAfter+" must be positive")
}

func TestDraftRequestFromFlags_NegativeStaleAfter(t *testing.T) {
	setDraftFlags(t, 2026, "1001", "", -1)

	_, err := draftRequestFromFlags()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--"+config.FlagDraftStaleAfter+" must be positive")
}

func TestDraftRequestFromFlags_InvalidLeagues(t *testing.T) {
	setDraftFlags(t, 2026, "", "", config.DefaultDraftStaleAfter)

	_, err := draftRequestFromFlags()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--"+config.FlagDraftLeagues)
}

func TestDraftRequestFromFlags_Valid(t *testing.T) {
	setDraftFlags(t, 2026, "1001,1002", "/tmp/out.md", 48)

	request, err := draftRequestFromFlags()

	require.NoError(t, err)
	assert.Equal(t, 2026, request.Season)
	assert.Equal(t, []int{1001, 1002}, request.LeagueIDs)
	assert.Equal(t, "/tmp/out.md", request.Output)
	assert.Equal(t, 48*time.Hour, request.StaleAfter)
}
