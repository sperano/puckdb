package sqlcdb

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

// sqlcOnlyPackages run every statement through this package's generated
// queries: their SQL lives in queries/, where sqlc checks it against the
// migrations and the query metric can label it.
var sqlcOnlyPackages = []string{"draftwatch", "draftboard", "draftrecommend"}

// rawSQLMethods are the pgx methods (pool, connection or transaction) that
// take hand-written SQL.
var rawSQLMethods = map[string]bool{
	"Exec": true, "Query": true, "QueryRow": true, "SendBatch": true, "CopyFrom": true,
}

// TestDraftStoresQueryOnlyThroughSqlc fails when a non-test file of an
// sqlc-only package calls a pgx method that takes hand-written SQL.
func TestDraftStoresQueryOnlyThroughSqlc(t *testing.T) {
	t.Parallel()
	for _, pkg := range sqlcOnlyPackages {
		for _, path := range packageSourceFiles(t, filepath.Join("..", pkg)) {
			for _, pos := range rawSQLCalls(t, path) {
				t.Errorf("%s: hand-written SQL; add the statement to internal/sqlcdb/queries and run sqlc generate", pos)
			}
		}
	}
}

func TestRawSQLCallsFindsPoolTransactionAndConnectionCalls(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "store.go")
	source := `package store

func f() {
	_, _ = pool.Exec(ctx, "DELETE FROM t")
	_ = tx.QueryRow(ctx, query, key).Scan(&v)
	rows, _ := conn.Query(ctx, query)
	_ = queries.ListDraftShortlist(ctx, key)
}
`
	require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	require.Len(t, rawSQLCalls(t, path), 3)
}

// packageSourceFiles lists the non-test Go files directly in dir.
func packageSourceFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var files []string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			files = append(files, filepath.Join(dir, name))
		}
	}
	require.NotEmpty(t, files, "no Go files in %s", dir)
	return files
}

// rawSQLCalls returns the position of every call of a rawSQLMethods method
// in path.
func rawSQLCalls(t *testing.T, path string) []token.Position {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	require.NoError(t, err)
	var calls []token.Position
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && rawSQLMethods[selector.Sel.Name] {
			calls = append(calls, fset.Position(call.Pos()))
		}
		return true
	})
	return calls
}
