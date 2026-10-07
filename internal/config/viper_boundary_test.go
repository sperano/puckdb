package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// viperFreePackages take their settings as values. Only cmd reads viper
// for them (cmd/settings.go), so their tests need no global state.
var viperFreePackages = []string{"store", "cache", "database", "temporal", "httpx", "graph"}

const (
	viperImportPath  = "github.com/spf13/viper"
	configImportPath = "github.com/sperano/puckdb/internal/config"
	viperPackageName = "viper"
)

// TestLibraryPackagesDoNotReadViper fails when a non-test file of a
// viper-free package imports viper or calls a config function that reads
// it, directly or through another config function.
func TestLibraryPackagesDoNotReadViper(t *testing.T) {
	t.Parallel()
	readers := viperReadingFuncs(t)
	require.Contains(t, readers, "GetYahooSeasonsConfig", "the reader detection itself is broken")

	for _, pkg := range viperFreePackages {
		for _, path := range nonTestGoFiles(t, filepath.Join("..", pkg)) {
			for _, violation := range viperUses(t, path, readers) {
				t.Errorf("%s: %s; take the setting as a value and read it in cmd/settings.go", violation.pos, violation.what)
			}
		}
	}
}

type viperViolation struct {
	pos  token.Position
	what string
}

// nonTestGoFiles lists the Go files under dir, subpackages included,
// without tests and testdata.
func nonTestGoFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == "testdata" {
			return filepath.SkipDir
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, files, "no Go files under %s", dir)
	return files
}

// viperReadingFuncs returns the package-level functions of this package
// that use viper, or call one that does. Methods are left out: the guard
// matches config.<Name> selectors, which only name package-level
// functions, so a viper-reading method would need a check of its own.
func viperReadingFuncs(t *testing.T) map[string]bool {
	t.Helper()
	direct := map[string]bool{}
	calls := map[string][]string{}
	fset := token.NewFileSet()
	for _, path := range nonTestGoFiles(t, ".") {
		file, err := parser.ParseFile(fset, path, nil, 0)
		require.NoError(t, err)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.SelectorExpr:
					if ident, ok := n.X.(*ast.Ident); ok && ident.Name == viperPackageName {
						direct[fn.Name.Name] = true
					}
				case *ast.CallExpr:
					if ident, ok := n.Fun.(*ast.Ident); ok {
						calls[fn.Name.Name] = append(calls[fn.Name.Name], ident.Name)
					}
				}
				return true
			})
		}
	}
	return closeOverCalls(direct, calls)
}

// closeOverCalls adds every caller of a reader to readers until no more
// are found.
func closeOverCalls(readers map[string]bool, calls map[string][]string) map[string]bool {
	for changed := true; changed; {
		changed = false
		for caller, callees := range calls {
			if readers[caller] {
				continue
			}
			for _, callee := range callees {
				if readers[callee] {
					readers[caller] = true
					changed = true
					break
				}
			}
		}
	}
	return readers
}

// viperUses reports the viper import and calls to config readers in path.
func viperUses(t *testing.T, path string, readers map[string]bool) []viperViolation {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	require.NoError(t, err)

	var violations []viperViolation
	configName := ""
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		require.NoError(t, err)
		switch importPath {
		case viperImportPath:
			violations = append(violations, viperViolation{fset.Position(spec.Pos()), "imports viper"})
		case configImportPath:
			configName = filepath.Base(configImportPath)
			if spec.Name != nil {
				configName = spec.Name.Name
			}
		}
	}
	if configName == "" {
		return violations
	}
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == configName && readers[sel.Sel.Name] {
			violations = append(violations, viperViolation{fset.Position(sel.Pos()), "uses config." + sel.Sel.Name + ", which reads viper"})
		}
		return true
	})
	return violations
}
