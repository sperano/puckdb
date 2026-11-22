package mcpserver

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

// authorizationHeader is the HTTP header carrying the bearer token.
const authorizationHeader = "Authorization"

// bearerPrefix precedes the token in the Authorization header.
const bearerPrefix = "Bearer "

// APIKeys maps a bearer token presented by an MCP client to the client name
// it identifies, as configured for one mcp-server instance (LoadAPIKeys).
// The zero value has no entries and so authenticates nothing: RequireAPIKey
// refuses every request with it.
type APIKeys struct {
	byKey map[string]string
}

// Len is the number of configured client keys.
func (k APIKeys) Len() int { return len(k.byKey) }

// Lookup returns the client name for a presented key; ok is false for an
// empty key or one not configured.
func (k APIKeys) Lookup(key string) (client string, ok bool) {
	if key == "" {
		return "", false
	}
	client, ok = k.byKey[key]
	return client, ok
}

// LoadAPIKeys reads and parses the API keys file at path (ParseAPIKeys).
func LoadAPIKeys(path string) (APIKeys, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return APIKeys{}, fmt.Errorf("read API keys file: %w", err)
	}
	keys, err := ParseAPIKeys(data)
	if err != nil {
		return APIKeys{}, fmt.Errorf("API keys file %s: %w", path, err)
	}
	return keys, nil
}

// ParseAPIKeys parses an API keys file: a YAML mapping of client names to
// their bearer key, for example
//
//	kelos: 3f9c1b7e8a2d4f6b9c0e1a2b3c4d5e6f
//	maurice: a07e4d9c8b7a6f5e4d3c2b1a09f8e7d6
//
// It refuses an empty client name or key, two client names sharing one key,
// a root that is not a mapping (null or empty included) and a second YAML
// document.
func ParseAPIKeys(data []byte) (APIKeys, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var entries map[string]string
	switch err := dec.Decode(&entries); {
	case errors.Is(err, io.EOF):
		return APIKeys{}, errors.New("parse: no client keys listed")
	case err != nil:
		return APIKeys{}, fmt.Errorf("parse: %w", err)
	case entries == nil:
		return APIKeys{}, errors.New("parse: the file is not a mapping of client names to keys")
	}
	switch err := dec.Decode(new(yaml.Node)); {
	case err == nil:
		return APIKeys{}, errors.New("parse: the file holds more than one YAML document")
	case !errors.Is(err, io.EOF):
		return APIKeys{}, fmt.Errorf("parse: %w", err)
	}

	byKey := make(map[string]string, len(entries))
	for client, key := range entries {
		client = strings.TrimSpace(client)
		key = strings.TrimSpace(key)
		if client == "" {
			return APIKeys{}, errors.New("a client name is empty")
		}
		if key == "" {
			return APIKeys{}, fmt.Errorf("client %q has an empty key", client)
		}
		if dup, ok := byKey[key]; ok {
			return APIKeys{}, fmt.Errorf("clients %q and %q share the same key", dup, client)
		}
		byKey[key] = client
	}
	return APIKeys{byKey: byKey}, nil
}

// RequireAPIKey wraps next so only a request carrying "Authorization:
// Bearer <key>", with key configured in keys, reaches it. Every other
// request gets 401 with a WWW-Authenticate header and never reaches next;
// an empty keys (no entries configured) refuses every request this way,
// authentication fully closed rather than silently open.
func RequireAPIKey(keys APIKeys, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			refuseUnauthenticated(w)
			return
		}
		client, ok := keys.Lookup(token)
		if !ok {
			refuseUnauthenticated(w)
			return
		}
		log.Debug().Str("client", client).Msg("mcp request authenticated")
		next.ServeHTTP(w, r)
	})
}

// bearerToken extracts the token from r's Authorization header; ok is false
// without a "Bearer " prefix or with an empty token.
func bearerToken(r *http.Request) (token string, ok bool) {
	token, ok = strings.CutPrefix(r.Header.Get(authorizationHeader), bearerPrefix)
	return token, ok && token != ""
}

// refuseUnauthenticated writes the 401 response for a request RequireAPIKey
// does not let through.
func refuseUnauthenticated(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="puckdb-mcp"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}
