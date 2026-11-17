package mauricereport

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
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
)

// cursor is the position after the last row of a page: its sort key.
// Instances sort by Name; users by (At = first_seen_at, ID) and
// conversations by (At = created_at, ID), both descending.
type cursor struct {
	Version int        `json:"v"`
	Kind    cursorKind `json:"k"`
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

// decodeCursor parses a cursor of kind; empty is the first page (nil).
func decodeCursor(kind cursorKind, encoded string) (*cursor, error) {
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
	if c.Version != cursorVersion || c.Kind != kind || !c.complete() {
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

// keyCursor encodes the (at, id) position of a users or conversations page.
func keyCursor(kind cursorKind, at time.Time, id string) (string, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return "", fmt.Errorf("encode cursor: %w", err)
	}
	return encodeCursor(cursor{Kind: kind, At: &at, ID: &parsed})
}
