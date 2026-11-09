package config

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The logging convention these tests enforce is on the wiki page
// puckdb/logging.md.

const (
	zerologGlobalImportPath = "github.com/rs/zerolog/log"
	temporalLogImportPath   = "go.temporal.io/sdk/log"
)

// logKeyPattern is the shape of every structured log key: snake_case.
var logKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// renamedLogKeys are snake_case keys with a canonical spelling elsewhere.
var renamedLogKeys = map[string]string{"err": "error"}

// logKeyRoots are the directories whose non-test files are scanned, relative
// to this package.
var logKeyRoots = []string{"..", filepath.Join("..", "..", "cmd")}

// zerologFieldMethods are the *zerolog.Event and zerolog.Context methods
// whose first argument is a field key.
var zerologFieldMethods = map[string]bool{
	"Str": true, "Strs": true, "Stringer": true, "Stringers": true, "Bytes": true, "Hex": true, "RawJSON": true,
	"Int": true, "Int8": true, "Int16": true, "Int32": true, "Int64": true, "Ints": true, "Ints64": true,
	"Uint": true, "Uint8": true, "Uint16": true, "Uint32": true, "Uint64": true, "Uints": true, "Uints64": true,
	"Float32": true, "Float64": true, "Floats32": true, "Floats64": true, "Bool": true, "Bools": true,
	"Dur": true, "Durs": true, "Time": true, "Times": true, "TimeDiff": true, "Interface": true, "Any": true,
	"AnErr": true, "Errs": true, "Dict": true, "Array": true, "Object": true, "IPAddr": true, "MACAddr": true, "Type": true,
}

// zerologChainStarts are the logger methods that begin a zerolog event or
// context chain.
var zerologChainStarts = map[string]bool{
	"Trace": true, "Debug": true, "Info": true, "Warn": true, "Error": true, "Fatal": true, "Panic": true,
	"Log": true, "With": true, "Err": true, "WithLevel": true,
}

// temporalLogLevels are the methods of go.temporal.io/sdk/log.Logger, which
// take a message and then alternating keys and values.
var temporalLogLevels = map[string]bool{"Debug": true, "Info": true, "Warn": true, "Error": true}

type logKeyViolation struct {
	pos  token.Position
	what string
}

// TestLogKeysAreStableSnakeCase fails on a structured log key, zerolog or
// Temporal, that is not snake_case, on a Temporal key that is not a string
// literal, and on a Temporal call with an unpaired or repeated key.
func TestLogKeysAreStableSnakeCase(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, root := range logKeyRoots {
		for _, path := range nonTestGoFiles(t, root) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, 0)
			require.NoError(t, err)
			keys, violations := logKeys(fset, file)
			for _, key := range keys {
				seen[key] = true
			}
			for _, v := range violations {
				t.Errorf("%s: %s", v.pos, v.what)
			}
		}
	}
	// The detection itself must still find both logging styles.
	require.True(t, seen["pool_id"], "no Temporal logger key found: the detection is broken")
	require.True(t, seen["player_id"], "no zerolog key found: the detection is broken")
}

// TestLogKeysDetectsViolations runs the detection on a sample with one
// violation of each kind.
func TestLogKeysDetectsViolations(t *testing.T) {
	t.Parallel()
	const src = `package sample

func f(ctx context.Context, e *zerolog.Event) {
	log.Info().Str("ok_key", "").Int("playerID", 1).Msg("m")
	e.Str("leagueID", "")
	logger := activity.GetLogger(ctx)
	logger.Info("m", "ok_key", 1, "seasonID", 2)
	logger.Warn("m", "err", err)
	logger.Info("m", key, 1)
	logger.Info("m", "lonely")
	workflow.GetLogger(ctx).Info("m", "count", 1, "count", 2)
	flags.Int("not-a-log-key", 0, "pflag, not zerolog")
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", src, 0)
	require.NoError(t, err)

	keys, violations := logKeys(fset, file)

	assert.NotContains(t, keys, "not-a-log-key")
	var what []string
	for _, v := range violations {
		what = append(what, v.what)
	}
	assert.ElementsMatch(t, []string{
		`log key "playerID" is not snake_case`,
		`log key "leagueID" is not snake_case`,
		`log key "seasonID" is not snake_case`,
		`log key "err": use "error"`,
		"Temporal log key is not a string literal",
		"Temporal log call has a key without a value",
		`Temporal log call repeats key "count"`,
	}, what)
}

// logKeys returns the structured log keys of file and the convention
// violations among them. Detection is syntactic: a zerolog field call
// counts when its receiver chain starts at a level or With call or at a
// variable declared as a zerolog type; a Temporal call when its receiver is
// a GetLogger call or a variable assigned one or declared as log.Logger.
func logKeys(fset *token.FileSet, file *ast.File) ([]string, []logKeyViolation) {
	zerologVars, temporalVars := loggerVars(file)
	var keys []string
	var violations []logKeyViolation
	report := func(node ast.Node, format string, args ...any) {
		violations = append(violations, logKeyViolation{fset.Position(node.Pos()), fmt.Sprintf(format, args...)})
	}
	checkKey := func(lit *ast.BasicLit) string {
		key, _ := strconv.Unquote(lit.Value)
		keys = append(keys, key)
		if want, ok := renamedLogKeys[key]; ok {
			report(lit, "log key %q: use %q", key, want)
		} else if !logKeyPattern.MatchString(key) {
			report(lit, "log key %q is not snake_case", key)
		}
		return key
	}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		if zerologFieldMethods[sel.Sel.Name] && isZerologChain(sel.X, zerologVars) {
			if lit, ok := stringLit(call.Args[0]); ok {
				checkKey(lit)
			}
		}
		if temporalLogLevels[sel.Sel.Name] && isTemporalLogger(sel.X, temporalVars) && call.Ellipsis == token.NoPos {
			checkTemporalCall(call, checkKey, report)
		}
		return true
	})
	return keys, violations
}

// checkTemporalCall checks the key/value arguments after the message.
func checkTemporalCall(call *ast.CallExpr, checkKey func(*ast.BasicLit) string, report func(ast.Node, string, ...any)) {
	pairs := call.Args[1:]
	if len(pairs)%2 != 0 {
		report(call, "Temporal log call has a key without a value")
	}
	seen := map[string]bool{}
	for i := 0; i < len(pairs); i += 2 {
		lit, ok := stringLit(pairs[i])
		if !ok {
			report(pairs[i], "Temporal log key is not a string literal")
			continue
		}
		key := checkKey(lit)
		if seen[key] {
			report(lit, "Temporal log call repeats key %q", key)
		}
		seen[key] = true
	}
}

func stringLit(e ast.Expr) (*ast.BasicLit, bool) {
	lit, ok := e.(*ast.BasicLit)
	return lit, ok && lit.Kind == token.STRING
}

// loggerVars returns the names file declares as zerolog loggers, events or
// contexts, and as Temporal loggers (declared log.Logger or assigned from a
// GetLogger call).
func loggerVars(file *ast.File) (map[string]bool, map[string]bool) {
	zerologVars, temporalVars := map[string]bool{}, map[string]bool{}
	temporalType := ""
	for _, spec := range file.Imports {
		if path, _ := strconv.Unquote(spec.Path.Value); path == temporalLogImportPath {
			temporalType = "log.Logger"
			if spec.Name != nil {
				temporalType = spec.Name.Name + ".Logger"
			}
		}
	}
	declare := func(names []*ast.Ident, typ ast.Expr) {
		switch name := typeName(typ); {
		case strings.HasPrefix(name, "zerolog.") || strings.HasPrefix(name, "*zerolog."):
			for _, n := range names {
				zerologVars[n.Name] = true
			}
		case temporalType != "" && name == temporalType:
			for _, n := range names {
				temporalVars[n.Name] = true
			}
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Field:
			declare(n.Names, n.Type)
		case *ast.ValueSpec:
			declare(n.Names, n.Type)
			for i, value := range n.Values {
				if i < len(n.Names) && isGetLoggerCall(value) {
					temporalVars[n.Names[i].Name] = true
				}
			}
		case *ast.AssignStmt:
			for i, rhs := range n.Rhs {
				if i >= len(n.Lhs) || !isGetLoggerCall(rhs) {
					continue
				}
				if id, ok := n.Lhs[i].(*ast.Ident); ok {
					temporalVars[id.Name] = true
				}
			}
		}
		return true
	})
	return zerologVars, temporalVars
}

func typeName(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.StarExpr:
		return "*" + typeName(e.X)
	case *ast.SelectorExpr:
		return typeName(e.X) + "." + e.Sel.Name
	case *ast.Ident:
		return e.Name
	}
	return ""
}

func isGetLoggerCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "GetLogger"
}

func isTemporalLogger(e ast.Expr, temporalVars map[string]bool) bool {
	if id, ok := e.(*ast.Ident); ok {
		return temporalVars[id.Name]
	}
	return isGetLoggerCall(e)
}

// isZerologChain walks a method chain down to its receiver and reports
// whether it is a zerolog chain: a level or With call on the global log
// package, a zero-argument level call, or a variable of a zerolog type.
func isZerologChain(e ast.Expr, zerologVars map[string]bool) bool {
	for {
		switch x := e.(type) {
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok {
				return false
			}
			if zerologChainStarts[sel.Sel.Name] {
				if id, ok := sel.X.(*ast.Ident); ok && (id.Name == "log" || id.Name == "zerolog") {
					return true
				}
				if len(x.Args) == 0 {
					return true
				}
			}
			e = sel.X
		case *ast.SelectorExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		case *ast.Ident:
			return zerologVars[x.Name]
		default:
			return false
		}
	}
}

// TestWorkflowFilesUseOnlyTheWorkflowLogger fails when a worker file that
// declares workflow code imports the global zerolog logger: workflow code
// logs through workflow.GetLogger, which stays silent during replay.
func TestWorkflowFilesUseOnlyTheWorkflowLogger(t *testing.T) {
	t.Parallel()
	for _, path := range nonTestGoFiles(t, filepath.Join("..", "worker")) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		require.NoError(t, err)
		if !importsPath(file, zerologGlobalImportPath) || !declaresWorkflowCode(file) {
			continue
		}
		t.Errorf("%s: workflow code imports %s; use workflow.GetLogger(ctx)", path, zerologGlobalImportPath)
	}
}

func importsPath(file *ast.File, importPath string) bool {
	for _, spec := range file.Imports {
		if path, _ := strconv.Unquote(spec.Path.Value); path == importPath {
			return true
		}
	}
	return false
}

// declaresWorkflowCode reports whether a function of file takes a
// workflow.Context.
func declaresWorkflowCode(file *ast.File) bool {
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		if field, ok := n.(*ast.Field); ok && typeName(field.Type) == "workflow.Context" {
			found = true
		}
		return !found
	})
	return found
}

// processStdioAllowed are the library files that may name the process's
// standard streams: the logger setup writes the console log to stderr.
var processStdioAllowed = map[string]bool{filepath.Join("..", "config", "log.go"): true}

// TestLibraryPackagesDoNotWriteProcessStdio fails when a non-test file under
// internal/ prints with fmt.Print*, print or println, or names os.Stdin,
// os.Stdout or os.Stderr. Library code logs through zerolog and writes user
// output to an io.Writer its caller passes in.
func TestLibraryPackagesDoNotWriteProcessStdio(t *testing.T) {
	t.Parallel()
	for _, path := range nonTestGoFiles(t, "..") {
		if processStdioAllowed[path] {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		require.NoError(t, err)
		ast.Inspect(file, func(n ast.Node) bool {
			if what := processStdioUse(n); what != "" {
				t.Errorf("%s: %s; take an io.Writer or log through zerolog", fset.Position(n.Pos()), what)
			}
			return true
		})
	}
}

func processStdioUse(n ast.Node) string {
	switch n := n.(type) {
	case *ast.SelectorExpr:
		pkg, ok := n.X.(*ast.Ident)
		if !ok {
			return ""
		}
		switch {
		case pkg.Name == "fmt" && strings.HasPrefix(n.Sel.Name, "Print"):
			return "fmt." + n.Sel.Name + " writes to stdout"
		case pkg.Name == "os" && (n.Sel.Name == "Stdin" || n.Sel.Name == "Stdout" || n.Sel.Name == "Stderr"):
			return "uses os." + n.Sel.Name
		}
	case *ast.CallExpr:
		if id, ok := n.Fun.(*ast.Ident); ok && (id.Name == "print" || id.Name == "println") {
			return id.Name + " writes to stderr"
		}
	}
	return ""
}
