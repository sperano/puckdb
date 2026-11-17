package mauricereport

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sperano/puckdb/internal/canonhash"
)

// cursorVersion is the encoding version of every cursor; another version is
// ErrInvalidCursor.
const cursorVersion = 1

// cursorKind names the list a cursor belongs to, so a cursor of one list is
// refused by another.
type cursorKind string

const (
	cursorInstances     cursorKind = "instances"
	cursorUsers         cursorKind = "users"
	cursorConversations cursorKind = "conversations"
	cursorUserActivity  cursorKind = "user_activity"
)

// cursor is the position after the last row of a page: its sort key.
// Instances sort by Name; users and user activity by (At = first_seen_at,
// ID) and conversations by (At = created_at, ID), all descending. Scope
// identifies the selection the page was read from (usersScope;
// conversationQuery.scope for conversations and user activity; empty for
// instances), so a cursor sent with another instance or filter is refused
// instead of skipping that selection's rows before its position.
type cursor struct {
	Version int        `json:"v"`
	Kind    cursorKind `json:"k"`
	Scope   string     `json:"s,omitempty"`
	Name    string     `json:"n,omitempty"`
	At      *time.Time `json:"t,omitempty"`
	ID      *uuid.UUID `json:"id,omitempty"`
}

// encodeCursor returns the opaque form of c.
func encodeCursor(c cursor) (string, error) {
	c.Version = cursorVersion
	raw, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("encode cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// decodeCursor parses a cursor of kind issued for scope; empty is the first
// page (nil).
func decodeCursor(kind cursorKind, scope, encoded string) (*cursor, error) {
	if encoded == "" {
		return nil, nil
	}
	if len(encoded) > MaxCursorBytes {
		return nil, ErrInvalidCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	var c cursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, ErrInvalidCursor
	}
	if c.Version != cursorVersion || c.Kind != kind || c.Scope != scope || !c.complete() {
		return nil, ErrInvalidCursor
	}
	return &c, nil
}

// complete reports whether c holds exactly the sort key of its kind.
func (c cursor) complete() bool {
	if c.Kind == cursorInstances {
		return c.Name != "" && c.At == nil && c.ID == nil
	}
	return c.Name == "" && c.At != nil && c.ID != nil
}

// keyCursor encodes the (at, id) position of a users, user activity or
// conversations page read from scope.
func keyCursor(kind cursorKind, scope string, at time.Time, id string) (string, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return "", fmt.Errorf("encode cursor: %w", err)
	}
	return encodeCursor(cursor{Kind: kind, Scope: scope, At: &at, ID: &parsed})
}

// usersScope is the cursor scope of the users of instance.
func usersScope(instance string) (string, error) {
	return scopeHash(struct {
		Instance string `json:"instance"`
	}{instance})
}

// scopeHash is the cursor scope of a selection: a fixed-length hash, so a
// long search string cannot push a cursor past MaxCursorBytes.
func scopeHash(selection any) (string, error) {
	hash, err := canonhash.JSON(selection)
	if err != nil {
		return "", fmt.Errorf("cursor scope: %w", err)
	}
	return hash, nil
}
