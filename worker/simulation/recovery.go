package simulation

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/sperano/puckdb/llm"
)

// MaxToolNameEditDistance is the cap for fuzzy tool-name matching.
// Distance 0 = exact (handled by ParseAction without entering recovery);
// distance 1-2 catches the typo modes the LLM actually exhibits
// (draft_payer, drop_palyer, set_linup). Distance 3+ is more likely a
// different tool than a typo — the 6 tool names in this codebase
// average ~12 chars and pairs are at edit distance ≥4 from each other,
// so this threshold doesn't risk cross-tool false positives.
const MaxToolNameEditDistance = 2

// playerIDPattern matches a standalone 7-digit number — the format of
// every NHL player ID in this codebase. The \b boundaries prevent
// matches inside longer numeric runs (e.g., game IDs are 10 digits;
// `\b\d{7}\b` won't fire on those).
var playerIDPattern = regexp.MustCompile(`\b\d{7}\b`)

// LevenshteinDistance returns the minimum number of single-character
// insertions, deletions, or substitutions to transform a into b. Pure
// function; safe for concurrent use; bounded inputs (tool names) keep
// the standard 2-row DP allocation negligible.
func LevenshteinDistance(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(
				curr[j-1]+1,    // insertion
				prev[j]+1,      // deletion
				prev[j-1]+cost, // substitution / match
			)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

// MatchToolName returns the canonical tool name from candidates that
// best matches `name`, plus the edit distance the match required.
// Returns ("", -1, false) if no candidate is within MaxToolNameEditDistance.
//
// Exact matches return distance 0 and short-circuit the search. Among
// fuzzy candidates, the closest one wins; ties (multiple candidates at
// the same minimum distance) return the FIRST candidate in the
// passed-in slice — callers should pass tools in a deterministic order
// (e.g., DraftTools()/DailyTools() return the same order every time).
func MatchToolName(name string, candidates []string) (canonical string, distance int, ok bool) {
	for _, c := range candidates {
		if c == name {
			return c, 0, true
		}
	}
	bestDist := -1
	bestName := ""
	for _, c := range candidates {
		d := LevenshteinDistance(name, c)
		if d > MaxToolNameEditDistance {
			continue
		}
		if bestDist == -1 || d < bestDist {
			bestDist = d
			bestName = c
		}
	}
	if bestDist == -1 {
		return "", -1, false
	}
	return bestName, bestDist, true
}

// LenientUnmarshalJSON tolerates two common LLM mistakes that break
// strict json.Unmarshal:
//
//  1. Trailing commas before } or ]: `{"a":1,}` → `{"a":1}`
//  2. Unquoted object keys: `{a:1}` → `{"a":1}`
//
// Implementation walks the input as a string-aware state machine:
// only mutates outside string literals, so a `}` or `,` inside a JSON
// string value is left alone.
//
// On success, the cleaned-up JSON is unmarshaled into v. On preprocess
// failure or unmarshal failure, the wrapped error includes the
// post-cleanup payload to aid debugging — the original raw text is
// still in the caller's hands.
func LenientUnmarshalJSON(data []byte, v any) error {
	cleaned := lenientCleanJSON(string(data))
	if err := json.Unmarshal([]byte(cleaned), v); err != nil {
		return fmt.Errorf("simulation: lenient unmarshal: %w (cleaned: %q)", err, cleaned)
	}
	return nil
}

// lenientCleanJSON applies the two fix-ups described on
// LenientUnmarshalJSON. Exposed (lowercase) for testing without going
// through unmarshal.
func lenientCleanJSON(s string) string {
	var out strings.Builder
	out.Grow(len(s))

	inString := false
	escape := false
	i := 0
	for i < len(s) {
		c := s[i]

		if inString {
			out.WriteByte(c)
			if escape {
				escape = false
			} else if c == '\\' {
				escape = true
			} else if c == '"' {
				inString = false
			}
			i++
			continue
		}

		// Outside strings.
		if c == '"' {
			inString = true
			out.WriteByte(c)
			i++
			continue
		}

		// Trailing comma: `,` followed by whitespace then `}` or `]`.
		if c == ',' {
			j := i + 1
			for j < len(s) && isJSONWhitespace(s[j]) {
				j++
			}
			if j < len(s) && (s[j] == '}' || s[j] == ']') {
				// Drop the comma; preserve the whitespace + closer.
				i++
				continue
			}
		}

		// Unquoted key: `{` or `,` followed by whitespace then an
		// identifier char, ending at `:`. We rewrite the identifier to
		// "<ident>".
		if c == '{' || c == ',' {
			out.WriteByte(c)
			j := i + 1
			for j < len(s) && isJSONWhitespace(s[j]) {
				out.WriteByte(s[j])
				j++
			}
			// Identifier must start with letter or _ — anything else
			// (including a quote) means the key is already quoted or
			// the input is something else (number, etc.).
			if j < len(s) && isIdentStart(s[j]) {
				start := j
				for j < len(s) && isIdentPart(s[j]) {
					j++
				}
				// Followed by optional whitespace, then `:` to confirm
				// this was a key rather than a value.
				k := j
				for k < len(s) && isJSONWhitespace(s[k]) {
					k++
				}
				if k < len(s) && s[k] == ':' {
					out.WriteByte('"')
					out.WriteString(s[start:j])
					out.WriteByte('"')
					i = j
					continue
				}
			}
			i++
			continue
		}

		out.WriteByte(c)
		i++
	}

	return out.String()
}

func isJSONWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// ExtractPlayerIDs scans free-form prose for 7-digit player IDs and
// returns them in first-seen order, deduplicated. Used as a desperate
// last-resort recovery when the LLM returned a tool name and prose
// instead of a JSON arguments object — typical pattern: "I want to
// draft 8478402" or "drop player_id 8479337".
//
// Caller must validate the returned IDs before acting on them; this
// function does not check that the IDs are real players or that they
// belong to the agent's roster.
func ExtractPlayerIDs(prose string) []int64 {
	matches := playerIDPattern.FindAllString(prose, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(matches))
	result := make([]int64, 0, len(matches))
	for _, m := range matches {
		id, err := strconv.ParseInt(m, 10, 64)
		if err != nil {
			continue // shouldn't happen — regex guarantees digits
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

// RecoverAction tries hard to turn an llm.ToolCall into a typed Action,
// even when the LLM emitted something off-spec. Three layers, applied
// in order:
//
//  1. Exact ParseAction (gated on knownTools). The fast path; ~99% of
//     calls land here.
//  2. Fuzzy tool name. If the name isn't legal, try edit-distance
//     matching against knownTools; on a match within
//     MaxToolNameEditDistance, retry with the canonical name.
//  3. Lenient JSON on the (possibly fuzzy-renamed) call.
//
// knownTools is the LEGAL tool set for the current phase — pass the
// result of DraftTools() during draft turns and DailyTools() during
// daily turns. The recovery layer enforces phase scope: an exact match
// against a wrong-phase tool name is rejected just as strongly as a
// nonsense name. Without this gating, a stray `set_lineup` emitted
// during the draft phase would silently parse and confuse downstream
// dispatch.
//
// Returns the parsed Action and a "recovery distance":
//   - 0  → exact path; nothing was repaired.
//   - >0 → fuzzy match used; the value is the edit distance.
//   - -1 → tool name was exact but args needed lenient cleanup.
//
// A non-zero distance is the signal Phase 3.3 metrics should record —
// it's a model-quality observation, not a bug.
func RecoverAction(call llm.ToolCall, knownTools []llm.Tool) (action Action, recoveryDistance int, err error) {
	legal := make(map[string]struct{}, len(knownTools))
	names := make([]string, 0, len(knownTools))
	for _, t := range knownTools {
		legal[t.Function.Name] = struct{}{}
		names = append(names, t.Function.Name)
	}

	// Layer 1: exact, gated on phase scope.
	if _, ok := legal[call.Function.Name]; ok {
		if act, e := ParseAction(call); e == nil {
			return act, 0, nil
		}
	}

	// Layer 2: fuzzy tool name. MatchToolName's candidates are already
	// the legal set, so the rename can only land on an in-phase tool.
	canonical, dist, ok := MatchToolName(call.Function.Name, names)
	if ok && dist > 0 {
		retried := llm.ToolCall{
			ID:   call.ID,
			Type: call.Type,
			Function: llm.ToolCallFunction{
				Name:      canonical,
				Arguments: call.Function.Arguments,
			},
		}
		if act, e := ParseAction(retried); e == nil {
			return act, dist, nil
		}
		// Fuzzy name match worked but args still failed → keep the
		// renamed call and fall into layer 3.
		call = retried
	}

	// Layer 3: lenient JSON on the (possibly fuzzy-renamed) call.
	// Re-check legality after the potential rename.
	if _, ok := legal[call.Function.Name]; ok {
		if act, e := parseActionLenient(call); e == nil {
			if dist > 0 {
				return act, dist, nil
			}
			return act, -1, nil
		}
	}

	// All three layers failed — surface a single error that names the
	// tool. Callers (Phase 3.1 activities) should log this as a
	// tool_use_failure transaction and feed a clear error string back
	// to the LLM so it can correct in the next round.
	return nil, 0, fmt.Errorf("simulation: cannot recover tool call %q (args=%q)",
		call.Function.Name, call.Function.Arguments)
}

// parseActionLenient is ParseAction but using LenientUnmarshalJSON.
// Mirrors ParseAction's switch, kept private — the public parse path
// stays strict; this fallback only fires from RecoverAction.
func parseActionLenient(call llm.ToolCall) (Action, error) {
	switch call.Function.Name {
	case ToolDraftPlayer:
		var args DraftPlayerArgs
		if err := LenientUnmarshalJSON([]byte(call.Function.Arguments), &args); err != nil {
			return nil, err
		}
		return args, nil
	case ToolSetLineup:
		var args SetLineupArgs
		if err := LenientUnmarshalJSON([]byte(call.Function.Arguments), &args); err != nil {
			return nil, err
		}
		return args, nil
	case ToolAddPlayer:
		var args AddPlayerArgs
		if err := LenientUnmarshalJSON([]byte(call.Function.Arguments), &args); err != nil {
			return nil, err
		}
		return args, nil
	case ToolClaimPlayer:
		var args ClaimPlayerArgs
		if err := LenientUnmarshalJSON([]byte(call.Function.Arguments), &args); err != nil {
			return nil, err
		}
		return args, nil
	case ToolDropPlayer:
		var args DropPlayerArgs
		if err := LenientUnmarshalJSON([]byte(call.Function.Arguments), &args); err != nil {
			return nil, err
		}
		return args, nil
	case ToolUpdateNotes:
		var args UpdateNotesArgs
		if err := LenientUnmarshalJSON([]byte(call.Function.Arguments), &args); err != nil {
			return nil, err
		}
		return args, nil
	case ToolSetTeamName:
		var args SetTeamNameArgs
		if err := LenientUnmarshalJSON([]byte(call.Function.Arguments), &args); err != nil {
			return nil, err
		}
		return args, nil
	default:
		return nil, fmt.Errorf("simulation: unknown tool %q (lenient)", call.Function.Name)
	}
}
