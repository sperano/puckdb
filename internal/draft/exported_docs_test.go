package draft

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The draft helper's package directories: every internal/draft* package and
// internal/projection. Every exported declaration in them carries a doc
// comment.
const (
	draftPackagesGlob = "../draft*"
	projectionPackage = "../projection"
)

// interfaceMethods are the methods of error, fmt.Stringer and the
// errors.Unwrap protocol, by name, with the result types their signature
// must have (and no parameters). Their contract is their documentation, so a
// method with exactly that signature needs no comment of its own; one that
// only shares the name does.
var interfaceMethods = map[string][]string{
	"Error":  {"string"},
	"String": {"string"},
	"Unwrap": {"error", "[]error"},
}

// TestDraftHelperPackagesDocumentExports fails on an exported function,
// method, type, constant or variable without a doc comment (its own or its
// declaration group's), and on a package without a package comment.
func TestDraftHelperPackagesDocumentExports(t *testing.T) {
	t.Parallel()
	dirs, err := filepath.Glob(draftPackagesGlob)
	require.NoError(t, err)
	require.Contains(t, dirs, filepath.Join("..", "draftrank"), "the package glob itself is broken")
	for _, dir := range append(dirs, projectionPackage) {
		fset, files := parseNonTestFiles(t, dir)
		hasPackageDoc := false
		for _, file := range files {
			hasPackageDoc = hasPackageDoc || file.Doc != nil
			for _, missing := range undocumentedExports(fset, file) {
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

func (T) String(int) string { return "" }

func (*T) Unwrap() []error { return nil }

type U struct{}

func (*U) Error() (message string) { return "" }

func (U) String(...int) string { return "" }

func (U) Unwrap() (error, bool) { return nil, false }

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
	E = 5 // A trailing comment is not a doc comment.
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
	require.Equal(t, []string{"Bare", "T", "T.Method", "T.String", "U", "U.String", "U.Unwrap", "D", "E", "V"}, names)
}

type undocumented struct {
	pos  token.Position
	name string
}

func parseNonTestFiles(t *testing.T, dir string) (*token.FileSet, []*ast.File) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	fset := token.NewFileSet()
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments)
		require.NoError(t, err)
		files = append(files, file)
	}
	require.NotEmpty(t, files, "no Go files in %s", dir)
	return fset, files
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
	if !ast.IsExported(receiver) || implementsInterfaceMethod(decl) {
		return "", false
	}
	return receiver + "." + decl.Name.Name, true
}

// implementsInterfaceMethod reports whether a method has the exact
// signature of an interfaceMethods entry: no parameters and one result of a
// listed type.
func implementsInterfaceMethod(decl *ast.FuncDecl) bool {
	results, known := interfaceMethods[decl.Name.Name]
	signature := decl.Type
	if !known || signature.Params.NumFields() != 0 || signature.Results.NumFields() != 1 {
		return false
	}
	return slices.Contains(results, types.ExprString(signature.Results.List[0].Type))
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
		return spec.Doc != nil
	case *ast.ValueSpec:
		return spec.Doc != nil
	}
	return false
}
