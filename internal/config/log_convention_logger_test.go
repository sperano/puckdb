package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The logger choice rules of the wiki page puckdb/logging.md: workflow code
// logs through workflow.GetLogger, an activity method through
// activity.GetLogger and never also through the global zerolog logger.

const (
	temporalWorkflowImportPath = "go.temporal.io/sdk/workflow"
	temporalActivityImportPath = "go.temporal.io/sdk/activity"
)

// TestWorkflowFilesUseOnlyTheWorkflowLogger fails when a worker file that
// declares workflow code imports the global zerolog logger: workflow code
// logs through workflow.GetLogger, which stays silent during replay.
func TestWorkflowFilesUseOnlyTheWorkflowLogger(t *testing.T) {
	t.Parallel()
	for _, path := range nonTestGoFiles(t, filepath.Join("..", "worker")) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		require.NoError(t, err)
		if workflowFileImportsGlobalLogger(file) {
			t.Errorf("%s: workflow code imports %s; use workflow.GetLogger(ctx)", path, zerologGlobalImportPath)
		}
	}
}

func TestWorkflowFileImportsGlobalLogger(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		src  string
		want bool
	}{
		{"workflow code", `package s
import (
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/workflow"
)
func W(ctx workflow.Context) {}`, true},
		{"aliased workflow import", `package s
import (
	"github.com/rs/zerolog/log"
	wf "go.temporal.io/sdk/workflow"
)
func W(ctx wf.Context) {}`, true},
		{"activity code only", `package s
import (
	"context"
	"github.com/rs/zerolog/log"
)
func A(ctx context.Context) {}`, false},
		{"workflow code without the global logger", `package s
import "go.temporal.io/sdk/workflow"
func W(ctx workflow.Context) {}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "sample.go", tc.src, 0)
			require.NoError(t, err)
			assert.Equal(t, tc.want, workflowFileImportsGlobalLogger(file))
		})
	}
}

// workflowFileImportsGlobalLogger reports whether file imports the global
// zerolog logger and has a function taking a workflow.Context.
func workflowFileImportsGlobalLogger(file *ast.File) bool {
	workflowPkg, ok := importName(file, temporalWorkflowImportPath)
	if !ok {
		return false
	}
	if _, ok := importName(file, zerologGlobalImportPath); !ok {
		return false
	}
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		if field, ok := n.(*ast.Field); ok && typeName(field.Type) == workflowPkg+".Context" {
			found = true
		}
		return !found
	})
	return found
}

// TestActivityMethodsDoNotMixLoggers fails on a function under internal/
// that calls activity.GetLogger and also logs through the global zerolog
// logger.
func TestActivityMethodsDoNotMixLoggers(t *testing.T) {
	t.Parallel()
	for _, path := range nonTestGoFiles(t, "..") {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		require.NoError(t, err)
		for _, fn := range mixedLoggerFuncs(file) {
			t.Errorf("%s: %s mixes activity.GetLogger with the global zerolog logger", fset.Position(fn.Pos()), fn.Name.Name)
		}
	}
}

func TestMixedLoggerFuncs(t *testing.T) {
	t.Parallel()
	const src = `package s
import (
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/activity"
)
func (a *A) Mixed(ctx context.Context) {
	logger := activity.GetLogger(ctx)
	logger.Info("m")
	log.Warn().Msg("m")
}
func (a *A) MixedInClosure(ctx context.Context) {
	logger := activity.GetLogger(ctx)
	go func() { log.Info().Msg("m") }()
	logger.Info("m")
}
func (a *A) ActivityOnly(ctx context.Context) {
	activity.GetLogger(ctx).Info("m")
}
func helper() {
	log.Info().Msg("m")
}
`
	file, err := parser.ParseFile(token.NewFileSet(), "sample.go", src, 0)
	require.NoError(t, err)

	var names []string
	for _, fn := range mixedLoggerFuncs(file) {
		names = append(names, fn.Name.Name)
	}
	assert.Equal(t, []string{"Mixed", "MixedInClosure"}, names)
}

// mixedLoggerFuncs returns the functions of file that call
// activity.GetLogger and also use the global zerolog logger.
func mixedLoggerFuncs(file *ast.File) []*ast.FuncDecl {
	activityPkg, ok := importName(file, temporalActivityImportPath)
	if !ok {
		return nil
	}
	zerologPkg, ok := importName(file, zerologGlobalImportPath)
	if !ok {
		return nil
	}
	var mixed []*ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		usesActivity, usesGlobal := false, false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok {
				usesActivity = usesActivity || (pkg.Name == activityPkg && sel.Sel.Name == "GetLogger")
				usesGlobal = usesGlobal || pkg.Name == zerologPkg
			}
			return true
		})
		if usesActivity && usesGlobal {
			mixed = append(mixed, fn)
		}
	}
	return mixed
}

// importName returns the name file refers to importPath by.
func importName(file *ast.File, importPath string) (string, bool) {
	for _, spec := range file.Imports {
		path, _ := strconv.Unquote(spec.Path.Value)
		if path != importPath {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name, true
		}
		return filepath.Base(path), true
	}
	return "", false
}
