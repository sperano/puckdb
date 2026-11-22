package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseAPIKeys(t *testing.T) {
	keys, err := ParseAPIKeys([]byte("kelos: key-one\nmaurice: key-two\n"))
	require.NoError(t, err)
	require.Equal(t, 2, keys.Len())

	client, ok := keys.Lookup("key-one")
	require.True(t, ok)
	require.Equal(t, "kelos", client)

	client, ok = keys.Lookup("key-two")
	require.True(t, ok)
	require.Equal(t, "maurice", client)
}

func TestParseAPIKeysTrimsWhitespace(t *testing.T) {
	keys, err := ParseAPIKeys([]byte("  kelos  :  key-one  \n"))
	require.NoError(t, err)
	_, ok := keys.Lookup("key-one")
	require.True(t, ok)
}

func TestParseAPIKeysRejects(t *testing.T) {
	tests := map[string]struct {
		data string
		want string
	}{
		"empty file":        {"", "no client keys listed"},
		"comment only":      {"# nothing here\n", "no client keys listed"},
		"null document":     {"null\n", "not a mapping"},
		"not a mapping":     {"- one\n- two\n", "parse:"},
		"second document":   {"kelos: key-one\n---\nmaurice: key-two\n", "more than one YAML document"},
		"empty client name": {"\"\": key-one\n", "client name is empty"},
		"empty key":         {"kelos: \"\"\n", `client "kelos" has an empty key`},
		"duplicate key":     {"kelos: shared\nmaurice: shared\n", "share the same key"},
		"unknown field (not a map[string]string)": {"kelos:\n  key: one\n", "parse:"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := ParseAPIKeys([]byte(tt.data))
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestLoadAPIKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.yaml")
	require.NoError(t, os.WriteFile(path, []byte("kelos: key-one\n"), 0o600))

	keys, err := LoadAPIKeys(path)
	require.NoError(t, err)
	_, ok := keys.Lookup("key-one")
	require.True(t, ok)
}

func TestLoadAPIKeysMissingFile(t *testing.T) {
	_, err := LoadAPIKeys(filepath.Join(t.TempDir(), "missing.yaml"))
	require.ErrorContains(t, err, "read API keys file")
}

func TestLoadAPIKeysMalformedFileNamesPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.yaml")
	require.NoError(t, os.WriteFile(path, []byte(""), 0o600))

	_, err := LoadAPIKeys(path)
	require.ErrorContains(t, err, path)
	require.ErrorContains(t, err, "no client keys listed")
}

func mustKeys(t *testing.T, data string) APIKeys {
	t.Helper()
	keys, err := ParseAPIKeys([]byte(data))
	require.NoError(t, err)
	return keys
}

// requireAPIKeyRequest runs a request against RequireAPIKey(keys, next) and
// reports whether next was reached.
func requireAPIKeyRequest(t *testing.T, keys APIKeys, authHeader string) (reached bool, status int) {
	t.Helper()
	handler := RequireAPIKey(keys, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return reached, rec.Code
}

func TestRequireAPIKeyAcceptsConfiguredKey(t *testing.T) {
	keys := mustKeys(t, "kelos: key-one\n")
	reached, status := requireAPIKeyRequest(t, keys, "Bearer key-one")
	require.True(t, reached)
	require.Equal(t, http.StatusOK, status)
}

func TestRequireAPIKeyRejectsUnknownKey(t *testing.T) {
	keys := mustKeys(t, "kelos: key-one\n")
	reached, status := requireAPIKeyRequest(t, keys, "Bearer wrong-key")
	require.False(t, reached)
	require.Equal(t, http.StatusUnauthorized, status)
}

func TestRequireAPIKeyRejectsMissingHeader(t *testing.T) {
	keys := mustKeys(t, "kelos: key-one\n")
	reached, status := requireAPIKeyRequest(t, keys, "")
	require.False(t, reached)
	require.Equal(t, http.StatusUnauthorized, status)
}

func TestRequireAPIKeyRejectsWrongScheme(t *testing.T) {
	keys := mustKeys(t, "kelos: key-one\n")
	reached, status := requireAPIKeyRequest(t, keys, "Basic key-one")
	require.False(t, reached)
	require.Equal(t, http.StatusUnauthorized, status)
}

func TestRequireAPIKeyRejectsEmptyBearerToken(t *testing.T) {
	keys := mustKeys(t, "kelos: key-one\n")
	reached, status := requireAPIKeyRequest(t, keys, "Bearer ")
	require.False(t, reached)
	require.Equal(t, http.StatusUnauthorized, status)
}

// TestRequireAPIKeyWithNoEntriesRefusesEverything pins the fail-closed
// default: an APIKeys zero value (no --mcp-api-keys-file configured) must
// refuse every request, not let it through.
func TestRequireAPIKeyWithNoEntriesRefusesEverything(t *testing.T) {
	reached, status := requireAPIKeyRequest(t, APIKeys{}, "Bearer anything")
	require.False(t, reached)
	require.Equal(t, http.StatusUnauthorized, status)
}

func TestRequireAPIKeySetsWWWAuthenticateHeader(t *testing.T) {
	keys := mustKeys(t, "kelos: key-one\n")
	handler := RequireAPIKey(keys, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, `Bearer realm="puckdb-mcp"`, rec.Header().Get("WWW-Authenticate"))
}
