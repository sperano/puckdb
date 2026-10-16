package shared

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// progressOptionsOwner is the one file allowed to build LocalActivityOptions
// for progress persistence; every other site goes through
// WithProgressLocalActivityOptions.
const progressOptionsOwner = "progress.go"

func TestWithProgressLocalActivityOptions_SetsSharedTimeout(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var got workflow.LocalActivityOptions
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		got = workflow.GetLocalActivityOptions(WithProgressLocalActivityOptions(ctx))
		return nil
	})
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, config.DefaultProgressLocalActivityTimeout, got.ScheduleToCloseTimeout)
	require.Zero(t, got.StartToCloseTimeout, "only ScheduleToClose bounds a progress save")
}

// TestLocalActivityOptionsAreCentralized fails when a worker package builds
// its own LocalActivityOptions instead of calling
// WithProgressLocalActivityOptions, so the timeout cannot drift between
// sites again.
func TestLocalActivityOptionsAreCentralized(t *testing.T) {
	workerDir := filepath.Join("..")
	var offenders []string
	err := filepath.WalkDir(workerDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if filepath.Base(path) == progressOptionsOwner && filepath.Base(filepath.Dir(path)) == "shared" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(content), "LocalActivityOptions{") {
			offenders = append(offenders, path)
		}
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, offenders, "build local activity options through shared.WithProgressLocalActivityOptions")
}
