package mauricereport

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// day is one UTC day, the unit of the date interval.
const day = 24 * time.Hour

// conversationQuery is a validated ConversationFilter.
type conversationQuery struct {
	instance       string
	userID         *uuid.UUID
	from, until    *time.Time // [from, until): until is the day after To
	statuses       []string
	provider       string
	model          string
	toolName       string
	searchPattern  string // ILIKE pattern, '\' escapes; empty = no search
	includeDeleted bool
}

// hasTurnFilter reports whether a turn-level predicate is set.
func (q conversationQuery) hasTurnFilter() bool {
	return q.from != nil || q.until != nil || len(q.statuses) > 0 ||
		q.provider != "" || q.model != "" || q.toolName != ""
}

// scope is the cursor scope of q: every field that selects rows, with the
// statuses sorted, since their order does not change the selection.
func (q conversationQuery) scope() (string, error) {
	statuses := slices.Clone(q.statuses)
	slices.Sort(statuses)
	return scopeHash(struct {
		Instance       string     `json:"instance"`
		UserID         *uuid.UUID `json:"user_id"`
		From           *time.Time `json:"from"`
		Until          *time.Time `json:"until"`
		Statuses       []string   `json:"statuses"`
		Provider       string     `json:"provider"`
		Model          string     `json:"model"`
		ToolName       string     `json:"tool_name"`
		SearchPattern  string     `json:"search_pattern"`
		IncludeDeleted bool       `json:"include_deleted"`
	}{q.instance, q.userID, q.from, q.until, statuses, q.provider, q.model, q.toolName, q.searchPattern, q.includeDeleted})
}

// validateFilter checks f and returns its query form.
func validateFilter(f ConversationFilter) (conversationQuery, error) {
	instance, err := validateInstance(f.Instance)
	if err != nil {
		return conversationQuery{}, err
	}
	q := conversationQuery{instance: instance, includeDeleted: f.IncludeDeleted}
	if f.UserID != "" {
		id, err := parseID("userId", f.UserID)
		if err != nil {
			return conversationQuery{}, err
		}
		q.userID = &id
	}
	if q.from, q.until, err = parseInterval(f.From, f.To); err != nil {
		return conversationQuery{}, err
	}
	if q.statuses, err = validateStatuses(f.Statuses); err != nil {
		return conversationQuery{}, err
	}
	for _, name := range []struct {
		field string
		value string
		dst   *string
	}{
		{"provider", f.Provider, &q.provider},
		{"model", f.Model, &q.model},
		{"toolName", f.ToolName, &q.toolName},
	} {
		if *name.dst, err = validateName(name.field, name.value, false); err != nil {
			return conversationQuery{}, err
		}
	}
	if q.searchPattern, err = searchPattern(f.Search); err != nil {
		return conversationQuery{}, err
	}
	return q, nil
}

// validateInstance checks a required instance name.
func validateInstance(instance string) (string, error) {
	return validateName("instance", instance, true)
}

// validateName checks a selector: valid UTF-8 without NUL, at most
// MaxNameRunes, not blank when required. Selectors are matched exactly, so
// surrounding spaces are an error rather than trimmed away.
func validateName(field, value string, required bool) (string, error) {
	if value == "" {
		if required {
			return "", fmt.Errorf("%w: %s is required", ErrInvalidArgument, field)
		}
		return "", nil
	}
	if err := checkText(field, value, MaxNameRunes); err != nil {
		return "", err
	}
	if strings.TrimSpace(value) != value {
		return "", fmt.Errorf("%w: %s has leading or trailing spaces", ErrInvalidArgument, field)
	}
	return value, nil
}

// checkText rejects invalid UTF-8, NUL (PostgreSQL text cannot hold it)
// and more than maxRunes runes.
func checkText(field, value string, maxRunes int) error {
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%w: %s is not valid text", ErrInvalidArgument, field)
	}
	if n := utf8.RuneCountInString(value); n > maxRunes {
		return fmt.Errorf("%w: %s is %d characters, at most %d", ErrInvalidArgument, field, n, maxRunes)
	}
	return nil
}

// parseID parses a canonical UUID.
func parseID(field, value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id.String() != strings.ToLower(value) {
		return uuid.UUID{}, fmt.Errorf("%w: %s is not a UUID", ErrInvalidArgument, field)
	}
	return id, nil
}

// parseInterval parses the inclusive UTC days from and to into the
// half-open instant interval [from, day after to). Either may be empty.
func parseInterval(from, to string) (*time.Time, *time.Time, error) {
	start, err := parseDay("from", from)
	if err != nil {
		return nil, nil, err
	}
	end, err := parseDay("to", to)
	if err != nil {
		return nil, nil, err
	}
	if start != nil && end != nil && end.Before(*start) {
		return nil, nil, fmt.Errorf("%w: to %s is before from %s", ErrInvalidArgument, to, from)
	}
	if end != nil {
		next := end.Add(day)
		end = &next
	}
	return start, end, nil
}

// parseDay parses a strict YYYY-MM-DD day at 00:00 UTC; empty is nil.
func parseDay(field, value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	t, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return nil, fmt.Errorf("%w: %s %q is not a YYYY-MM-DD date", ErrInvalidArgument, field, value)
	}
	return &t, nil
}

// validateStatuses checks and deduplicates the turn statuses.
func validateStatuses(statuses []TurnStatus) ([]string, error) {
	seen := make(map[TurnStatus]bool, len(statuses))
	var out []string
	for _, s := range statuses {
		switch s {
		case TurnRunning, TurnSucceeded, TurnFailed, TurnCancelled:
		default:
			return nil, fmt.Errorf("%w: unknown turn status %q", ErrInvalidArgument, s)
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, string(s))
		}
	}
	return out, nil
}

// likeEscaper escapes the ILIKE wildcards and the escape character itself,
// so the search is matched as a literal substring.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// searchPattern returns the ILIKE pattern of a search. Surrounding
// whitespace is trimmed first: blank is no search, and MaxSearchRunes bounds
// the trimmed text.
func searchPattern(search string) (string, error) {
	trimmed := strings.TrimSpace(search)
	if trimmed == "" {
		return "", nil
	}
	if err := checkText("search", trimmed, MaxSearchRunes); err != nil {
		return "", err
	}
	return "%" + likeEscaper.Replace(trimmed) + "%", nil
}

// pageLimit resolves a requested page size.
func pageLimit(limit int) (int, error) {
	switch {
	case limit == 0:
		return DefaultLimit, nil
	case limit < 0 || limit > MaxLimit:
		return 0, fmt.Errorf("%w: limit %d is outside 1..%d", ErrInvalidArgument, limit, MaxLimit)
	}
	return limit, nil
}
