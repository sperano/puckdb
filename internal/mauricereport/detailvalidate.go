package mauricereport

import (
	"fmt"
	"strconv"

	"github.com/google/uuid"
)

// detailRef is a validated ConversationRef.
type detailRef struct {
	instance       string
	conversationID uuid.UUID
	includeDeleted bool
}

// validateRef checks ref and returns its query form.
func validateRef(ref ConversationRef) (detailRef, error) {
	instance, err := validateInstance(ref.Instance)
	if err != nil {
		return detailRef{}, err
	}
	id, err := parseID("conversationId", ref.ConversationID)
	if err != nil {
		return detailRef{}, err
	}
	return detailRef{instance: instance, conversationID: id, includeDeleted: ref.IncludeDeleted}, nil
}

// conds are the predicates that select r's conversation c of owner u: the
// conversation list's owner scope and deletion rule, and its ID.
func (r detailRef) conds(args *sqlArgs) []string {
	q := conversationQuery{instance: r.instance, includeDeleted: r.includeDeleted}
	return append(conversationConds(q, args), "c.id = "+args.add(r.conversationID.String())+"::uuid")
}

// detailScope is the cursor scope of a detail list of r's conversation
// under parent (a turn or call ID; empty for a conversation-wide list).
// Deletion is left out: it decides whether the conversation is found, not
// which of its rows a page lists.
func detailScope(r detailRef, parent string) (string, error) {
	return scopeHash(struct {
		Instance     string    `json:"instance"`
		Conversation uuid.UUID `json:"conversation"`
		Parent       string    `json:"parent"`
	}{r.instance, r.conversationID, parent})
}

// parseRowID parses a canonical positive decimal bigint ID (an LLM call or
// tool call ID): no sign, no leading zero.
func parseRowID(field, value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != value {
		return 0, fmt.Errorf("%w: %s is not an ID", ErrInvalidArgument, field)
	}
	return id, nil
}

// payloadWindow resolves the window of a payload request.
func payloadWindow(req PayloadRequest) (offset int64, limit int, err error) {
	if req.Offset < 0 || req.Offset > MaxPayloadOffset {
		return 0, 0, fmt.Errorf("%w: offset %d is outside 0..%d", ErrInvalidArgument, req.Offset, MaxPayloadOffset)
	}
	switch {
	case req.Limit == 0:
		return req.Offset, DefaultPayloadRunes, nil
	case req.Limit < 0 || req.Limit > MaxPayloadRunes:
		return 0, 0, fmt.Errorf("%w: limit %d is outside 1..%d", ErrInvalidArgument, req.Limit, MaxPayloadRunes)
	}
	return req.Offset, req.Limit, nil
}
