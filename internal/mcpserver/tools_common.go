package mcpserver

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
)

// Integer arguments
//
// Every integer tool argument goes through convertInteger, never through the
// SDK's GetInt/RequireInt: those truncate a JSON number such as 20252026.9
// to 20252026, and a later int32 conversion wraps 4315219322 to the same
// value, so a malformed argument would silently query something else.
//
// Accepted values:
//   - a JSON number (decoded as float64) that is finite, has no fractional
//     part and lies within ±maxExactJSONInteger, where float64 still holds
//     every integer exactly;
//   - a string holding a base-10 integer literal, as strconv.ParseInt reads
//     it (ASCII digits with an optional sign: "20252026", "-1", "+7").
//     The SDK always accepted numeric strings and some clients send them,
//     so they keep working; spaces, decimals, exponents and hex are refused;
//   - an int, int32 or int64 (in-process callers and tests).
//
// Every form is limited to ±maxExactJSONInteger, and a JSON null counts as
// absent. The value must then lie within the argument's bounds, which are
// clamped to the destination type's range before any conversion, so a
// narrowing cast can never wrap.

const (
	// maxExactJSONInteger is the largest integer a JSON number decoded as
	// float64 represents exactly (2^53 - 1); beyond it neighbouring integers
	// collapse onto the same float64.
	maxExactJSONInteger = 1<<53 - 1
	// minimumPositiveInteger is the smallest database ID, week or round.
	minimumPositiveInteger = 1
	// firstNHLSeasonID is the NHL's first season, 1917-1918.
	firstNHLSeasonID = 19171918
	// lastSeasonID is the largest season ID the startYear*10000 + endYear
	// form can hold.
	lastSeasonID = 99999999
	// lastPlayoffRound is the Stanley Cup Final.
	lastPlayoffRound = 4
	// maxEchoedArgumentLength caps how much of a malformed argument an
	// error message repeats.
	maxEchoedArgumentLength = 40
)

// intArg is one integer tool argument: its name and the range it accepts.
type intArg struct {
	name     string
	min, max int64
}

// Integer arguments shared by several tools. Tool-specific ones live next
// to their tool.
var (
	seasonParam   = intArg{name: "season", min: firstNHLSeasonID, max: lastSeasonID}
	gameIDParam   = intArg{name: "game_id", min: minimumPositiveInteger, max: maxExactJSONInteger}
	teamIDParam   = intArg{name: "team_id", min: minimumPositiveInteger, max: maxExactJSONInteger}
	playerIDParam = intArg{name: playerIDArg, min: minimumPositiveInteger, max: maxExactJSONInteger}
	limitParam    = intArg{name: "limit", min: 0, max: math.MaxInt32}
	roundParam    = intArg{name: "round", min: minimumPositiveInteger, max: lastPlayoffRound}
	leagueIDParam = intArg{name: leagueIDArg, min: minimumPositiveInteger, max: math.MaxInt32}
	// yahooTeamIDParam is a Yahoo team ID, stored as int4 unlike NHL team IDs.
	yahooTeamIDParam = intArg{name: "team_id", min: minimumPositiveInteger, max: math.MaxInt32}
)

// sqlInteger is a destination type of an integer argument.
type sqlInteger interface {
	int32 | int64
}

// rangeFor narrows arg's bounds to the range of T.
func rangeFor[T sqlInteger](arg intArg) (lo, hi int64) {
	lo, hi = int64(math.MinInt64), int64(math.MaxInt64)
	var zero T
	if _, ok := any(zero).(int32); ok {
		lo, hi = math.MinInt32, math.MaxInt32
	}
	return max(arg.min, lo), min(arg.max, hi)
}

// integerValue converts one raw argument value; see the policy above.
func integerValue(raw any) (int64, bool) {
	var value int64
	switch v := raw.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Trunc(v) != v ||
			v < -maxExactJSONInteger || v > maxExactJSONInteger {
			return 0, false
		}
		value = int64(v)
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, false
		}
		value = n
	case int:
		value = int64(v)
	case int32:
		value = int64(v)
	case int64:
		value = v
	default:
		return 0, false
	}
	if value < -maxExactJSONInteger || value > maxExactJSONInteger {
		return 0, false
	}
	return value, true
}

// convertInteger validates raw against arg and the range of T, and only
// then converts it.
func convertInteger[T sqlInteger](arg intArg, raw any) (T, *mcp.CallToolResult) {
	lo, hi := rangeFor[T](arg)
	value, ok := integerValue(raw)
	if !ok || value < lo || value > hi {
		return 0, mcp.NewToolResultError(fmt.Sprintf("invalid %s %s: want an integer from %d to %d",
			arg.name, formatRaw(raw), lo, hi))
	}
	return T(value), nil
}

// formatRaw prints a raw argument as the client sent it: plain digits for
// a JSON number, a quoted string for a string. Anything longer than
// maxEchoedArgumentLength is cut, so an error never echoes a huge value.
func formatRaw(raw any) string {
	var text string
	switch v := raw.(type) {
	case float64:
		text = strconv.FormatFloat(v, 'f', -1, 64)
	case string:
		text = strconv.Quote(v)
	default:
		text = fmt.Sprint(v)
	}
	if len(text) > maxEchoedArgumentLength {
		return text[:maxEchoedArgumentLength] + "..."
	}
	return text
}

// argument returns the raw value of name; a JSON null counts as absent,
// as it did for the SDK getters.
func argument(req mcp.CallToolRequest, name string) (any, bool) {
	raw, present := req.GetArguments()[name]
	return raw, present && raw != nil
}

// requireInt reads a required integer argument.
func requireInt[T sqlInteger](req mcp.CallToolRequest, arg intArg) (T, *mcp.CallToolResult) {
	raw, present := argument(req, arg.name)
	if !present {
		return 0, mcp.NewToolResultError(arg.name + " is required")
	}
	return convertInteger[T](arg, raw)
}

// optionalInt reads an optional integer argument; given is false when it
// is absent.
func optionalInt[T sqlInteger](req mcp.CallToolRequest, arg intArg) (value T, given bool, errResult *mcp.CallToolResult) {
	raw, present := argument(req, arg.name)
	if !present {
		return 0, false, nil
	}
	value, errResult = convertInteger[T](arg, raw)
	return value, errResult == nil, errResult
}

// optionalFilter reads an optional filter argument for which these tools
// have always taken 0 to mean "no filter" (clients often send 0 for none).
// Absent or 0 is not given; any other value must be within arg's bounds.
func optionalFilter[T sqlInteger](req mcp.CallToolRequest, arg intArg) (value T, given bool, errResult *mcp.CallToolResult) {
	raw, present := argument(req, arg.name)
	if n, ok := integerValue(raw); !present || (ok && n == 0) {
		return 0, false, nil
	}
	value, errResult = convertInteger[T](arg, raw)
	return value, errResult == nil, errResult
}

// optionalInt4Filter is optionalFilter for a nullable int4 query parameter.
func optionalInt4Filter(req mcp.CallToolRequest, arg intArg) (pgtype.Int4, *mcp.CallToolResult) {
	value, given, errResult := optionalFilter[int32](req, arg)
	return pgtype.Int4{Int32: value, Valid: given}, errResult
}

// optionalInt8Filter is optionalFilter for a nullable int8 query parameter.
func optionalInt8Filter(req mcp.CallToolRequest, arg intArg) (pgtype.Int8, *mcp.CallToolResult) {
	value, given, errResult := optionalFilter[int64](req, arg)
	return pgtype.Int8{Int64: value, Valid: given}, errResult
}

// limitOrDefault reads the optional limit argument; absent is defaultLimit.
// An explicit 0 is passed on as given.
func limitOrDefault(req mcp.CallToolRequest, defaultLimit int32) (int32, *mcp.CallToolResult) {
	limit, given, errResult := optionalInt[int32](req, limitParam)
	if errResult != nil || !given {
		return defaultLimit, errResult
	}
	return limit, nil
}

// requireIntSlice reads a required array of integers; every item must be
// within arg's bounds.
func requireIntSlice[T sqlInteger](req mcp.CallToolRequest, arg intArg) ([]T, *mcp.CallToolResult) {
	raw, present := argument(req, arg.name)
	if !present {
		return nil, mcp.NewToolResultError(arg.name + " is required")
	}
	items, isArray := raw.([]any)
	if !isArray {
		return nil, mcp.NewToolResultError(fmt.Sprintf("invalid %s %s: want an array of integers", arg.name, formatRaw(raw)))
	}
	values := make([]T, len(items))
	for i, item := range items {
		itemArg := intArg{name: fmt.Sprintf("%s[%d]", arg.name, i), min: arg.min, max: arg.max}
		value, errResult := convertInteger[T](itemArg, item)
		if errResult != nil {
			return nil, errResult
		}
		values[i] = value
	}
	return values, nil
}

// toolResult renders query rows as CSV, or the query error as a tool error.
func toolResult[T any](rows []T, err error) (*mcp.CallToolResult, error) {
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return ResultCSV(rows)
}

// jsonResult renders one query row as JSON, or the query error as a tool
// error.
func jsonResult[T any](row T, err error) (*mcp.CallToolResult, error) {
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return ResultJSON(row)
}

// parseDate converts one date argument: a string holding a real calendar
// date as YYYY-MM-DD. pgtype.Date.Scan is not used, as it also takes
// "infinity" and a " BC" suffix and turns 2025-02-30 into March 2, so a
// malformed date would silently query another day.
func parseDate(name string, raw any) (pgtype.Date, *mcp.CallToolResult) {
	if text, isString := raw.(string); isString {
		if day, err := time.Parse(time.DateOnly, text); err == nil {
			return pgtype.Date{Time: day, Valid: true}, nil
		}
	}
	return pgtype.Date{}, mcp.NewToolResultError(fmt.Sprintf("invalid %s %s: want a date as YYYY-MM-DD", name, formatRaw(raw)))
}

// requireDate reads the required "date" argument (YYYY-MM-DD); on failure it
// returns the tool error to send back.
func requireDate(req mcp.CallToolRequest) (pgtype.Date, *mcp.CallToolResult) {
	const name = "date"
	raw, present := argument(req, name)
	if !present {
		return pgtype.Date{}, mcp.NewToolResultError(name + " is required")
	}
	return parseDate(name, raw)
}

// optionalDate reads an optional date argument (YYYY-MM-DD); absent or
// empty is NULL.
func optionalDate(req mcp.CallToolRequest, name string) (pgtype.Date, *mcp.CallToolResult) {
	raw, present := argument(req, name)
	if !present || raw == "" {
		return pgtype.Date{}, nil
	}
	return parseDate(name, raw)
}
