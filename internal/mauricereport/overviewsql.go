package mauricereport

import (
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Overview statements: every one starts with selectionCTEs, so the totals,
// the model breakdown and a user page read the same included rows.

// Percentiles of the latency summaries.
const (
	medianPercentile = 0.5
	p95Percentile    = 0.95
)

// durationSQL is a row's completed_at - started_at in seconds.
const durationSQL = "extract(epoch FROM completed_at - started_at)::float8"

// selectionCTEs is the WITH list of the rows q includes:
//   - scope_users: the users of the instance (or q's one user),
//   - convs: the conversations the conversation list returns for q,
//   - turns: the turns of convs meeting every turn-level filter (all of
//     their turns when q has none),
//   - calls: every LLM call of turns, plus the title generation calls of
//     convs that titleCallConds includes,
//   - merged: per conversation, the union of its terminal turns' and title
//     calls' intervals, one row per disjoint interval.
//
// users, when not nil, restricts every CTE to those owners (a user page).
// Every row carries its owner's user_id.
func selectionCTEs(q conversationQuery, users []string, args *sqlArgs) string {
	scope := []string{"u.instance = " + args.add(q.instance)}
	if q.userID != nil {
		scope = append(scope, "u.id = "+args.add(q.userID.String())+"::uuid")
	}
	conversations := conversationConds(q, args)
	if users != nil {
		page := "u.id = ANY(" + args.add(users) + "::uuid[])"
		scope = append(scope, page)
		conversations = append(conversations, page)
	}
	turns := append([]string{"TRUE"}, turnConds(q, args)...)
	titles := append([]string{"l.turn_id IS NULL"}, titleCallConds(q, args)...)
	return `WITH scope_users AS (
			SELECT u.id AS user_id FROM app_users u
			WHERE ` + and(scope) + `
		), convs AS (
			SELECT c.id, c.user_id
			FROM maurice_conversations c
			JOIN app_users u ON u.id = c.user_id
			WHERE ` + and(conversations) + `
		), turns AS (
			SELECT t.id, t.conversation_id, c.user_id, t.status, t.started_at, t.completed_at
			FROM maurice_turns t
			JOIN convs c ON c.id = t.conversation_id
			WHERE ` + and(turns) + `
		), calls AS (
			SELECT l.turn_id, l.conversation_id, t.user_id, ` + callColumns + `
			FROM maurice_llm_calls l
			JOIN turns t ON t.id = l.turn_id
			UNION ALL
			SELECT l.turn_id, l.conversation_id, c.user_id, ` + callColumns + `
			FROM maurice_llm_calls l
			JOIN convs c ON c.id = l.conversation_id
			WHERE ` + and(titles) + `
		), intervals AS (
			SELECT user_id, conversation_id, started_at, completed_at FROM turns WHERE completed_at IS NOT NULL
			UNION ALL
			SELECT user_id, conversation_id, started_at, completed_at FROM calls WHERE call_kind = 'title_generation'
		), ordered AS (
			SELECT *, max(completed_at) OVER (PARTITION BY conversation_id ORDER BY started_at, completed_at
			                                  ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING) AS previous_end
			FROM intervals
		), numbered AS (
			SELECT *, count(*) FILTER (WHERE previous_end IS NULL OR started_at > previous_end)
			              OVER (PARTITION BY conversation_id ORDER BY started_at, completed_at
			                    ROWS UNBOUNDED PRECEDING) AS island
			FROM ordered
		), merged AS (
			SELECT user_id, conversation_id, min(started_at) AS started_at, max(completed_at) AS completed_at
			FROM numbered
			GROUP BY user_id, conversation_id, island
		)`
}

// callColumns are the maurice_llm_calls l columns the calls CTE keeps.
const callColumns = `l.call_kind, l.provider, l.model, l.status, l.started_at, l.completed_at,
			       l.input_tokens, l.output_tokens, l.cache_creation_input_tokens, l.cache_read_input_tokens`

// titleCallConds are the predicates a title generation call l of an
// included conversation must meet: a start in the interval and the
// provider/model filter, on the call itself. A title call has no turn, so a
// status or tool name filter, which only a turn can meet, excludes every
// title call.
func titleCallConds(q conversationQuery, args *sqlArgs) []string {
	if len(q.statuses) > 0 || q.toolName != "" {
		return []string{"FALSE"}
	}
	var conds []string
	if q.from != nil {
		conds = append(conds, "l.started_at >= "+args.add(*q.from))
	}
	if q.until != nil {
		conds = append(conds, "l.started_at < "+args.add(*q.until))
	}
	if q.provider != "" {
		conds = append(conds, "l.provider = "+args.add(q.provider))
	}
	if q.model != "" {
		conds = append(conds, "l.model = "+args.add(q.model))
	}
	return conds
}

func and(conds []string) string {
	return strings.Join(conds, "\n\t\t\t  AND ")
}

// latencySQL summarizes durationSQL over the rows meeting filter: count,
// mean, median, 95th percentile and maximum.
func latencySQL(filter string) string {
	f := " FILTER (WHERE " + filter + ")"
	return "count(*)" + f + ", avg(" + durationSQL + ")" + f +
		fmt.Sprintf(", percentile_cont(%g) WITHIN GROUP (ORDER BY %s)%s", medianPercentile, durationSQL, f) +
		fmt.Sprintf(", percentile_cont(%g) WITHIN GROUP (ORDER BY %s)%s", p95Percentile, durationSQL, f) +
		", max(" + durationSQL + ")" + f
}

// tokenSQL is the reported sum and the reporting call count of a token
// column.
func tokenSQL(column string) string {
	return "COALESCE(sum(" + column + "), 0)::bigint, count(" + column + ")"
}

// usageSQL aggregates calls rows into the usageTargets columns.
var usageSQL = `count(*), count(*) FILTER (WHERE status = 'failed'), ` +
	tokenSQL("input_tokens") + ", " + tokenSQL("output_tokens") + ", " +
	tokenSQL("cache_creation_input_tokens") + ", " + tokenSQL("cache_read_input_tokens") + ", " +
	latencySQL("status = 'succeeded'")

// turnSQL aggregates turns rows into the activityRow turn columns.
var turnSQL = `count(DISTINCT user_id),
		count(*) FILTER (WHERE status = 'running'), count(*) FILTER (WHERE status = 'succeeded'),
		count(*) FILTER (WHERE status = 'failed'), count(*) FILTER (WHERE status = 'cancelled'),
		min(started_at), max(started_at), ` + latencySQL("status = 'succeeded'")

// activitySQL is one activity row per source row (src.user_id): the key
// columns, then the activityRow columns. match restricts each CTE to the
// source row ("TRUE" for the totals).
func activitySQL(key, source, match string) string {
	return `
		SELECT ` + key + `us.users, cv.conversations, tu.*, ca.*, ti.*, pr.seconds
		FROM ` + source + `
		CROSS JOIN LATERAL (SELECT count(*) AS users FROM scope_users WHERE ` + match + `) us
		CROSS JOIN LATERAL (SELECT count(*) AS conversations FROM convs WHERE ` + match + `) cv
		CROSS JOIN LATERAL (SELECT ` + turnSQL + ` FROM turns WHERE ` + match + `) tu
		CROSS JOIN LATERAL (SELECT ` + usageSQL + ` FROM calls WHERE ` + match + `) ca
		CROSS JOIN LATERAL (SELECT ` + usageSQL + ` FROM calls WHERE ` + match +
		` AND call_kind = 'title_generation') ti
		CROSS JOIN LATERAL (SELECT COALESCE(sum(` + durationSQL + `), 0) AS seconds
		                    FROM merged WHERE ` + match + `) pr`
}

// totalsSQL is the one activity row of everything q includes.
func totalsSQL(q conversationQuery) (string, []any) {
	var args sqlArgs
	sql := selectionCTEs(q, nil, &args) + activitySQL("", "(VALUES (NULL::uuid)) AS src(user_id)", "TRUE")
	return sql, args
}

// userActivitySQL is one row per user of users: the user ID, then the
// activity row of what q includes of theirs.
func userActivitySQL(q conversationQuery, users []string) (string, []any) {
	var args sqlArgs
	ctes := selectionCTEs(q, users, &args)
	source := "unnest(" + args.add(users) + "::uuid[]) AS src(user_id)"
	return ctes + activitySQL("src.user_id::text, ", source, "user_id = src.user_id"), args
}

// modelsSQL is the per provider/model breakdown of the calls q includes.
func modelsSQL(q conversationQuery) (string, []any) {
	var args sqlArgs
	sql := selectionCTEs(q, nil, &args) + `
		SELECT provider, model, count(DISTINCT turn_id), ` + usageSQL + `
		FROM calls
		GROUP BY provider, model
		ORDER BY provider, model`
	return sql, args
}

// activityRow is one row of activitySQL.
type activityRow struct {
	users, activeUsers int64
	activity           Activity
}

// targets are the scan targets of activitySQL's columns after the key.
func (r *activityRow) targets() []any {
	a := &r.activity
	out := []any{&r.users, &a.Conversations, &r.activeUsers,
		&a.Turns.Running, &a.Turns.Succeeded, &a.Turns.Failed, &a.Turns.Cancelled,
		&a.FirstTurnStartedAt, &a.LastTurnStartedAt}
	out = append(out, latencyTargets(&a.TurnLatency)...)
	out = append(out, usageTargets(&a.Usage)...)
	out = append(out, usageTargets(&a.TitleGeneration)...)
	return append(out, &a.ProcessingSeconds)
}

// usageTargets are the scan targets of usageSQL's columns.
func usageTargets(u *Usage) []any {
	out := []any{&u.Calls, &u.FailedCalls}
	for _, c := range []*TokenCount{&u.InputTokens, &u.OutputTokens, &u.CacheCreationInputTokens, &u.CacheReadInputTokens} {
		out = append(out, &c.Reported, &c.ReportedCalls)
	}
	return append(out, latencyTargets(&u.CallLatency)...)
}

// latencyTargets are the scan targets of latencySQL's columns.
func latencyTargets(l *Latency) []any {
	return []any{&l.Count, &l.Mean, &l.P50, &l.P95, &l.Max}
}

// finishUsage derives the unreported call counts once the row is scanned.
func finishUsage(u *Usage) {
	for _, c := range []*TokenCount{&u.InputTokens, &u.OutputTokens, &u.CacheCreationInputTokens, &u.CacheReadInputTokens} {
		c.UnreportedCalls = u.Calls - c.ReportedCalls
	}
}

func (r *activityRow) finish() {
	finishUsage(&r.activity.Usage)
	finishUsage(&r.activity.TitleGeneration)
}

// scanModel reads one row of modelsSQL.
func scanModel(row pgx.CollectableRow) (ModelUsage, error) {
	var m ModelUsage
	targets := append([]any{&m.Model.Provider, &m.Model.Model, &m.Turns}, usageTargets(&m.Usage)...)
	if err := row.Scan(targets...); err != nil {
		return ModelUsage{}, err
	}
	finishUsage(&m.Usage)
	return m, nil
}
