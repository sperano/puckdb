package draft

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// documentedPackages are the draft helper's packages, by directory under
// internal/. Every exported declaration in them carries a doc comment.
var documentedPackages = []string{"draft", "draftboard", "draftrank", "draftrecommend", "draftsession", "projection"}

// interfaceMethods implement a standard interface whose contract is its
// documentation, so they need no comment of their own.
var interfaceMethods = map[string]bool{"Error": true, "String": true, "Unwrap": true}

// TestDraftHelperPackagesDocumentExports fails on an exported function,
// method, type, constant or variable without a doc comment (its own, a
// trailing line comment, or its declaration group's), and on a package
// without a package comment.
func TestDraftHelperPackagesDocumentExports(t *testing.T) {
	t.Parallel()
	for _, pkg := range documentedPackages {
		dir := filepath.Join("..", pkg)
		files := parseNonTestFiles(t, dir)
		hasPackageDoc := false
		for _, file := range files {
			hasPackageDoc = hasPackageDoc || file.ast.Doc != nil
			for _, missing := range undocumentedExports(file.fset, file.ast) {
				t.Errorf("%s: exported %s has no doc comment", missing.pos, missing.name)
			}
		}
		if !hasPackageDoc {
			t.Errorf("%s: package has no package comment", dir)
		}
	}
}

func TestUndocumentedExportsFindsEveryKind(t *testing.T) {
	t.Parallel()
	const source = `package p

// Documented is fine.
func Documented() {}

func Bare() {}

type T struct{}

func (T) Method() {}

func (T) Error() string { return "" }

type hidden struct{}

func (hidden) Method() {}

// G is a documented group.
const (
	A = 1
	B = 2
)

const (
	// C is documented.
	C = 3
	D = 4
	E = 5 // E is documented.
	f = 6
)

var V = 7
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", source, parser.ParseComments)
	require.NoError(t, err)

	var names []string
	for _, missing := range undocumentedExports(fset, file) {
		names = append(names, missing.name)
	}
	require.Equal(t, []string{"Bare", "T", "T.Method", "D", "V"}, names)
}

type parsedFile struct {
	fset *token.FileSet
	ast  *ast.File
}

type undocumented struct {
	pos  token.Position
	name string
}

func parseNonTestFiles(t *testing.T, dir string) []parsedFile {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	fset := token.NewFileSet()
	var files []parsedFile
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments)
		require.NoError(t, err)
		files = append(files, parsedFile{fset: fset, ast: file})
	}
	require.NotEmpty(t, files, "no Go files in %s", dir)
	return files
}

func undocumentedExports(fset *token.FileSet, file *ast.File) []undocumented {
	var missing []undocumented
	report := func(node ast.Node, name string) {
		missing = append(missing, undocumented{pos: fset.Position(node.Pos()), name: name})
	}
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			if name, exported := funcName(decl); exported && decl.Doc == nil {
				report(decl, name)
			}
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				if name, exported := specName(spec); exported && !specDocumented(decl, spec) {
					report(spec, name)
				}
			}
		}
	}
	return missing
}

// funcName names a function or a method of an exported type, and says
// whether it needs a doc comment.
func funcName(decl *ast.FuncDecl) (string, bool) {
	if !decl.Name.IsExported() {
		return "", false
	}
	if decl.Recv == nil {
		return decl.Name.Name, true
	}
	receiver := receiverTypeName(decl.Recv.List[0].Type)
	if !ast.IsExported(receiver) || interfaceMethods[decl.Name.Name] {
		return "", false
	}
	return receiver + "." + decl.Name.Name, true
}

func receiverTypeName(expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.StarExpr:
		return receiverTypeName(expr.X)
	case *ast.IndexExpr:
		return receiverTypeName(expr.X)
	case *ast.IndexListExpr:
		return receiverTypeName(expr.X)
	case *ast.Ident:
		return expr.Name
	}
	return ""
}

// specName names the first exported identifier a type or value spec
// declares.
func specName(spec ast.Spec) (string, bool) {
	switch spec := spec.(type) {
	case *ast.TypeSpec:
		return spec.Name.Name, spec.Name.IsExported()
	case *ast.ValueSpec:
		for _, name := range spec.Names {
			if name.IsExported() {
				return name.Name, true
			}
		}
	}
	return "", false
}

func specDocumented(decl *ast.GenDecl, spec ast.Spec) bool {
	if decl.Doc != nil {
		return true
	}
	switch spec := spec.(type) {
	case *ast.TypeSpec:
		return spec.Doc != nil || spec.Comment != nil
	case *ast.ValueSpec:
		return spec.Doc != nil || spec.Comment != nil
	}
	return false
}
