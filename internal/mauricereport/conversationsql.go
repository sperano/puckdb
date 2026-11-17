package mauricereport

import (
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// sqlArgs collects positional arguments and returns their placeholders.
type sqlArgs []any

func (a *sqlArgs) add(v any) string {
	*a = append(*a, v)
	return "$" + strconv.Itoa(len(*a))
}

// conversationSQL builds the conversation list query: the page of matching
// conversations first (filtered on conversation and owner columns and on
// EXISTS predicates over the conversation's turns, LLM calls, tool calls and
// messages, then ordered and limited on conversation columns), then the turn
// and model metadata of those rows.
// conversationID, when set, selects that one conversation instead of a
// cursor position. Every value is a placeholder.
func conversationSQL(q conversationQuery, after *cursor, conversationID string, limit int) (string, []any) {
	var args sqlArgs
	where := conversationConds(q, &args)
	if conversationID != "" {
		where = append(where, "c.id = "+args.add(conversationID)+"::uuid")
	}
	if after != nil {
		where = append(where, "(c.created_at, c.id) < ("+args.add(*after.At)+"::timestamptz, "+args.add(after.ID.String())+"::uuid)")
	}
	sql := `
		WITH page AS (
			SELECT c.id, u.id AS user_id, u.instance, u.username, c.title,
			       c.created_at, c.updated_at, c.deleted_at
			FROM maurice_conversations c
			JOIN app_users u ON u.id = c.user_id
			WHERE ` + strings.Join(where, "\n\t\t\t  AND ") + `
			ORDER BY c.created_at DESC, c.id DESC
			LIMIT ` + args.add(limit) + `
		)
		SELECT p.id::text, p.user_id::text, p.instance, p.username, p.title,
		       p.created_at, p.updated_at, p.deleted_at,
		       t.last_status, t.last_started_at,
		       t.running, t.succeeded, t.failed, t.cancelled,
		       m.providers, m.models
		FROM page p
		CROSS JOIN LATERAL (
			SELECT (array_agg(status ORDER BY turn_number DESC))[1] AS last_status,
			       max(started_at) AS last_started_at,
			       count(*) FILTER (WHERE status = 'running') AS running,
			       count(*) FILTER (WHERE status = 'succeeded') AS succeeded,
			       count(*) FILTER (WHERE status = 'failed') AS failed,
			       count(*) FILTER (WHERE status = 'cancelled') AS cancelled
			FROM maurice_turns
			WHERE conversation_id = p.id
		) t
		CROSS JOIN LATERAL (
			SELECT COALESCE(array_agg(provider ORDER BY provider, model), '{}') AS providers,
			       COALESCE(array_agg(model ORDER BY provider, model), '{}') AS models
			FROM (
				SELECT DISTINCT provider, model
				FROM maurice_llm_calls
				WHERE conversation_id = p.id AND turn_id IS NOT NULL
			) used
		) m
		ORDER BY p.created_at DESC, p.id DESC`
	return sql, args
}

// conversationConds are the predicates that select a conversation c of
// owner u under q: the owner scope, deletion, one turn meeting every
// turn-level filter, and the search. The conversation list and the overview
// share them, so both include the same conversations.
func conversationConds(q conversationQuery, args *sqlArgs) []string {
	where := []string{"u.instance = " + args.add(q.instance)}
	if q.userID != nil {
		where = append(where, "u.id = "+args.add(q.userID.String())+"::uuid")
	}
	if !q.includeDeleted {
		where = append(where, "c.deleted_at IS NULL")
	}
	if q.hasTurnFilter() {
		where = append(where, turnFilterSQL(q, args))
	}
	if q.searchPattern != "" {
		where = append(where, searchSQL(args.add(q.searchPattern)))
	}
	return where
}

// turnFilterSQL is the predicate "one turn of the conversation meets every
// turn-level filter".
func turnFilterSQL(q conversationQuery, args *sqlArgs) string {
	conds := append([]string{"t.conversation_id = c.id"}, turnConds(q, args)...)
	return "EXISTS (SELECT 1 FROM maurice_turns t WHERE " + strings.Join(conds, " AND ") + ")"
}

// turnConds are the turn-level predicates of q over a turn t: its start in
// the interval, its status, an LLM call of the provider/model and a tool
// call of the tool name. Empty when q has no turn filter.
func turnConds(q conversationQuery, args *sqlArgs) []string {
	var conds []string
	if q.from != nil {
		conds = append(conds, "t.started_at >= "+args.add(*q.from))
	}
	if q.until != nil {
		conds = append(conds, "t.started_at < "+args.add(*q.until))
	}
	if len(q.statuses) > 0 {
		conds = append(conds, "t.status = ANY("+args.add(q.statuses)+"::text[])")
	}
	if q.provider != "" || q.model != "" {
		call := []string{"l.turn_id = t.id"}
		if q.provider != "" {
			call = append(call, "l.provider = "+args.add(q.provider))
		}
		if q.model != "" {
			call = append(call, "l.model = "+args.add(q.model))
		}
		conds = append(conds, "EXISTS (SELECT 1 FROM maurice_llm_calls l WHERE "+strings.Join(call, " AND ")+")")
	}
	if q.toolName != "" {
		conds = append(conds, "EXISTS (SELECT 1 FROM maurice_tool_calls tc WHERE tc.turn_id = t.id AND tc.tool_name = "+
			args.add(q.toolName)+")")
	}
	return conds
}

// searchSQL is the predicate "the title or a retained user or assistant
// message contains the pattern", pattern being the ILIKE placeholder.
func searchSQL(pattern string) string {
	return `(c.title ILIKE ` + pattern + ` ESCAPE '\'
			       OR EXISTS (SELECT 1 FROM maurice_messages mm
			                  WHERE mm.conversation_id = c.id
			                    AND mm.role IN ('user', 'assistant')
			                    AND mm.content ILIKE ` + pattern + ` ESCAPE '\'))`
}

// scanConversation reads one row of conversationSQL.
func scanConversation(row pgx.CollectableRow) (Conversation, error) {
	var (
		c         Conversation
		status    *string
		providers []string
		models    []string
	)
	err := row.Scan(&c.ID, &c.UserID, &c.Instance, &c.Username, &c.Title,
		&c.CreatedAt, &c.UpdatedAt, &c.DeletedAt,
		&status, &c.LastTurnStartedAt,
		&c.Turns.Running, &c.Turns.Succeeded, &c.Turns.Failed, &c.Turns.Cancelled,
		&providers, &models)
	if err != nil {
		return Conversation{}, err
	}
	if status != nil {
		s := TurnStatus(*status)
		c.Status = &s
	}
	c.Models = make([]Model, len(providers))
	for i := range providers {
		c.Models[i] = Model{Provider: providers[i], Model: models[i]}
	}
	return c, nil
}
