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
var renamedLogKeys = map[string]string{
	"err": "error", "gameid": "game_id", "playerid": "player_id", "teamid": "team_id",
	"leagueid": "league_id", "seasonid": "season_id",
}

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
// literal, and on a Temporal call with an unpaired or repeated key. Calls
// of a package's key/value forwarders (logFetched) are checked as Temporal
// calls.
func TestLogKeysAreStableSnakeCase(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	forwarderCount := 0
	for _, root := range logKeyRoots {
		for _, pkg := range parsePackages(t, root) {
			forwarders := keyvalForwarders(pkg.files)
			forwarderCount += len(forwarders)
			for _, file := range pkg.files {
				keys, violations := logKeys(pkg.fset, file, forwarders)
				for _, key := range keys {
					seen[key] = true
				}
				for _, v := range violations {
					t.Errorf("%s: %s", v.pos, v.what)
				}
			}
		}
	}
	// The detection itself must still find both logging styles.
	require.True(t, seen["pool_id"], "no Temporal logger key found: the detection is broken")
	require.True(t, seen["player_id"], "no zerolog key found: the detection is broken")
	require.NotZero(t, forwarderCount, "no key/value forwarder found: the detection is broken")
}

// parsedPackage is the non-test files of one directory.
type parsedPackage struct {
	fset  *token.FileSet
	files []*ast.File
}

// parsePackages parses the non-test files under root, grouped by directory.
func parsePackages(t *testing.T, root string) []parsedPackage {
	t.Helper()
	byDir := map[string]*parsedPackage{}
	var dirs []string
	for _, path := range nonTestGoFiles(t, root) {
		dir := filepath.Dir(path)
		pkg, ok := byDir[dir]
		if !ok {
			pkg = &parsedPackage{fset: token.NewFileSet()}
			byDir[dir] = pkg
			dirs = append(dirs, dir)
		}
		file, err := parser.ParseFile(pkg.fset, path, nil, 0)
		require.NoError(t, err)
		pkg.files = append(pkg.files, file)
	}
	pkgs := make([]parsedPackage, 0, len(dirs))
	for _, dir := range dirs {
		pkgs = append(pkgs, *byDir[dir])
	}
	return pkgs
}

// TestLogKeysDetectsViolations runs the detection on a sample with one
// violation of each kind.
func TestLogKeysDetectsViolations(t *testing.T) {
	t.Parallel()
	const src = `package sample

func f(ctx context.Context, e *zerolog.Event) {
	log.Info().Str("ok_key", "").Int("playerID", 1).Msg("m")
	e.Str("leagueID", "")
	evt := log.Debug().Str("ok_key", "")
	evt = evt.Int("teamID", 1)
	var ctxLog = log.With().Str("ok_key", "")
	ctxLog.Str("gameid", "")
	logFetched(logger, origin, "League", "season", 1, "weekID", 2)
	logFetched(logger, origin, "League", "lonely")
	logFetched(logger, origin, "League", keyvals...)
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

	keys, violations := logKeys(fset, file, map[string]int{"logFetched": 3})

	assert.NotContains(t, keys, "not-a-log-key")
	var what []string
	for _, v := range violations {
		what = append(what, v.what)
	}
	assert.ElementsMatch(t, []string{
		`log key "playerID" is not snake_case`,
		`log key "leagueID" is not snake_case`,
		`log key "seasonID" is not snake_case`,
		`log key "teamID" is not snake_case`,
		`log key "gameid": use "game_id"`,
		`log key "weekID" is not snake_case`,
		`log key "err": use "error"`,
		"Temporal log key is not a string literal",
		"Temporal log call has a key without a value",
		"Temporal log call has a key without a value",
		`Temporal log call repeats key "count"`,
	}, what)
}

// TestKeyvalForwardersFindsTemporalForwarders checks which functions count
// as key/value forwarders.
func TestKeyvalForwardersFindsTemporalForwarders(t *testing.T) {
	t.Parallel()
	const src = `package sample

import "go.temporal.io/sdk/log"

func logFetched(logger log.Logger, origin int, what string, keyvals ...any) {
	logger.Info(what, keyvals...)
}

func notLogging(prefix string, args ...any) {
	fmt.Sprint(args...)
}

func (r recv) method(logger log.Logger, keyvals ...any) {
	logger.Info("m", keyvals...)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", src, 0)
	require.NoError(t, err)

	assert.Equal(t, map[string]int{"logFetched": 3}, keyvalForwarders([]*ast.File{file}))
}

// logKeys returns the structured log keys of file and the convention
// violations among them. Detection is syntactic: a zerolog field call
// counts when its receiver chain starts at a level or With call or at a
// variable declared as a zerolog type or assigned a zerolog chain; a
// Temporal call when its receiver is a GetLogger call or a variable
// assigned one or declared as log.Logger, or when it calls one of
// forwarders (name to index of the first key argument).
func logKeys(fset *token.FileSet, file *ast.File, forwarders map[string]int) ([]string, []logKeyViolation) {
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
		if id, ok := call.Fun.(*ast.Ident); ok {
			if first, ok := forwarders[id.Name]; ok && call.Ellipsis == token.NoPos && len(call.Args) >= first {
				checkKeyvals(call, call.Args[first:], checkKey, report)
			}
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
			checkKeyvals(call, call.Args[1:], checkKey, report)
		}
		return true
	})
	return keys, violations
}

// checkKeyvals checks the alternating key/value arguments pairs of a
// Temporal log call.
func checkKeyvals(call *ast.CallExpr, pairs []ast.Expr, checkKey func(*ast.BasicLit) string, report func(ast.Node, string, ...any)) {
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

// keyvalForwarders returns the functions of a package that pass their
// variadic ...any parameter on to a Temporal log call, by name, with the
// index of that parameter: their callers' arguments from there on are log
// keys and values. Methods are not looked at.
func keyvalForwarders(files []*ast.File) map[string]int {
	forwarders := map[string]int{}
	for _, file := range files {
		_, temporalVars := loggerVars(file)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil {
				continue
			}
			if name, index, ok := variadicAnyParam(fn.Type); ok && forwardsKeyvals(fn.Body, name, temporalVars) {
				forwarders[fn.Name.Name] = index
			}
		}
	}
	return forwarders
}

// variadicAnyParam returns the name and index of a final ...any or
// ...interface{} parameter.
func variadicAnyParam(fn *ast.FuncType) (string, int, bool) {
	params := fn.Params.List
	if len(params) == 0 {
		return "", 0, false
	}
	last := params[len(params)-1]
	ellipsis, ok := last.Type.(*ast.Ellipsis)
	if !ok || len(last.Names) != 1 {
		return "", 0, false
	}
	switch elt := ellipsis.Elt.(type) {
	case *ast.Ident:
		if elt.Name != "any" {
			return "", 0, false
		}
	case *ast.InterfaceType:
		if len(elt.Methods.List) != 0 {
			return "", 0, false
		}
	default:
		return "", 0, false
	}
	index := 0
	for _, p := range params[:len(params)-1] {
		index += max(len(p.Names), 1)
	}
	return last.Names[0].Name, index, true
}

// forwardsKeyvals reports whether body passes param... to a Temporal log
// level call.
func forwardsKeyvals(body *ast.BlockStmt, param string, temporalVars map[string]bool) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || call.Ellipsis == token.NoPos || len(call.Args) == 0 {
			return !found
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		last, isIdent := call.Args[len(call.Args)-1].(*ast.Ident)
		if ok && isIdent && last.Name == param && temporalLogLevels[sel.Sel.Name] && isTemporalLogger(sel.X, temporalVars) {
			found = true
		}
		return !found
	})
	return found
}

func stringLit(e ast.Expr) (*ast.BasicLit, bool) {
	lit, ok := e.(*ast.BasicLit)
	return lit, ok && lit.Kind == token.STRING
}

// loggerVars returns the names file declares as zerolog loggers, events or
// contexts (declared with a zerolog type or assigned a zerolog chain), and
// as Temporal loggers (declared log.Logger or assigned from a GetLogger
// call).
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
				if i < len(n.Names) {
					assign(n.Names[i], value, zerologVars, temporalVars)
				}
			}
		case *ast.AssignStmt:
			for i, rhs := range n.Rhs {
				if i >= len(n.Lhs) {
					break
				}
				if id, ok := n.Lhs[i].(*ast.Ident); ok {
					assign(id, rhs, zerologVars, temporalVars)
				}
			}
		}
		return true
	})
	return zerologVars, temporalVars
}

// assign records name as a Temporal logger when value is a GetLogger call,
// and as a zerolog event or context when value is a zerolog chain.
func assign(name *ast.Ident, value ast.Expr, zerologVars, temporalVars map[string]bool) {
	if isGetLoggerCall(value) {
		temporalVars[name.Name] = true
	} else if _, ok := value.(*ast.CallExpr); ok && isZerologChain(value, zerologVars) {
		zerologVars[name.Name] = true
	}
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
