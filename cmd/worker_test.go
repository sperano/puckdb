package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"
)

// workerSourceRoot holds every package with workflow code, relative to cmd/.
const workerSourceRoot = "../internal/worker"

// workflowSDKImport marks a file as workflow code for the scheduled-name scan.
const workflowSDKImport = `"go.temporal.io/sdk/workflow"`

// historyOnlyActivities are registered although no current workflow code
// schedules them: histories of a still-registered workflow type recorded
// their names. Each entry says which workflow did.
var historyOnlyActivities = map[string]string{
	"ImportEdgeSkaters":             "ImportEdgeWorkflow before the per-team Edge imports",
	"ImportEdgeGoalies":             "ImportEdgeWorkflow before the per-team Edge imports",
	"ImportEdgeTeams":               "ImportEdgeWorkflow before the per-team Edge imports",
	"ImportEdgeTeamZoneTimeDetails": "ImportEdgeWorkflow before the per-team Edge imports",
}

// recordingWorker records the activity names registration helpers register.
// Every other worker.Worker method panics (the embedded nil interface).
type recordingWorker struct {
	worker.Worker
	activities []string
}

func (w *recordingWorker) RegisterActivity(a any) {
	w.activities = append(w.activities, temporalActivityName(a))
}

func (w *recordingWorker) RegisterActivityWithOptions(a any, options activity.RegisterOptions) {
	name := options.Name
	if name == "" {
		name = temporalActivityName(a)
	}
	w.activities = append(w.activities, name)
}

// temporalActivityName derives an activity's default name the way the
// Temporal SDK does: the function's last dotted element without the "-fm"
// suffix of a method value.
func temporalActivityName(a any) string {
	full := runtime.FuncForPC(reflect.ValueOf(a).Pointer()).Name()
	return strings.TrimSuffix(full[strings.LastIndex(full, ".")+1:], "-fm")
}

// registeredWorkerActivities returns every activity name the tasks and asset
// workers register (the admin queue registers its own inline).
func registeredWorkerActivities() []string {
	w := &recordingWorker{}
	d := taskActivityDeps{}
	registerSimulationActivities(w, nil, nil)
	registerProgressActivities(w, nil)
	registerYahooFetchActivities(w, d)
	registerNHLActivities(w, d, registerYahooImportActivities(w, d))
	registerPlayerActivities(w, d)
	registerNewsActivities(w, nil, nil)
	registerDraftRankingActivities(w, nil)
	registerAssetActivities(w, nil)
	return w.activities
}

// scheduledActivityNames collects, from every workflow-code file under
// workerSourceRoot, the names a workflow can schedule: method values passed
// as functions (a selector not in call position) and string literals (named
// activities). A superset of what is scheduled, never a subset.
func scheduledActivityNames(t *testing.T) map[string]bool {
	t.Helper()
	names := make(map[string]bool)
	fset := token.NewFileSet()
	err := filepath.WalkDir(workerSourceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		if !importsPath(file, workflowSDKImport) {
			return nil
		}
		collectReferencedNames(file, names)
		return nil
	})
	require.NoError(t, err)
	return names
}

func importsPath(file *ast.File, path string) bool {
	for _, spec := range file.Imports {
		if spec.Path.Value == path {
			return true
		}
	}
	return false
}

func collectReferencedNames(file *ast.File, names map[string]bool) {
	called := make(map[*ast.SelectorExpr]bool)
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok {
				called[sel] = true
			}
		case *ast.SelectorExpr:
			if !called[n] {
				names[n.Sel.Name] = true
			}
		case *ast.BasicLit:
			if n.Kind == token.STRING {
				if s, err := strconv.Unquote(n.Value); err == nil {
					names[s] = true
				}
			}
		}
		return true
	})
}

// TestRegisteredActivitiesAreScheduledOrRecorded keeps registrations tied to
// what a workflow can run: an activity that only runs in-process inside
// another one (ImportDay's per-file imports) is not registered, and one kept
// for histories is listed in historyOnlyActivities with the workflow that
// recorded it.
func TestRegisteredActivitiesAreScheduledOrRecorded(t *testing.T) {
	registered := registeredWorkerActivities()
	scheduled := scheduledActivityNames(t)
	require.NotEmpty(t, registered)

	seen := make(map[string]bool, len(registered))
	for _, name := range registered {
		assert.False(t, seen[name], "%s registered twice", name)
		seen[name] = true
		_, historyOnly := historyOnlyActivities[name]
		assert.True(t, scheduled[name] || historyOnly,
			"%s is registered but no workflow schedules it: unregister it, or list the workflow whose histories recorded it in historyOnlyActivities", name)
	}
	for name, reason := range historyOnlyActivities {
		assert.True(t, seen[name], "historyOnlyActivities lists %s (%s), which is not registered", name, reason)
		assert.False(t, scheduled[name], "historyOnlyActivities lists %s, which workflow code schedules again", name)
	}
}
