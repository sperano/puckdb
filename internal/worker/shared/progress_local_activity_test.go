package shared

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// workerDir is internal/worker, relative to this package directory.
const workerDir = ".."

// localActivityOptionOwners lists the files under internal/worker allowed to
// build workflow.LocalActivityOptions. Every other site applies a helper from
// shared; a local activity with a justified different timeout adds its file
// here with the reason.
var localActivityOptionOwners = map[string]string{
	filepath.Join(workerDir, "shared", "progress.go"): "WithProgressLocalActivityOptions, the progress persistence options",
}

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
// workflow.LocalActivityOptions outside localActivityOptionOwners, so a
// timeout cannot be written at a call site again.
func TestLocalActivityOptionsAreCentralized(t *testing.T) {
	var offenders []string
	err := filepath.WalkDir(workerDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if _, owner := localActivityOptionOwners[filepath.Clean(path)]; owner {
			return nil
		}
		if buildsLocalActivityOptions(t, path) {
			offenders = append(offenders, path)
		}
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, offenders,
		"apply shared.WithProgressLocalActivityOptions (or add the file to localActivityOptionOwners with a reason)")
}

// buildsLocalActivityOptions reports whether the file contains a composite
// literal of workflow.LocalActivityOptions.
func buildsLocalActivityOptions(t *testing.T, path string) bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoError(t, err)
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		sel, ok := lit.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "LocalActivityOptions" {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "workflow" {
			found = true
		}
		return !found
	})
	return found
}
