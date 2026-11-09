package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestProcessStdioUse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		expr string
		want string
	}{
		{`fmt.Println("x")`, "fmt.Println writes to stdout"},
		{`fmt.Printf("%d", 1)`, "fmt.Printf writes to stdout"},
		{`io.WriteString(os.Stderr, "x")`, "uses os.Stderr"},
		{`bufio.NewScanner(os.Stdin)`, "uses os.Stdin"},
		{`print("x")`, "print writes to stderr"},
		{`println("x")`, "println writes to stderr"},
		{`fmt.Sprintf("%d", 1)`, ""},
		{`fmt.Fprint(w, "x")`, ""},
		{`os.Getenv("HOME")`, ""},
		{`w.Println("x")`, ""},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			expr, err := parser.ParseExpr(tc.expr)
			require.NoError(t, err)
			var found []string
			ast.Inspect(expr, func(n ast.Node) bool {
				if what := processStdioUse(n); what != "" {
					found = append(found, what)
				}
				return true
			})
			if tc.want == "" {
				assert.Empty(t, found)
			} else {
				assert.Equal(t, []string{tc.want}, found)
			}
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
